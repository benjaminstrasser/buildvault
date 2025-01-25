//go:build mage
// +build mage

package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/magefile/mage/mg" // mg contains helpful utility functions, like Deps
)

// Default target to run when none is specified
// If not set, running mage will list available targets
// var Default = Build


func Test() error {
	fmt.Println("Testing...")
	cmd := exec.Command("echo", "$XDG_RUNTIME_DIR")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func Run() error {
	mg.Deps(Build)
	fmt.Println("Running buildvault and piping output to buildctl...")

	// Create a pipe
	r, w, _ := os.Pipe()

	// First command: ./buildvault
	buildvaultCmd := exec.Command("./buildvault")
	buildvaultCmd.Stdout = w
	buildvaultCmd.Stderr = os.Stderr

	// Second command: ./tools/buildkit/bin/buildctl build
	buildctlCmd := exec.Command("./tools/buildkit/bin/buildctl", "--addr", "unix://$XDG_RUNTIME_DIR/buildkit/buildkitd.sock", "build")
	buildctlCmd.Stdin = r
	buildctlCmd.Stdout = os.Stdout
	buildctlCmd.Stderr = os.Stderr

	// Start the first command
	if err := buildvaultCmd.Start(); err != nil {
		return fmt.Errorf("failed to start buildvault: %w", err)
	}

	// Start the second command
	if err := buildctlCmd.Start(); err != nil {
		return fmt.Errorf("failed to start buildctl: %w", err)
	}

	// Close the write-end of the pipe in the current process
	w.Close()

	// Wait for both commands to finish
	if err := buildvaultCmd.Wait(); err != nil {
		return fmt.Errorf("buildvault command failed: %w", err)
	}

	if err := buildctlCmd.Wait(); err != nil {
		return fmt.Errorf("buildctl command failed: %w", err)
	}

	return nil
}

func StartBuildkit() error {

	mg.Deps(StopBuildkit)

	fmt.Println("Starting Buildkit it...")

	// Command to start the Buildkit daemon
	cmd := exec.Command("./tools/rootless/bin/rootlesskit", "./tools/buildkit/bin/buildkitd")

	// Start the command and detach it from the current process
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start Buildkit daemon: %w", err)
	}

	// Print the process ID for debugging
	fmt.Printf("Buildkit daemon started with PID: %d\n", cmd.Process.Pid)

	return nil
}

func StopBuildkit() error {
	fmt.Println("Checking if Buildkit daemon is already running...")

	// Check if rootlesskit is already running
	cmd := exec.Command("pgrep", "-x", "rootlesskit")
	output, err := cmd.Output()
	exitErr, ok := err.(*exec.ExitError)

	if err == nil && len(output) > 0 {
		fmt.Println("Buildkit daemon is running.")
	} else if ok && exitErr.ExitCode() == 1 {
		fmt.Println("Buildkit daemon is not running.")
		return nil
	} else if ok && exitErr.ExitCode() != 1 {
		// Exit code 1 means no process found; other exit codes should be handled as errors
		return fmt.Errorf("error checking for Buildkit daemon: %w", err)
	}

	fmt.Println("Stopping Buildkit daemon...")

	// Command to stop the Buildkit daemon
	cmd = exec.Command("pkill", "-x", "rootlesskit")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	// Run the command to terminate rootlesskit
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to stop Buildkit daemon: %w", err)
	}

	fmt.Println("Waiting for Buildkit daemon and its child processes to stop...")

	// Check if any `rootlesskit` processes are still running
	for {
		cmd = exec.Command("pgrep", "-x", "rootlesskit")
		output, err = cmd.Output()

		if err != nil {
			// If no processes are found (exit code 1), break the loop
			if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
				fmt.Println("All Buildkit daemon processes stopped.")
				break
			}
			// Handle other errors
			return fmt.Errorf("error checking for Buildkit daemon processes: %w", err)
		}

		// Output contains remaining process IDs, so continue waiting
		if len(output) > 0 {
			fmt.Printf("Still waiting for processes: %s\n", strings.TrimSpace(string(output)))
			time.Sleep(1 * time.Second) // Wait before rechecking
		}
	}

	fmt.Println("Buildkit daemon and all child processes are completely stopped.")
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
