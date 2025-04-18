package pkg

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"
	imagetypes "github.com/docker/docker/api/types/image"
	"io"
	"os"
	"slices"
)

// Task represents a container-based task with a base image and a set of commands to run.
type Task struct {
	Name         string       // Name of the task (used for container identification)
	BaseImage    string       // Base Docker image to use
	Commands     []string     // Slice of commands to execute inside the container
	Dependencies []Dependency // Map of task name to file patterns to copy from that task
	containerID  string       // id of the docker container
}

type Artifact struct {
	From string `json:"from"`
	To   string `json:"to"`
}

type Dependency struct {
	Task      *Task
	Artifacts []Artifact `json:"artifacts"`
}

// generateContainerName creates a deterministic name for the task container
func (t *Task) generateContainerName() string {
	hash := t.generateHash()
	return fmt.Sprintf("buildvault_%s_%s", t.Name, hash)
}

func (t *Task) generateHash() string {
	// Create a hash based on task name, image, and commands for uniqueness
	hasher := sha256.New()
	hasher.Write([]byte(t.Name))
	hasher.Write([]byte(t.BaseImage))

	// Include commands in the hash
	commandsJSON, _ := json.Marshal(t.Commands)
	hasher.Write(commandsJSON)

	// Loop over dependencies and include them in the hash
	for _, dependency := range t.Dependencies {
		hasher.Write([]byte(dependency.Task.Name))
		for _, pattern := range dependency.Artifacts {
			hasher.Write([]byte(pattern.To))
			hasher.Write([]byte(pattern.From))
		}
	}

	hash := hex.EncodeToString(hasher.Sum(nil))[:12]
	return hash
}

func imageExistsLocally(cli *client.Client, baseImage string) (bool, error){
	images, err := cli.ImageList(context.Background(), imagetypes.ListOptions{})
	if err != nil {
		return false, fmt.Errorf("unable to get images, please make sure that docker daemon is up and running: %w", err)
	}

	for _, image := range images {
		// Check if the image name matches the one we are looking for
		if image.RepoTags != nil {
			for _, tag := range image.RepoTags {
				// fmt.Println("image tag:",tag)
				// fmt.Println("image name:",baseImage)
				if tag == baseImage {
					return true, nil
				}
			}
		}
	}
	return false, nil
}


func pullImage(ctx context.Context, cli *client.Client, t *Task) error {
	// Check if the image already exists locally
    exists, err := imageExistsLocally(cli, t.BaseImage)
    if err != nil {
        return fmt.Errorf("failed to check for image: %w", err)
    }
    
    if exists {
        fmt.Printf("Image %s already exists locally\n", t.BaseImage)
        return nil
    }
    
    // Image doesn't exist, pull it
    fmt.Printf("Pulling image: %s\n", t.BaseImage)
    reader, err := cli.ImagePull(ctx, t.BaseImage, imagetypes.PullOptions{})
    if err != nil {
        return fmt.Errorf("failed to pull image: %w", err)
    }
    defer reader.Close()
    
    // Stream the pull progress to stdout
    if _, err := io.Copy(os.Stdout, reader); err != nil {
        return fmt.Errorf("error streaming pull output: %w", err)
    }
    
    return nil
}

// findTaskContainer looks for a container for the specified task
func findTaskContainer(ctx context.Context, cli *client.Client, taskName string) (string, bool, error) {
	// Search for containers with the task name in their name
	listFilters := filters.NewArgs()
	listFilters.Add("name", fmt.Sprintf("buildvault_%s_", taskName))

	containers, err := cli.ContainerList(ctx, container.ListOptions{
		All:     true, // Include stopped containers
		Filters: listFilters,
	})
	if err != nil {
		return "", false, fmt.Errorf("error searching for task container: %w", err)
	}

	if len(containers) > 0 {
		return containers[0].ID, true, nil
	}

	return "", false, nil
}

func (t *Task) isCircularDependencyFree(ctx context.Context, parentHashes []string) bool {
	if len(t.Dependencies) == 0 {
		return true
	}

	currentHash := t.generateHash()

	// Check if current task is already in the parent chain (circular dependency)
	if slices.Contains(parentHashes, currentHash) {
		return false
	}

	// Create a new slice for this level to avoid modifying the original
	newParentHashes := append(append([]string{}, parentHashes...), currentHash)

	// Check all dependencies recursively
	for _, dependency := range t.Dependencies {
		if !dependency.Task.isCircularDependencyFree(ctx, newParentHashes) {
			return false
		}
	}

	return true
}

func (t *Task) executeDependenciesAndCopyArtifacts(ctx context.Context, cli *client.Client) error {
	if len(t.Dependencies) == 0 {
		fmt.Println("No dependencies found")
		return nil
	}

	if !t.isCircularDependencyFree(ctx, []string{}) {
		fmt.Println("Circular dependency found")
	}

	fmt.Println("Processing dependencies:")

	fmt.Println("Executing dependencies:")
	// TODO goroutines for parallelism
	for _, dependency := range t.Dependencies {
		fmt.Printf("- %s\n", dependency.Task.Name)
		if err := dependency.Task.Execute(ctx, cli); err != nil {
			return fmt.Errorf("error executing task dependency %s:  %w", dependency.Task.Name, err)
		}
	}

	fmt.Println("Copying artifacts from dependencies:")
	// run afterward to ensure ordering is kept consistent after goroutines run
	for _, dependency := range t.Dependencies {
		fmt.Printf("- %s\n", dependency.Task.Name)
		for _, artifact := range dependency.Artifacts {
			fmt.Printf("  Copying %s from task '%s' to current task at %s\n", artifact.From, dependency.Task.Name, artifact.To)
			if err := copyBetweenContainers(ctx, cli, dependency.Task.containerID, t.containerID, artifact.From, artifact.To); err != nil {
				return fmt.Errorf("error copying dependency file %s: %w", artifact.From, err)
			}
		}
	}

	return nil
}

func (t *Task) executeCommands(ctx context.Context, cli *client.Client) error {
	// Execute all commands in sequence
	for idx, cmd := range t.Commands {
		fmt.Printf("Executing command %d: %s\n", idx+1, cmd)

		execResp, err := cli.ContainerExecCreate(ctx, t.containerID, container.ExecOptions{
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

		_, err = stdcopy.StdCopy(os.Stdout, os.Stderr, attachResp.Reader)
		if err != nil {
			return fmt.Errorf("error StdCopy: %w", err)
		}
		fmt.Println() // Add newline for command output separation

		// Check the exit code of the command
		inspectResp, err := cli.ContainerExecInspect(ctx, execResp.ID)
		if err != nil {
			return fmt.Errorf("error inspecting exec for command '%s': %w", cmd, err)
		}

		if inspectResp.ExitCode != 0 {
			return fmt.Errorf("command '%s' failed with exit code %d", cmd, inspectResp.ExitCode)
		}
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

	if err := cleanUpRunningContainer(ctx, containerName, cli); err != nil {
		return err
	}

	if err := pullImage(ctx, cli, t); err != nil {
		return err
	}

	resp, err := createLongLivedContainer(ctx, containerName, t.BaseImage, cli)
	if err != nil {
		return err
	}
	t.containerID = resp.ID

	if err := startContainer(ctx, t.containerID, cli); err != nil {
		return err
	}

	if err := t.executeDependenciesAndCopyArtifacts(ctx, cli); err != nil {
		return err
	}

	if err := t.executeCommands(ctx, cli); err != nil {
		return err
	}

	if err := stopContainer(ctx, t.containerID, cli); err != nil {
		return err
	}

	fmt.Printf("Task '%s' execution complete. Container '%s' is stopped but preserved.\n",
		t.Name, containerName)

	return nil
}
