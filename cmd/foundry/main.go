package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var (
	apiURL  string
	rootCmd = &cobra.Command{
		Use:   "foundry",
		Short: "Foundry CLI",
		Long:  "A command-line interface for managing foundry projects and plans",
	}
)

func init() {
	rootCmd.PersistentFlags().StringVar(&apiURL, "api-url", "http://localhost:8080", "Base URL of the foundry API")
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
