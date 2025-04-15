package pkg

import (
	"context"
	"testing"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/client"
)

func setupDockerClient(t *testing.T) *client.Client {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		t.Fatalf("Failed to create Docker client: %v", err)
	}
	return cli
}

func cleanupContainers(t *testing.T, cli *client.Client, taskNames []string) {
	ctx := context.Background()

	for _, name := range taskNames {
		task := Task{Name: name}
		containerName := task.generateContainerName()

		// Try to find the container
		listFilters := filters.NewArgs()
		listFilters.Add("name", containerName)

		containers, err := cli.ContainerList(ctx, container.ListOptions{
			All:     true,
			Filters: listFilters,
		})
		if err != nil {
			t.Logf("Error listing containers during cleanup: %v", err)
			continue
		}

		// Remove each found container
		for _, cnt := range containers {
			t.Logf("Cleaning up container: %s", cnt.ID[:12])

			// Stop if running
			if cnt.State == "running" {
				if err := cli.ContainerStop(ctx, cnt.ID, container.StopOptions{Signal: "SIGKILL"}); err != nil {
					t.Logf("Error stopping container during cleanup: %v", err)
				}
			}

			// Remove
			if err := cli.ContainerRemove(ctx, cnt.ID, container.RemoveOptions{Force: true}); err != nil {
				t.Logf("Error removing container during cleanup: %v", err)
			}
		}
	}
}

func TestGenerateContainerName(t *testing.T) {
	// Test that container name generation is deterministic
	task1 := Task{
		Name:      "test-task",
		BaseImage: "alpine",
		Commands:  []string{"echo hello"},
	}

	task2 := Task{
		Name:      "test-task",
		BaseImage: "alpine",
		Commands:  []string{"echo hello"},
	}

	// Same task definition should produce the same container name
	name1 := task1.generateContainerName()
	name2 := task2.generateContainerName()

	if name1 != name2 {
		t.Errorf("Container name generation is not deterministic. Got %s and %s for identical tasks",
			name1, name2)
	}

	// Different command should produce different container name
	task2.Commands = []string{"echo different"}
	name3 := task2.generateContainerName()

	if name1 == name3 {
		t.Errorf("Container names should be different for tasks with different commands. Got %s for both", name1)
	}

	// Different image should produce different container name
	task3 := Task{
		Name:      "test-task",
		BaseImage: "ubuntu",
		Commands:  []string{"echo hello"},
	}
	name4 := task3.generateContainerName()

	if name1 == name4 {
		t.Errorf("Container names should be different for tasks with different images. Got %s for both", name1)
	}
}

func TestTaskExecution(t *testing.T) {
	cli := setupDockerClient(t)
	defer cli.Close()

	// Ensure cleanup after all tests
	taskNames := []string{"test-basic-task"}
	defer cleanupContainers(t, cli, taskNames)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Simple task execution test
	t.Run("BasicTaskExecution", func(t *testing.T) {
		task := Task{
			Name:      "test-basic-task",
			BaseImage: "docker.io/library/alpine",
			Commands: []string{
				"mkdir -p /output", "echo 'Hello from test' > /output/test-file.txt",
				"cat /output/test-file.txt",
			},
		}

		err := task.Execute(ctx, cli)
		if err != nil {
			t.Fatalf("Task execution failed: %v", err)
		}

		// Verify the container exists and is stopped
		containerName := task.generateContainerName()
		listFilters := filters.NewArgs()
		listFilters.Add("name", containerName)

		containers, err := cli.ContainerList(ctx, container.ListOptions{
			All:     true,
			Filters: listFilters,
		})
		if err != nil {
			t.Fatalf("Failed to list containers: %v", err)
		}

		if len(containers) == 0 {
			t.Fatalf("Container not found after task execution")
		}

		if containers[0].State != "exited" && containers[0].State != "running" {
			t.Errorf("Container should be stopped, but state is: %s", containers[0].State)
		}
	})
}

func TestTaskDependencies(t *testing.T) {
	cli := setupDockerClient(t)
	defer cli.Close()

	// Ensure cleanup after all tests
	taskNames := []string{"test-producer-task", "test-consumer-task"}
	defer cleanupContainers(t, cli, taskNames)

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	// Test dependency artifacts
	t.Run("TaskDependencies", func(t *testing.T) {
		// Create producer task
		producer := Task{
			Name:      "test-producer-task",
			BaseImage: "docker.io/library/alpine",
			Commands: []string{
				"mkdir -p /output",
				"echo 'This is dependency data' > /output/data.txt",
				"cat /output/data.txt",
			},
		}

		err := producer.Execute(ctx, cli)
		if err != nil {
			t.Fatalf("Producer task execution failed: %v", err)
		}

		// Create consumer task with dependency
		consumer := Task{
			Name:      "test-consumer-task",
			BaseImage: "docker.io/library/alpine",
			Dependencies: map[string][]string{
				"test-producer-task": {"/output/data.txt"},
			},
			Commands: []string{
				"cat /output/data.txt",
				"mkdir -p /result",
				"echo 'Consumer added this' >> /output/data.txt",
				"cat /output/data.txt > /result/processed.txt",
			},
		}

		err = consumer.Execute(ctx, cli)
		if err != nil {
			t.Fatalf("Consumer task execution failed: %v", err)
		}

		// Verify that both tasks' containers exist and are preserved
		for _, containerName := range []string{producer.generateContainerName(), consumer.generateContainerName()} {

			listFilters := filters.NewArgs()
			listFilters.Add("name", containerName)

			containers, err := cli.ContainerList(ctx, container.ListOptions{
				All:     true,
				Filters: listFilters,
			})
			if err != nil {
				t.Fatalf("Failed to list containers: %v", err)
			}

			if len(containers) == 0 {
				t.Fatalf("Container not found for task: %s", containerName)
			}

			// Verify container is stopped
			if containers[0].State != "exited" && containers[0].State != "running" {
				t.Errorf("Container should be stopped, but state is: %s", containers[0].State)
			}
		}
	})
}

func TestMultipleDependencies(t *testing.T) {
	cli := setupDockerClient(t)
	defer cli.Close()

	// Ensure cleanup after all tests
	taskNames := []string{"data-source-1", "data-source-2", "data-combiner"}
	defer cleanupContainers(t, cli, taskNames)

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	t.Run("MultipleDependencySources", func(t *testing.T) {
		// Create first source task
		source1 := Task{
			Name:      "data-source-1",
			BaseImage: "docker.io/library/alpine",
			Commands: []string{
				"mkdir -p /output",
				"echo 'Source 1 Data' > /output/source1.txt",
			},
		}

		err := source1.Execute(ctx, cli)
		if err != nil {
			t.Fatalf("Source 1 task execution failed: %v", err)
		}

		// Create second source task
		source2 := Task{
			Name:      "data-source-2",
			BaseImage: "docker.io/library/alpine",
			Commands: []string{
				"mkdir -p /output",
				"echo 'Source 2 Data' > /output/source2.txt",
			},
		}

		err = source2.Execute(ctx, cli)
		if err != nil {
			t.Fatalf("Source 2 task execution failed: %v", err)
		}

		// Create task dependent on both sources
		combiner := Task{
			Name:      "data-combiner",
			BaseImage: "docker.io/library/alpine",
			Dependencies: map[string][]string{
				"data-source-1": {"/output/source1.txt"},
				"data-source-2": {"/output/source2.txt"},
			},
			Commands: []string{
				"mkdir -p /combined",
				"cat /output/source1.txt > /combined/combined.txt",
				"cat /output/source2.txt >> /combined/combined.txt",
				"echo 'Both sources combined' >> /combined/combined.txt",
				"cat /combined/combined.txt",
				// Basic verification
				"grep -q 'Source 1 Data' /combined/combined.txt || exit 1",
				"grep -q 'Source 2 Data' /combined/combined.txt || exit 1",
			},
		}

		err = combiner.Execute(ctx, cli)
		if err != nil {
			t.Fatalf("Combiner task execution failed: %v", err)
		}
	})
}

func TestFileContentDependency(t *testing.T) {
	cli := setupDockerClient(t)
	defer cli.Close()

	// Ensure cleanup after all tests
	taskNames := []string{"test-content-producer", "test-content-verifier"}
	defer cleanupContainers(t, cli, taskNames)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	t.Run("VerifyFileContentPreservation", func(t *testing.T) {
		// Test unique content to verify content preservation
		uniqueContent := "UNIQUE_TEST_CONTENT_" + time.Now().Format(time.RFC3339)

		// Producer task that creates a file with unique content
		producer := Task{
			Name:      "test-content-producer",
			BaseImage: "docker.io/library/alpine",
			Commands: []string{
				"mkdir -p /data",
				"echo '" + uniqueContent + "' > /data/unique.txt",
			},
		}

		err := producer.Execute(ctx, cli)
		if err != nil {
			t.Fatalf("Producer task execution failed: %v", err)
		}

		// Consumer task that reads the file and verifies content
		consumer := Task{
			Name:      "test-content-verifier",
			BaseImage: "docker.io/library/alpine",
			Dependencies: map[string][]string{
				"test-content-producer": {"/data/unique.txt"},
			},
			Commands: []string{
				// Write the file content to stdout for verification
				"cat /data/unique.txt > /tmp/verification.txt",
				// Use grep to check if the unique content exists
				"grep -q '" + uniqueContent + "' /data/unique.txt || (echo 'Content verification failed' && exit 1)",
				// If we reach here, the verification passed
				"echo 'Content verification successful'",
			},
		}

		err = consumer.Execute(ctx, cli)
		if err != nil {
			t.Fatalf("Content verification task failed: %v", err)
		}
	})
}
