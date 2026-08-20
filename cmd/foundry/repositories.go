package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"
	"github.com/tonis2/foundry/internal/apiclient"
)

var repositoriesCmd = &cobra.Command{
	Use:   "repositories",
	Short: "Manage foundry repositories",
	Long:  "Manage foundry repositories via the API",
}

var repositoriesCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a new repository",
	Long:  "Create a new repository from JSON input (name, local_path, remote_url)",
	RunE: func(cmd *cobra.Command, args []string) error {
		client := apiclient.NewClient(apiURL)

		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			return fmt.Errorf("failed to read stdin: %w", err)
		}

		var repo apiclient.Repository
		if err := client.Post("/api/repositories", json.RawMessage(data), &repo); err != nil {
			return fmt.Errorf("failed to create repository: %w", err)
		}

		result, _ := json.MarshalIndent(repo, "", "  ")
		fmt.Println(string(result))
		return nil
	},
}

var repositoriesListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all repositories",
	Long:  "List all repositories",
	RunE: func(cmd *cobra.Command, args []string) error {
		client := apiclient.NewClient(apiURL)

		var repos []apiclient.Repository
		if err := client.Get("/api/repositories", &repos); err != nil {
			return fmt.Errorf("failed to list repositories: %w", err)
		}

		result, _ := json.MarshalIndent(repos, "", "  ")
		fmt.Println(string(result))
		return nil
	},
}

var repositoriesGetCmd = &cobra.Command{
	Use:   "get <id>",
	Short: "Get a specific repository",
	Long:  "Get a specific repository by ID",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		id := args[0]
		client := apiclient.NewClient(apiURL)

		var repo apiclient.Repository
		if err := client.Get(fmt.Sprintf("/api/repositories/%s", id), &repo); err != nil {
			return fmt.Errorf("failed to get repository: %w", err)
		}

		result, _ := json.MarshalIndent(repo, "", "  ")
		fmt.Println(string(result))
		return nil
	},
}

var repositoriesUpdateCmd = &cobra.Command{
	Use:   "update <id>",
	Short: "Update a repository",
	Long:  "Update a repository with optional name, local_path, and remote_url (JSON from stdin). Setting a locator field to null explicitly clears it.",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		id := args[0]
		client := apiclient.NewClient(apiURL)

		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			return fmt.Errorf("failed to read stdin: %w", err)
		}

		var repo apiclient.Repository
		if err := client.Patch(fmt.Sprintf("/api/repositories/%s", id), json.RawMessage(data), &repo); err != nil {
			return fmt.Errorf("failed to update repository: %w", err)
		}

		result, _ := json.MarshalIndent(repo, "", "  ")
		fmt.Println(string(result))
		return nil
	},
}

var repositoriesDeleteCmd = &cobra.Command{
	Use:   "delete <id>",
	Short: "Delete a repository",
	Long:  "Delete a repository by ID",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		id := args[0]
		client := apiclient.NewClient(apiURL)

		if err := client.Delete(fmt.Sprintf("/api/repositories/%s", id)); err != nil {
			return fmt.Errorf("failed to delete repository: %w", err)
		}

		fmt.Printf("Repository %s deleted successfully\n", id)
		return nil
	},
}

func init() {
	repositoriesCmd.AddCommand(repositoriesCreateCmd)
	repositoriesCmd.AddCommand(repositoriesListCmd)
	repositoriesCmd.AddCommand(repositoriesGetCmd)
	repositoriesCmd.AddCommand(repositoriesUpdateCmd)
	repositoriesCmd.AddCommand(repositoriesDeleteCmd)
}
