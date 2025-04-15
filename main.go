package main

import (
	"context"
	"log"

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

	// First task: create a text file
	task1 := pkg.Task{
		Name:      "generate-file",
		BaseImage: "docker.io/library/alpine",
		Commands: []string{
			"mkdir data",
			"echo 'hello world' > /data/test.txt",
			"echo 'additional content' >> /data/test.txt",
			"mkdir -p /output",
			"cat /data/test.txt > /output/test.txt",
			"cat /output/test.txt",
		},
	}

	log.Println("Executing first task...")
	if err := task1.Execute(ctx, cli); err != nil {
		log.Fatalf("Error executing first task: %v", err)
	}

	// Second task: use the file from the first task
	task2 := pkg.Task{
		Name:      "use-file",
		BaseImage: "docker.io/library/alpine",
		Dependencies: map[string][]string{
			"generate-file": {"/output/test.txt"}, // Copy test.txt from generate-file task
		},
		Commands: []string{
			"cat /output/test.txt",
			"echo 'modified by second task' >> /output/test.txt",
			"mkdir -p /final",
			"cat /output/test.txt > /final/modified.txt",
			"cat /final/modified.txt",
		},
	}

	log.Println("Executing second task...")
	if err := task2.Execute(ctx, cli); err != nil {
		log.Fatalf("Error executing second task: %v", err)
	}

	// Third task: use files from both previous tasks
	task3 := pkg.Task{
		Name:      "combine-files",
		BaseImage: "docker.io/library/alpine",
		Dependencies: map[string][]string{
			"generate-file": {"/output/test.txt"},    // Original file
			"use-file":      {"/final/modified.txt"}, // Modified file
		},
		Commands: []string{
			"mkdir -p /combined",
			"echo '--- Original file ---' > /combined/combined.txt",
			"cat /output/test.txt >> /combined/combined.txt",
			"echo '\n--- Modified file ---' >> /combined/combined.txt",
			"cat /final/modified.txt >> /combined/combined.txt",
			"cat /combined/combined.txt",
		},
	}

	log.Println("Executing third task...")
	if err := task3.Execute(ctx, cli); err != nil {
		log.Fatalf("Error executing third task: %v", err)
	}

	log.Println("All tasks completed successfully")
	log.Println("Containers are preserved with their artifacts")
}
