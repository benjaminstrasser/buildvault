package pkg

import (
	"context"
	"fmt"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/client"
	"path/filepath"
)

func listContainersByName(ctx context.Context, containerName string, cli *client.Client) ([]container.Summary, error) {
	listFilters := filters.NewArgs()
	listFilters.Add("name", containerName)

	containers, err := cli.ContainerList(ctx, container.ListOptions{
		All:     true,
		Filters: listFilters,
	})
	if err != nil {
		return nil, fmt.Errorf("error checking for existing container: %w", err)
	}

	return containers, nil
}

func containerExists(ctx context.Context, containerName string, cli *client.Client) (bool, error) {
	containers, err := listContainersByName(ctx, containerName, cli)
	if err != nil {
		return false, err
	}

	return len(containers) > 0, nil
}

func cleanUpRunningContainer(ctx context.Context, containerName string, cli *client.Client) error {
	containers, err := listContainersByName(ctx, containerName, cli)
	if err != nil {
		return err
	}

	for _, containerSummary := range containers {
		containerID := containerSummary.ID
		fmt.Printf("Found existing container %s, removing it...\n", containerID[:12])

		// Stop it if it's running
		if containerSummary.State == "running" {
			if err := cli.ContainerStop(ctx, containerID, container.StopOptions{}); err != nil {
				return fmt.Errorf("error stopping existing container: %w", err)
			}
		}

		// Remove it to start fresh
		if err := cli.ContainerRemove(ctx, containerID, container.RemoveOptions{}); err != nil {
			fmt.Errorf("error removing existing container: %w", err)
		}
	}
	return nil
}

func createLongLivedContainer(ctx context.Context, containerName string, baseImage string, cli *client.Client) (container.CreateResponse, error) {
	init := true
	response, err := cli.ContainerCreate(ctx, &container.Config{
		Image: baseImage,
		Cmd:   []string{"tail", "-f", "/dev/null"}, // Keep container alive
		Tty:   true,
	}, &container.HostConfig{
		Init: &init, // This is equivalent to --init flag to indicate that an init process should be used as the PID 1 in the container. Specifying an init process ensures the usual responsibilities of an init system, such as reaping zombie processes, are performed inside the created container. This effectively allows SIGTERMS to stop the container
	}, nil, nil, containerName)
	if err != nil {
		return response, fmt.Errorf("error creating container: %w", err)
	}

	return response, nil
}

func startContainer(ctx context.Context, containerID string, cli *client.Client) error {
	// Start the container
	if err := cli.ContainerStart(ctx, containerID, container.StartOptions{}); err != nil {
		return fmt.Errorf("error starting container: %w", err)
	}
	fmt.Printf("Container started with ID: %s\n", containerID)
	return nil
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
