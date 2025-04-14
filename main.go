package main

import (
	"context"
	"log"
	"os"
	"path/filepath"

	pkg "github.com/benjaminstrasser/buildvault/pkg"
	"github.com/docker/docker/client"
)

func main() {
	ctx := context.Background()
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		log.Fatalf("Error creating Docker client: %v", err)
	}
	defer cli.Close()

	// Define artifact directory
	artifactsDir := "build-artifacts"

	// Ensure artifacts directory exists
	if err := os.MkdirAll(artifactsDir, 0755); err != nil {
		log.Fatalf("Failed to create artifacts directory: %v", err)
	}

	// First task: create a text file
	task1 := pkg.Task{
		Name:      "generate-file",
		BaseImage: "docker.io/library/alpine",
		Commands: []string{
			"echo 'hello world' > test.txt",
			"echo 'additional content' >> test.txt",
			"cat test.txt",
		},
		Artifacts:    []string{"test.txt"},
		ArtifactsDir: artifactsDir,
	}

	log.Println("Executing first task...")
	if err := task1.Execute(ctx, cli); err != nil {
		log.Fatalf("Error executing first task: %v", err)
	}

	// Define input artifacts for the second task
	// Maps local artifact path to container path
	inputArtifacts := map[string]string{
		filepath.Join(artifactsDir, "test.txt"): "/input/test.txt",
	}

	// Second task: use the file from the first task
	task2 := pkg.Task{
		Name:           "use-file",
		BaseImage:      "docker.io/library/alpine",
		InputArtifacts: inputArtifacts,
		Commands: []string{
			"cat /input/test.txt",
			"echo 'modified by second task' >> /input/test.txt",
			"cat /input/test.txt > modified.txt",
			"cat modified.txt",
		},
		Artifacts:    []string{"modified.txt"},
		ArtifactsDir: artifactsDir,
	}

	log.Println("Executing second task...")
	if err := task2.Execute(ctx, cli); err != nil {
		log.Fatalf("Error executing second task: %v", err)
	}

	log.Println("Tasks completed successfully")
	log.Printf("Artifacts available in %s directory", artifactsDir)
}
