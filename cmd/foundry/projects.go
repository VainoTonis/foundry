package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"
	"github.com/tonis2/foundry/internal/apiclient"
)

var projectsCmd = &cobra.Command{
	Use:   "projects",
	Short: "Manage foundry projects",
	Long:  "Manage foundry projects via the API",
}

var projectsCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a new project",
	Long:  "Create a new project from JSON input (name, repo_path)",
	RunE: func(cmd *cobra.Command, args []string) error {
		client := apiclient.NewClient(apiURL)
		
		var body struct {
			Name     string `json:"name"`
			RepoPath string `json:"repo_path"`
		}
		
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			return fmt.Errorf("failed to read stdin: %w", err)
		}
		
		if err := json.Unmarshal(data, &body); err != nil {
			return fmt.Errorf("failed to parse JSON: %w", err)
		}
		
		var project apiclient.Project
		if err := client.Post("/api/projects", body, &project); err != nil {
			return fmt.Errorf("failed to create project: %w", err)
		}
		
		result, _ := json.MarshalIndent(project, "", "  ")
		fmt.Println(string(result))
		return nil
	},
}

var projectsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all projects",
	Long:  "List all projects",
	RunE: func(cmd *cobra.Command, args []string) error {
		client := apiclient.NewClient(apiURL)
		
		var projects []apiclient.Project
		if err := client.Get("/api/projects", &projects); err != nil {
			return fmt.Errorf("failed to list projects: %w", err)
		}
		
		result, _ := json.MarshalIndent(projects, "", "  ")
		fmt.Println(string(result))
		return nil
	},
}

var projectsGetCmd = &cobra.Command{
	Use:   "get <id>",
	Short: "Get a specific project",
	Long:  "Get a specific project by ID",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		id := args[0]
		client := apiclient.NewClient(apiURL)
		
		var project apiclient.Project
		if err := client.Get(fmt.Sprintf("/api/projects/%s", id), &project); err != nil {
			return fmt.Errorf("failed to get project: %w", err)
		}
		
		result, _ := json.MarshalIndent(project, "", "  ")
		fmt.Println(string(result))
		return nil
	},
}

var projectsUpdateCmd = &cobra.Command{
	Use:   "update <id>",
	Short: "Update a project",
	Long:  "Update a project with optional name and repo_path (JSON from stdin)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		id := args[0]
		client := apiclient.NewClient(apiURL)
		
		var body struct {
			Name     *string `json:"name"`
			RepoPath *string `json:"repo_path"`
		}
		
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			return fmt.Errorf("failed to read stdin: %w", err)
		}
		
		if err := json.Unmarshal(data, &body); err != nil {
			return fmt.Errorf("failed to parse JSON: %w", err)
		}
		
		var project apiclient.Project
		if err := client.Patch(fmt.Sprintf("/api/projects/%s", id), body, &project); err != nil {
			return fmt.Errorf("failed to update project: %w", err)
		}
		
		result, _ := json.MarshalIndent(project, "", "  ")
		fmt.Println(string(result))
		return nil
	},
}

var projectsDeleteCmd = &cobra.Command{
	Use:   "delete <id>",
	Short: "Delete a project",
	Long:  "Delete a project by ID",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		id := args[0]
		client := apiclient.NewClient(apiURL)
		
		if err := client.Delete(fmt.Sprintf("/api/projects/%s", id)); err != nil {
			return fmt.Errorf("failed to delete project: %w", err)
		}
		
		fmt.Printf("Project %s deleted successfully\n", id)
		return nil
	},
}

var projectsDiscoverCmd = &cobra.Command{
	Use:   "discover",
	Short: "Discover available repositories",
	Long:  "Discover available repositories from the configured git root",
	RunE: func(cmd *cobra.Command, args []string) error {
		client := apiclient.NewClient(apiURL)
		
		var repos []map[string]interface{}
		if err := client.Get("/api/projects/discover", &repos); err != nil {
			return fmt.Errorf("failed to discover repositories: %w", err)
		}
		
		result, _ := json.MarshalIndent(repos, "", "  ")
		fmt.Println(string(result))
		return nil
	},
}

func init() {
	projectsCmd.AddCommand(projectsCreateCmd)
	projectsCmd.AddCommand(projectsListCmd)
	projectsCmd.AddCommand(projectsGetCmd)
	projectsCmd.AddCommand(projectsUpdateCmd)
	projectsCmd.AddCommand(projectsDeleteCmd)
	projectsCmd.AddCommand(projectsDiscoverCmd)
	
	rootCmd.AddCommand(projectsCmd)
}
