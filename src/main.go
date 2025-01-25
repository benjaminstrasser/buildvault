package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

// Root command
var rootCmd = &cobra.Command{
	Use:   "mycli",
	Short: "MyCLI is a simple example CLI built with Cobra",
	Long:  `MyCLI is an example project to demonstrate building a CLI using the Cobra library in Go.`,
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println("Welcome to MyCLI! Use --help to see available commands.")
	},
}

// Hello command
var helloCmd = &cobra.Command{
	Use:   "hello",
	Short: "Prints a hello message",
	Long:  `The hello command prints a personalized hello message.`,
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println("Hello, world!")
	},
}

// Goodbye command
var goodbyeCmd = &cobra.Command{
	Use:   "goodbye",
	Short: "Prints a goodbye message",
	Long:  `The goodbye command prints a personalized goodbye message.`,
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println("Goodbye! See you soon!")
	},
}

// Initialize the CLI
func init() {
	// Add subcommands to the root command
	rootCmd.AddCommand(helloCmd)
	rootCmd.AddCommand(goodbyeCmd)
}

// Main function
func main() {
	// Execute the root command
	if err := rootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}