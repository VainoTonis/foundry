package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"

	"github.com/spf13/cobra"
	"github.com/tonis2/foundry/internal/apiclient"
)

var plansCmd = &cobra.Command{
	Use:   "plans",
	Short: "Manage plans",
}

var createCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a new plan with steps",
	Long:  "Create a new plan. Reads JSON from stdin with project_id, title, summary, content, and optional steps array.",
	RunE: func(cmd *cobra.Command, args []string) error {
		client := apiclient.NewClient(apiURL)

		// Read JSON from stdin
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			return fmt.Errorf("failed to read stdin: %w", err)
		}

		// Parse the input JSON - steps can be strings or objects
		var input struct {
			ProjectID int64         `json:"project_id"`
			Title     string        `json:"title"`
			Summary   string        `json:"summary"`
			Content   string        `json:"content"`
			Steps     []interface{} `json:"steps"`
		}

		if err := json.Unmarshal(data, &input); err != nil {
			return fmt.Errorf("failed to parse JSON: %w", err)
		}

		// Create the plan first
		planReq := struct {
			ProjectID int64  `json:"project_id"`
			Title     string `json:"title"`
			Summary   string `json:"summary"`
			Content   string `json:"content"`
		}{
			ProjectID: input.ProjectID,
			Title:     input.Title,
			Summary:   input.Summary,
			Content:   input.Content,
		}

		var plan apiclient.Plan
		if err := client.Post("/api/plans", planReq, &plan); err != nil {
			return fmt.Errorf("failed to create plan: %w", err)
		}

		// Add steps if provided
		for position, stepVal := range input.Steps {
			var stepText string
			var parallelGroup *int

			// Handle both string and object formats
			switch v := stepVal.(type) {
			case string:
				stepText = v
			case map[string]interface{}:
				// Try to get text field
				if textVal, ok := v["text"]; ok {
					stepText = fmt.Sprintf("%v", textVal)
				} else {
					return fmt.Errorf("step at position %d is missing 'text' field", position)
				}
				// Try to get parallel_group field
				if pgVal, ok := v["parallel_group"]; ok && pgVal != nil {
					switch pg := pgVal.(type) {
					case float64:
						pgInt := int(pg)
						parallelGroup = &pgInt
					case int:
						parallelGroup = &pg
					}
				}
			default:
				return fmt.Errorf("step at position %d has invalid format", position)
			}

			stepReq := apiclient.CreateStepInput{
				Position:      position,
				Text:          stepText,
				ParallelGroup: parallelGroup,
			}

			var step apiclient.PlanStep
			if err := client.Post(fmt.Sprintf("/api/plans/%d/steps", plan.ID), stepReq, &step); err != nil {
				return fmt.Errorf("failed to create step at position %d: %w", position, err)
			}
		}

		// Fetch and return the complete plan with its steps
		var completePlan apiclient.Plan
		if err := client.Get(fmt.Sprintf("/api/plans/%d", plan.ID), &completePlan); err != nil {
			return fmt.Errorf("failed to fetch plan: %w", err)
		}

		var steps []apiclient.PlanStep
		if err := client.Get(fmt.Sprintf("/api/plans/%d/steps", plan.ID), &steps); err != nil {
			return fmt.Errorf("failed to fetch plan steps: %w", err)
		}

		// Output the plan and steps
		output := struct {
			*apiclient.Plan
			Steps []apiclient.PlanStep `json:"steps"`
		}{
			Plan:  &completePlan,
			Steps: steps,
		}

		outputJSON, err := json.MarshalIndent(output, "", "  ")
		if err != nil {
			return fmt.Errorf("failed to marshal output: %w", err)
		}

		fmt.Println(string(outputJSON))
		return nil
	},
}

var getCmd = &cobra.Command{
	Use:   "get <id>",
	Short: "Get a plan by ID",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		client := apiclient.NewClient(apiURL)
		id := args[0]

		var plan apiclient.Plan
		if err := client.Get(fmt.Sprintf("/api/plans/%s", id), &plan); err != nil {
			return fmt.Errorf("failed to get plan: %w", err)
		}

		var steps []apiclient.PlanStep
		if err := client.Get(fmt.Sprintf("/api/plans/%s/steps", id), &steps); err != nil {
			return fmt.Errorf("failed to get plan steps: %w", err)
		}

		output := struct {
			*apiclient.Plan
			Steps []apiclient.PlanStep `json:"steps"`
		}{
			Plan:  &plan,
			Steps: steps,
		}

		outputJSON, err := json.MarshalIndent(output, "", "  ")
		if err != nil {
			return fmt.Errorf("failed to marshal output: %w", err)
		}

		fmt.Println(string(outputJSON))
		return nil
	},
}

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List all plans",
	RunE: func(cmd *cobra.Command, args []string) error {
		client := apiclient.NewClient(apiURL)

		var plans []apiclient.Plan
		if err := client.Get("/api/plans", &plans); err != nil {
			return fmt.Errorf("failed to list plans: %w", err)
		}

		outputJSON, err := json.MarshalIndent(plans, "", "  ")
		if err != nil {
			return fmt.Errorf("failed to marshal output: %w", err)
		}

		fmt.Println(string(outputJSON))
		return nil
	},
}

var updateCmd = &cobra.Command{
	Use:   "update <id>",
	Short: "Update a plan",
	Long:  "Update a plan. Reads JSON from stdin with fields to update (status, project_id, title, summary, content).",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		client := apiclient.NewClient(apiURL)
		id := args[0]

		// Read JSON from stdin
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			return fmt.Errorf("failed to read stdin: %w", err)
		}

		var updateData map[string]interface{}
		if err := json.Unmarshal(data, &updateData); err != nil {
			return fmt.Errorf("failed to parse JSON: %w", err)
		}

		// Build update request with optional fields
		updateReq := struct {
			Status    *string `json:"status,omitempty"`
			ProjectID *int64  `json:"project_id,omitempty"`
			Title     *string `json:"title,omitempty"`
			Summary   *string `json:"summary,omitempty"`
			Content   *string `json:"content,omitempty"`
		}{}

		if status, ok := updateData["status"]; ok && status != nil {
			s := fmt.Sprintf("%v", status)
			updateReq.Status = &s
		}
		if projectID, ok := updateData["project_id"].(float64); ok {
			id := int64(projectID)
			updateReq.ProjectID = &id
		}
		if title, ok := updateData["title"]; ok && title != nil {
			t := fmt.Sprintf("%v", title)
			updateReq.Title = &t
		}
		if summary, ok := updateData["summary"]; ok && summary != nil {
			s := fmt.Sprintf("%v", summary)
			updateReq.Summary = &s
		}
		if content, ok := updateData["content"]; ok && content != nil {
			c := fmt.Sprintf("%v", content)
			updateReq.Content = &c
		}

		var plan apiclient.Plan
		if err := client.Patch(fmt.Sprintf("/api/plans/%s", id), updateReq, &plan); err != nil {
			return fmt.Errorf("failed to update plan: %w", err)
		}

		outputJSON, err := json.MarshalIndent(plan, "", "  ")
		if err != nil {
			return fmt.Errorf("failed to marshal output: %w", err)
		}

		fmt.Println(string(outputJSON))
		return nil
	},
}

var updateStepCmd = &cobra.Command{
	Use:   "update-step",
	Short: "Update a plan step",
	Long:  "Update a plan step. Reads JSON from stdin with plan_id, step_id-or-position, and fields to update.",
	RunE: func(cmd *cobra.Command, args []string) error {
		client := apiclient.NewClient(apiURL)

		// Read JSON from stdin
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			return fmt.Errorf("failed to read stdin: %w", err)
		}

		var input map[string]interface{}
		if err := json.Unmarshal(data, &input); err != nil {
			return fmt.Errorf("failed to parse JSON: %w", err)
		}

		// Extract plan_id and step_id
		planIDVal, ok := input["plan_id"]
		if !ok {
			return fmt.Errorf("missing 'plan_id' field")
		}
		planID := fmt.Sprintf("%v", planIDVal)

		// Handle step_id or position
		stepIDVal, hasStepID := input["step_id"]
		positionVal, hasPosition := input["position"]

		if !hasStepID && !hasPosition {
			return fmt.Errorf("missing 'step_id' or 'position' field")
		}

		var stepID string
		if hasStepID {
			stepID = fmt.Sprintf("%v", stepIDVal)
		} else {
			// Parse position as an integer for step lookup
			positionStr := fmt.Sprintf("%v", positionVal)
			if _, err := strconv.Atoi(positionStr); err != nil {
				return fmt.Errorf("invalid position value: %v (must be an integer)", positionVal)
			}
			stepID = positionStr
		}

		// Build update request with generic field forwarding
		updateReq := make(map[string]interface{})

		// Handle all possible update fields
		for key, value := range input {
			switch key {
			case "plan_id", "step_id", "position":
				// Skip identifiers
				continue
			case "status":
				if value != nil {
					updateReq["status"] = fmt.Sprintf("%v", value)
				}
			case "text":
				if value != nil {
					updateReq["text"] = fmt.Sprintf("%v", value)
				}
			case "parallel_group":
				if value != nil {
					switch v := value.(type) {
					case float64:
						updateReq["parallel_group"] = int(v)
					case int:
						updateReq["parallel_group"] = v
					case nil:
						updateReq["parallel_group"] = nil
					default:
						updateReq["parallel_group"] = fmt.Sprintf("%v", value)
					}
				}
			default:
				// Generic field forwarding - pass through unknown fields
				updateReq[key] = value
			}
		}

		var step apiclient.PlanStep
		if err := client.Patch(fmt.Sprintf("/api/plans/%s/steps/%s", planID, stepID), updateReq, &step); err != nil {
			return fmt.Errorf("failed to update plan step: %w", err)
		}

		outputJSON, err := json.MarshalIndent(step, "", "  ")
		if err != nil {
			return fmt.Errorf("failed to marshal output: %w", err)
		}

		fmt.Println(string(outputJSON))
		return nil
	},
}

var runCmd = &cobra.Command{
	Use:   "run <id>",
	Short: "Run a plan as a Foundry workflow",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		client := apiclient.NewClient(apiURL)
		var workflow map[string]interface{}
		if err := client.Post(fmt.Sprintf("/api/plans/%s/run", args[0]), struct{}{}, &workflow); err != nil {
			return fmt.Errorf("failed to run plan: %w", err)
		}
		result, _ := json.MarshalIndent(workflow, "", "  ")
		fmt.Println(string(result))
		return nil
	},
}

func init() {
	plansCmd.AddCommand(createCmd)
	plansCmd.AddCommand(runCmd)
	plansCmd.AddCommand(getCmd)
	plansCmd.AddCommand(listCmd)
	plansCmd.AddCommand(updateCmd)
	plansCmd.AddCommand(updateStepCmd)
}
