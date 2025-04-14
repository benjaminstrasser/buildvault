package main

import (
	"context"
	pgk "github.com/benjaminstrasser/buildvault/pgk"
	"github.com/docker/docker/client"
)

func main() {
	ctx := context.Background()
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		panic(err)
	}
	defer cli.Close()

	task := pgk.Task{
		Name:      "test",
		BaseImage: "docker.io/library/alpine",
		Commands: []string{
			"echo 'hello world' > test.txt",
			"cat test.txt",
		},
		Artifacts: []string{"test.txt"},
	}

	task.Execute(ctx, cli)
}
