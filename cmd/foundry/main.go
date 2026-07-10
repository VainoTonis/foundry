package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var (
	// apiURL is the base URL for the foundry API
	apiURL string
)

var rootCmd = &cobra.Command{
	Use:   "foundry",
	Short: "foundry CLI",
	Long:  "A CLI for interacting with the foundry API",
}

func init() {
	rootCmd.PersistentFlags().StringVar(&apiURL, "url", "http://localhost:8080", "URL of the foundry API server")
}

func main() {
	// Add subcommands
	rootCmd.AddCommand(plansCmd)

	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
