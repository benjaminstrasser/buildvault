package pgk

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"
)

// Task represents a container-based task with a base image and a set of commands to run.
type Task struct {
	Name      string
	BaseImage string   // Base Docker image to use.
	Commands  []string // Slice of commands to execute inside the container.
	Artifacts []string
}

// Execute runs the task:
// 1. Pulls the base image.
// 2. Creates and starts a container with the base image.
// 3. Executes each command in order.
// 4. Cleans up by stopping and removing the container.
func (t *Task) Execute(ctx context.Context, cli *client.Client) error {
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
