package pkg

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"
	"io"
	"os"
	"path/filepath"
)

// Task represents a container-based task with a base image and a set of commands to run.
type Task struct {
	Name         string              // Name of the task (used for container identification)
	BaseImage    string              // Base Docker image to use
	Commands     []string            // Slice of commands to execute inside the container
	Dependencies map[string][]string // Map of task name to file patterns to copy from that task
}

// generateContainerName creates a deterministic name for the task container
func (t *Task) generateContainerName() string {
	// Create a hash based on task name, image, and commands for uniqueness
	hasher := sha256.New()
	hasher.Write([]byte(t.Name))
	hasher.Write([]byte(t.BaseImage))

	// Include commands in the hash
	commandsJSON, _ := json.Marshal(t.Commands)
	hasher.Write(commandsJSON)

	hash := hex.EncodeToString(hasher.Sum(nil))[:12]
	return fmt.Sprintf("buildvault_%s_%s", t.Name, hash)
}

// findTaskContainer looks for a container for the specified task
func findTaskContainer(ctx context.Context, cli *client.Client, taskName string) (string, bool, error) {
	// Search for containers with the task name in their name
	filters := filters.NewArgs()
	filters.Add("name", fmt.Sprintf("buildvault_%s_", taskName))

	containers, err := cli.ContainerList(ctx, container.ListOptions{
		All:     true, // Include stopped containers
		Filters: filters,
	})
	if err != nil {
		return "", false, fmt.Errorf("error searching for task container: %w", err)
	}

	if len(containers) > 0 {
		return containers[0].ID, true, nil
	}

	return "", false, nil
}

// copyBetweenContainers copies files from one container to another
func copyBetweenContainers(ctx context.Context, cli *client.Client, sourceContainerID, targetContainerID, sourcePath, targetPath string) error {
	// Get file content from source container
	reader, _, err := cli.CopyFromContainer(ctx, sourceContainerID, sourcePath)
	if err != nil {
		return fmt.Errorf("error copying from source container: %w", err)
	}
	defer reader.Close()

	// Create target directory if needed
	targetDir := filepath.Dir(targetPath)
	if targetDir != "." {
		execResp, err := cli.ContainerExecCreate(ctx, targetContainerID, container.ExecOptions{
			Cmd: []string{"mkdir", "-p", targetDir},
		})
		if err != nil {
			return fmt.Errorf("error creating directory in target container: %w", err)
		}
		if err := cli.ContainerExecStart(ctx, execResp.ID, container.ExecStartOptions{}); err != nil {
			return fmt.Errorf("error creating directory in target container: %w", err)
		}
	}

	// Copy to target container
	err = cli.CopyToContainer(ctx, targetContainerID, targetDir, reader, container.CopyToContainerOptions{})
	if err != nil {
		return fmt.Errorf("error copying to target container: %w", err)
	}

	return nil
}

// Execute runs the task:
// 1. Creates or reuses a container with a deterministic name based on task properties
// 2. Copies artifacts from dependency task containers
// 3. Executes commands in the container
// 4. Stops the container but keeps it for future reference
func (t *Task) Execute(ctx context.Context, cli *client.Client) error {
	containerName := t.generateContainerName()
	fmt.Printf("Task: %s (Container: %s)\n", t.Name, containerName)

	var containerID string

	filters := filters.NewArgs()
	filters.Add("name", containerName)

	containers, err := cli.ContainerList(ctx, container.ListOptions{
		All:     true,
		Filters: filters,
	})
	if err != nil {
		return fmt.Errorf("error checking for existing container: %w", err)
	}

	if len(containers) > 0 {
		containerID = containers[0].ID
		fmt.Printf("Found existing container %s, removing it...\n", containerID[:12])

		// Stop it if it's running
		if containers[0].State == "running" {
			if err := cli.ContainerStop(ctx, containerID, container.StopOptions{}); err != nil {
				return fmt.Errorf("error stopping existing container: %w", err)
			}
		}

		// Remove it to start fresh
		if err := cli.ContainerRemove(ctx, containerID, container.RemoveOptions{}); err != nil {
			return fmt.Errorf("error removing existing container: %w", err)
		}
	}

	// Pull the image
	fmt.Printf("Pulling image: %s\n", t.BaseImage)
	reader, err := cli.ImagePull(ctx, t.BaseImage, image.PullOptions{})
	if err != nil {
		return fmt.Errorf("error pulling image: %w", err)
	}
	io.Copy(os.Stdout, reader)

	// Create a new container with our specific name
	resp, err := cli.ContainerCreate(ctx, &container.Config{
		Image: t.BaseImage,
		Cmd:   []string{"tail", "-f", "/dev/null"}, // Keep container alive
		Tty:   true,
	}, nil, nil, nil, containerName) // Use our deterministic name
	if err != nil {
		return fmt.Errorf("error creating container: %w", err)
	}

	containerID = resp.ID

	// Start the container
	if err := cli.ContainerStart(ctx, containerID, container.StartOptions{}); err != nil {
		return fmt.Errorf("error starting container: %w", err)
	}
	fmt.Printf("Container started with ID: %s\n", containerID[:12])

	// Process dependencies if any
	if len(t.Dependencies) > 0 {
		fmt.Println("Processing dependencies:")

		for dependencyTaskName, filePaths := range t.Dependencies {
			// Find the container for the dependency task
			sourceID, found, err := findTaskContainer(ctx, cli, dependencyTaskName)
			if err != nil {
				return err
			}

			if !found {
				return fmt.Errorf("dependency container for task '%s' not found", dependencyTaskName)
			}

			fmt.Printf("  Found dependency container for task '%s': %s\n", dependencyTaskName, sourceID[:12])

			// Copy each file from the dependency container
			for _, path := range filePaths {
				fmt.Printf("  Copying %s from task '%s' to current task\n", path, dependencyTaskName)

				// By default, we copy to the same path in the target container
				if err := copyBetweenContainers(ctx, cli, sourceID, containerID, path, path); err != nil {
					return fmt.Errorf("error copying dependency file %s: %w", path, err)
				}
			}
		}
	}

	// Execute all commands in sequence
	for idx, cmd := range t.Commands {
		fmt.Printf("Executing command %d: %s\n", idx+1, cmd)

		execResp, err := cli.ContainerExecCreate(ctx, containerID, container.ExecOptions{
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

		stdcopy.StdCopy(os.Stdout, os.Stderr, attachResp.Reader)
		fmt.Println() // Add newline for command output separation
	}

	// Stop the container but don't remove it - it will be available for future tasks
	fmt.Printf("Sending SIGKILL to conatiner: %s\n", containerID)
	if err := cli.ContainerStop(ctx, containerID, container.StopOptions{
		Signal: "SIGKILL",
	}); err != nil {
		return fmt.Errorf("error stopping container: %w", err)
	}

	fmt.Printf("Task '%s' execution complete. Container '%s' is stopped but preserved.\n",
		t.Name, containerName)

	return nil
}
