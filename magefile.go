//go:build mage
// +build mage

package main

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/magefile/mage/mg" // mg contains helpful utility functions, like Deps
)

// Default target to run when none is specified
// If not set, running mage will list available targets
// var Default = Build

func Run() error {
	mg.Deps(Build, startBuildkitDeamon)
	fmt.Println("Running buildvault...")
	cmd := exec.Command("./buildvault")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func startBuildkitDeamon() error {
	fmt.Println("Checking if Buildkit daemon is already running...")

	// Check if buildkitd is already running
	cmd := exec.Command("pgrep", "-x", "buildkitd")
	output, err := cmd.Output()

	if err == nil && len(output) > 0 {
		fmt.Println("Buildkit daemon is already running. Skipping start.")
		return nil
	} else if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() != 1 {
		// Exit code 1 means no process found; other exit codes should be handled as errors
		return fmt.Errorf("error checking for Buildkit daemon: %w", err)
	}

	fmt.Println("Buildkit daemon not running. Starting it...")

	// Command to start the Buildkit daemon
	cmd = exec.Command("./tools/rootless/bin/rootlesskit", "./tools/buildkit/bin/buildkitd")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	// Start the command and detach it from the current process
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start Buildkit daemon: %w", err)
	}

	// Print the process ID for debugging
	fmt.Printf("Buildkit daemon started with PID: %d\n", cmd.Process.Pid)

	return nil
}

// A build step that requires additional params, or platform specific steps for example
func Build() error {
	mg.Deps(InstallDeps)
	fmt.Println("Building...")
	cmd := exec.Command("go", "build", "-o", "buildvault", ".")
	return cmd.Run()
}

// A custom install step if you need your bin someplace other than go/bin
func Install() error {
	mg.Deps(Build)
	fmt.Println("Installing...")
	return os.Rename("./buildvault", "/usr/bin/buildvault")
}

// Manage your deps, or running package managers.
func InstallDeps() error {
	//fmt.Println("Installing Deps...")
	//cmd := exec.Command("go", "get", "github.com/stretchr/piglatin")
	//return cmd.Run()
	return nil
}

// Clean up after yourself
func Clean() {
	fmt.Println("Cleaning...")
	os.RemoveAll("MyApp")
}
