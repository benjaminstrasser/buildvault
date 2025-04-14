package pkg

import (
	"archive/tar"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"
)

// Task represents a container-based task with a base image and a set of commands to run.
type Task struct {
	Name           string
	BaseImage      string            // Base Docker image to use.
	Commands       []string          // Slice of commands to execute inside the container.
	Artifacts      []string          // Artifact files to extract from the container
	ArtifactsDir   string            // Directory to store artifacts (defaults to "artifacts")
	InputArtifacts map[string]string // Map of input artifacts to copy into the container (source path -> container path)
}

// Execute runs the task:
// 1. Pulls the base image.
// 2. Creates and starts a container with the base image.
// 3. Executes each command in order.
// 4. Extracts any artifacts from the container.
// 5. Cleans up by stopping and removing the container.
func (t *Task) Execute(ctx context.Context, cli *client.Client) error {
	// Set default artifacts directory if not provided
	if t.ArtifactsDir == "" {
		t.ArtifactsDir = "artifacts"
	}

	// Ensure artifacts directory exists
	if err := os.MkdirAll(t.ArtifactsDir, 0755); err != nil {
		return fmt.Errorf("failed to create artifacts directory: %w", err)
	}

	// Pull the image
	fmt.Printf("Pulling image: %s\n", t.BaseImage)
	reader, err := cli.ImagePull(ctx, t.BaseImage, image.PullOptions{})
	if err != nil {
		return fmt.Errorf("error pulling image: %w", err)
	}
	// Print the pull log
	io.Copy(os.Stdout, reader)

	// Create the container, using a command that keeps the container running.
	resp, err := cli.ContainerCreate(ctx, &container.Config{
		Image: t.BaseImage,
		Cmd:   []string{"tail", "-f", "/dev/null"},
		Tty:   true,
	}, nil, nil, nil, "")
	if err != nil {
		return fmt.Errorf("error creating container: %w", err)
	}

	// Copy input artifacts into the container if any
	for sourcePath, containerPath := range t.InputArtifacts {
		fmt.Printf("Copying input artifact from %s to container path %s\n", sourcePath, containerPath)

		// Create a tar archive of the file
		srcFile, err := os.Open(sourcePath)
		if err != nil {
			return fmt.Errorf("error opening input artifact %s: %w", sourcePath, err)
		}
		defer srcFile.Close()

		// Create parent directory in container if needed
		if containerDir := filepath.Dir(containerPath); containerDir != "." {
			execResp, err := cli.ContainerExecCreate(ctx, resp.ID, container.ExecOptions{
				Cmd: []string{"mkdir", "-p", containerDir},
			})
			if err != nil {
				return fmt.Errorf("error creating directory for input artifact: %w", err)
			}
			if err := cli.ContainerExecStart(ctx, execResp.ID, container.ExecStartOptions{}); err != nil {
				return fmt.Errorf("error creating directory for input artifact: %w", err)
			}
		}

		// Copy the file content to the container
		err = cli.CopyToContainer(ctx, resp.ID, filepath.Dir(containerPath), srcFile, container.CopyToOptions{})
		if err != nil {
			return fmt.Errorf("error copying input artifact to container: %w", err)
		}
	}

	// Start the container
	if err := cli.ContainerStart(ctx, resp.ID, container.StartOptions{}); err != nil {
		return fmt.Errorf("error starting container: %w", err)
	}
	fmt.Println("Container started with ID:", resp.ID)

	// Execute each command sequentially
	for idx, cmd := range t.Commands {
		fmt.Printf("Executing command %d: %s\n", idx+1, cmd)
		// Create the execution instance for the command
		execResp, err := cli.ContainerExecCreate(ctx, resp.ID, container.ExecOptions{
			Cmd:          []string{"sh", "-c", cmd},
			AttachStdout: true,
			AttachStderr: true,
		})
		if err != nil {
			return fmt.Errorf("error creating exec for command '%s': %w", cmd, err)
		}
		attachResp, err := cli.ContainerExecAttach(ctx, execResp.ID, container.ExecAttachOptions{})
		if err != nil {
			return fmt.Errorf("error attaching to exec for command '%s': %w", cmd, err)
		}
		defer attachResp.Close()

		// Copy the command's output to standard out and error.
		stdcopy.StdCopy(os.Stdout, os.Stderr, attachResp.Reader)
		fmt.Println() // Newline for better separation of command outputs
	}

	// Extract artifacts from the container
	if len(t.Artifacts) > 0 {
		fmt.Println("Extracting artifacts:")
		for _, artifact := range t.Artifacts {
			outputPath := filepath.Join(t.ArtifactsDir, filepath.Base(artifact))
			fmt.Printf("  - %s -> %s\n", artifact, outputPath)

			// Create a reader for the file content from the container
			reader, _, err := cli.CopyFromContainer(ctx, resp.ID, artifact)
			if err != nil {
				return fmt.Errorf("error extracting artifact '%s': %w", artifact, err)
			}
			defer reader.Close()

			// Extract the file content from the tar archive
			// The reader returns a tar archive, so we need to extract the actual file
			if err := ExtractFileFromTar(reader, outputPath); err != nil {
				return fmt.Errorf("error saving artifact '%s': %w", artifact, err)
			}
		}
	}

	// Stop the container
	if err := cli.ContainerStop(ctx, resp.ID, container.StopOptions{}); err != nil {
		return fmt.Errorf("error stopping container: %w", err)
	}

	// Remove the container
	if err := cli.ContainerRemove(ctx, resp.ID, container.RemoveOptions{}); err != nil {
		return fmt.Errorf("error removing container: %w", err)
	}

	fmt.Println("Task execution complete, container cleaned up.")
	return nil
}

// ExtractFileFromTar extracts a file from a tar archive
func ExtractFileFromTar(tarReader io.ReadCloser, destPath string) error {
	// Use standard tar package for extraction
	tr := tar.NewReader(tarReader)

	tr := tar.NewReader(tarReader)

	// Create directory for file if it doesn't exist
	if err := os.MkdirAll(filepath.Dir(destPath), 0755); err != nil {
		return err
	}

	// Iterate through the files in the archive
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break // End of archive
		}
		if err != nil {
			return err
		}

		// Skip directories, we only want files
		if header.Typeflag != tar.TypeReg {
			continue
		}

		// Create the file
		file, err := os.Create(destPath)
		if err != nil {
			return err
		}
		defer file.Close()

		// Copy the content
		if _, err := io.Copy(file, tr); err != nil {
			return err
		}

		// We only need the first file (the actual content)
		break
	}

	return nil
}
