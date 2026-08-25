package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strconv"

	"github.com/spf13/cobra"
	"github.com/tonis2/foundry/internal/apiclient"
)

var plansCmd = &cobra.Command{Use: "plans", Short: "Manage plans"}

type createPlanInput struct {
	RepositoryIDs []int64           `json:"repository_ids"`
	Title         string            `json:"title"`
	Summary       string            `json:"summary"`
	Content       string            `json:"content"`
	Steps         []json.RawMessage `json:"steps"`
}

type createPlanRequest struct {
	RepositoryIDs []int64 `json:"repository_ids"`
	Title         string  `json:"title"`
	Summary       string  `json:"summary"`
	Content       string  `json:"content"`
}

type planOutput struct {
	*apiclient.Plan
	Steps []apiclient.PlanStep `json:"steps"`
}

func decodeJSON(r io.Reader, v any) error {
	dec := json.NewDecoder(r)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return err
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return fmt.Errorf("multiple JSON values")
		}
		return err
	}
	return nil
}

func parseCreateSteps(raw []json.RawMessage) ([]apiclient.CreateStepInput, error) {
	steps := make([]apiclient.CreateStepInput, len(raw))
	for position, value := range raw {
		var text string
		if err := json.Unmarshal(value, &text); err == nil {
			steps[position] = apiclient.CreateStepInput{Position: position, Text: text}
			continue
		}
		var object struct {
			Text          *string `json:"text"`
			ParallelGroup *int    `json:"parallel_group"`
		}
		if err := decodeJSON(bytes.NewReader(value), &object); err != nil {
			return nil, fmt.Errorf("step at position %d has invalid format: %w", position, err)
		}
		if object.Text == nil {
			return nil, fmt.Errorf("step at position %d is missing 'text' field", position)
		}
		steps[position] = apiclient.CreateStepInput{Position: position, Text: *object.Text, ParallelGroup: object.ParallelGroup}
	}
	return steps, nil
}

func writeJSON(w io.Writer, value any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(value)
}

func planID(value string) (string, error) {
	id, err := strconv.ParseInt(value, 10, 64)
	if err != nil || id <= 0 {
		return "", fmt.Errorf("invalid plan ID %q: must be a positive integer", value)
	}
	return strconv.FormatInt(id, 10), nil
}

var createCmd = &cobra.Command{
	Use: "create", Short: "Create a new plan with steps",
	Long: "Create a new plan. Reads JSON from stdin with repository_ids, title, summary, content, and optional steps array.",
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		var input createPlanInput
		if err := decodeJSON(cmd.InOrStdin(), &input); err != nil {
			return fmt.Errorf("failed to parse JSON: %w", err)
		}
		if len(input.RepositoryIDs) == 0 {
			return fmt.Errorf("repository_ids is required and must contain at least one repository ID")
		}
		seenRepositories := make(map[int64]struct{}, len(input.RepositoryIDs))
		for _, id := range input.RepositoryIDs {
			if id <= 0 {
				return fmt.Errorf("repository_ids must contain positive integers")
			}
			if _, exists := seenRepositories[id]; exists {
				return fmt.Errorf("repository_ids must not contain duplicates")
			}
			seenRepositories[id] = struct{}{}
		}
		steps, err := parseCreateSteps(input.Steps)
		if err != nil {
			return err
		}
		client := apiclient.NewClient(apiURL)
		request := createPlanRequest{input.RepositoryIDs, input.Title, input.Summary, input.Content}
		var plan apiclient.Plan
		if err := client.Post("/api/plans", request, &plan); err != nil {
			return fmt.Errorf("failed to create plan: %w", err)
		}
		for _, request := range steps {
			var step apiclient.PlanStep
			if err := client.Post(fmt.Sprintf("/api/plans/%d/steps", plan.ID), request, &step); err != nil {
				return fmt.Errorf("failed to create step at position %d: %w", request.Position, err)
			}
		}
		var complete apiclient.Plan
		if err := client.Get(fmt.Sprintf("/api/plans/%d", plan.ID), &complete); err != nil {
			return fmt.Errorf("failed to fetch plan: %w", err)
		}
		var completeSteps []apiclient.PlanStep
		if err := client.Get(fmt.Sprintf("/api/plans/%d/steps", plan.ID), &completeSteps); err != nil {
			return fmt.Errorf("failed to fetch plan steps: %w", err)
		}
		return writeJSON(cmd.OutOrStdout(), planOutput{Plan: &complete, Steps: completeSteps})
	},
}

var getCmd = &cobra.Command{
	Use: "get <id>", Short: "Get a plan by ID", Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		id, err := planID(args[0])
		if err != nil {
			return err
		}
		client := apiclient.NewClient(apiURL)
		var plan apiclient.Plan
		if err := client.Get("/api/plans/"+id, &plan); err != nil {
			return fmt.Errorf("failed to get plan: %w", err)
		}
		var steps []apiclient.PlanStep
		if err := client.Get("/api/plans/"+id+"/steps", &steps); err != nil {
			return fmt.Errorf("failed to get plan steps: %w", err)
		}
		return writeJSON(cmd.OutOrStdout(), planOutput{Plan: &plan, Steps: steps})
	},
}

var listCmd = &cobra.Command{
	Use: "list", Short: "List all plans", Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		var plans []apiclient.Plan
		if err := apiclient.NewClient(apiURL).Get("/api/plans", &plans); err != nil {
			return fmt.Errorf("failed to list plans: %w", err)
		}
		return writeJSON(cmd.OutOrStdout(), plans)
	},
}

type updatePlanInput struct {
	Status        *string  `json:"status,omitempty"`
	Title         *string  `json:"title,omitempty"`
	Summary       *string  `json:"summary,omitempty"`
	Content       *string  `json:"content,omitempty"`
	RepositoryIDs *[]int64 `json:"repository_ids,omitempty"`
}

var updateCmd = &cobra.Command{
	Use: "update <id>", Short: "Update a plan",
	Long: "Update a plan. Reads JSON from stdin with fields to update (status, title, summary, content, repository_ids).",
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		id, err := planID(args[0])
		if err != nil {
			return err
		}
		var input updatePlanInput
		if err := decodeJSON(cmd.InOrStdin(), &input); err != nil {
			return fmt.Errorf("failed to parse JSON: %w", err)
		}
		if input.RepositoryIDs != nil {
			if len(*input.RepositoryIDs) == 0 {
				return fmt.Errorf("repository_ids must contain at least one repository ID")
			}
			seenRepositories := make(map[int64]struct{}, len(*input.RepositoryIDs))
			for _, repositoryID := range *input.RepositoryIDs {
				if repositoryID <= 0 {
					return fmt.Errorf("repository_ids must contain positive integers")
				}
				if _, exists := seenRepositories[repositoryID]; exists {
					return fmt.Errorf("repository_ids must not contain duplicates")
				}
				seenRepositories[repositoryID] = struct{}{}
			}
		}
		var plan apiclient.Plan
		if err := apiclient.NewClient(apiURL).Patch("/api/plans/"+id, input, &plan); err != nil {
			return fmt.Errorf("failed to update plan: %w", err)
		}
		return writeJSON(cmd.OutOrStdout(), plan)
	},
}

func rawInteger(raw json.RawMessage, name string, positive bool) (int64, error) {
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return 0, fmt.Errorf("missing '%s' field", name)
	}
	var number json.Number
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&number); err != nil {
		var text string
		if stringErr := json.Unmarshal(raw, &text); stringErr != nil {
			return 0, fmt.Errorf("%s must be an integer", name)
		}
		number = json.Number(text)
	}
	value, err := strconv.ParseInt(number.String(), 10, 64)
	if err != nil || (positive && value <= 0) || (!positive && value < 0) {
		qualifier := "a zero-based integer"
		if positive {
			qualifier = "a positive integer"
		}
		return 0, fmt.Errorf("%s must be %s", name, qualifier)
	}
	return value, nil
}

var updateStepCmd = &cobra.Command{
	Use: "update-step", Short: "Update a plan step",
	Long: "Update a plan step. Reads JSON from stdin with plan_id, exactly one of step_id or position, and fields to update.",
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		var input map[string]json.RawMessage
		if err := decodeJSON(cmd.InOrStdin(), &input); err != nil {
			return fmt.Errorf("failed to parse JSON: %w", err)
		}
		allowed := map[string]bool{"plan_id": true, "step_id": true, "position": true, "status": true, "text": true, "parallel_group": true}
		for key := range input {
			if !allowed[key] {
				return fmt.Errorf("unknown field %q", key)
			}
		}
		pid, err := rawInteger(input["plan_id"], "plan_id", true)
		if err != nil {
			return err
		}
		stepRaw, hasStep := input["step_id"]
		positionRaw, hasPosition := input["position"]
		if hasStep == hasPosition {
			return fmt.Errorf("exactly one of 'step_id' or 'position' is required")
		}
		client := apiclient.NewClient(apiURL)
		var stepID int64
		if hasStep {
			stepID, err = rawInteger(stepRaw, "step_id", true)
			if err != nil {
				return err
			}
		} else {
			position, parseErr := rawInteger(positionRaw, "position", false)
			if parseErr != nil {
				return parseErr
			}
			var steps []apiclient.PlanStep
			if err := client.Get(fmt.Sprintf("/api/plans/%d/steps", pid), &steps); err != nil {
				return fmt.Errorf("list plan steps: %w", err)
			}
			for _, step := range steps {
				if int64(step.Position) == position {
					stepID = step.ID
					break
				}
			}
			if stepID == 0 {
				return fmt.Errorf("no step at zero-based position %d", position)
			}
		}
		request := make(map[string]any)
		for _, key := range []string{"status", "text"} {
			if raw, ok := input[key]; ok {
				var value string
				if err := json.Unmarshal(raw, &value); err != nil {
					return fmt.Errorf("%s must be a string", key)
				}
				request[key] = value
			}
		}
		if raw, ok := input["parallel_group"]; ok {
			value, err := rawInteger(raw, "parallel_group", false)
			if err != nil {
				return err
			}
			request["parallel_group"] = value
		}
		if len(request) == 0 {
			return fmt.Errorf("at least one step field must be provided")
		}
		var step apiclient.PlanStep
		if err := client.Patch(fmt.Sprintf("/api/plans/%d/steps/%d", pid, stepID), request, &step); err != nil {
			return fmt.Errorf("failed to update plan step: %w", err)
		}
		return writeJSON(cmd.OutOrStdout(), step)
	},
}

var runCmd = &cobra.Command{
	Use: "run <id>", Short: "Run a plan as a Foundry workflow", Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		id, err := planID(args[0])
		if err != nil {
			return err
		}
		var workflow map[string]any
		if err := apiclient.NewClient(apiURL).Post("/api/plans/"+id+"/run", struct{}{}, &workflow); err != nil {
			return fmt.Errorf("failed to run plan: %w", err)
		}
		return writeJSON(cmd.OutOrStdout(), workflow)
	},
}

var reviewCmd = &cobra.Command{
	Use: "review <id>", Short: "Run a new Steward review for a plan", Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		id, err := planID(args[0])
		if err != nil {
			return err
		}
		var rev apiclient.PlanReview
		if err := apiclient.NewClient(apiURL).Post("/api/plans/"+id+"/reviews", struct{}{}, &rev); err != nil {
			return fmt.Errorf("failed to run plan review: %w", err)
		}
		return writeJSON(cmd.OutOrStdout(), rev)
	},
}

var reviewsCmd = &cobra.Command{
	Use: "reviews <id>", Short: "List Steward reviews for a plan, most recent first", Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		id, err := planID(args[0])
		if err != nil {
			return err
		}
		var reviews []apiclient.PlanReview
		if err := apiclient.NewClient(apiURL).Get("/api/plans/"+id+"/reviews", &reviews); err != nil {
			return fmt.Errorf("failed to list plan reviews: %w", err)
		}
		return writeJSON(cmd.OutOrStdout(), reviews)
	},
}

// planReviewCheck is foundry plans check's output: either the most
// recent Steward review of the plan, or an explicit no_review status
// when none exists yet, so a caller never has to infer absence from an
// empty list.
type planReviewCheck struct {
	PlanID int64                 `json:"plan_id"`
	Status string                `json:"status"`
	Review *apiclient.PlanReview `json:"review,omitempty"`
}

var checkCmd = &cobra.Command{
	Use: "check <id>", Short: "Check the latest Steward review status for a plan", Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		id, err := planID(args[0])
		if err != nil {
			return err
		}
		pid, err := strconv.ParseInt(id, 10, 64)
		if err != nil {
			return err
		}
		var reviews []apiclient.PlanReview
		if err := apiclient.NewClient(apiURL).Get("/api/plans/"+id+"/reviews", &reviews); err != nil {
			return fmt.Errorf("failed to check plan review: %w", err)
		}
		if len(reviews) == 0 {
			return writeJSON(cmd.OutOrStdout(), planReviewCheck{PlanID: pid, Status: "no_review"})
		}
		latest := reviews[0]
		return writeJSON(cmd.OutOrStdout(), planReviewCheck{PlanID: pid, Status: latest.Status, Review: &latest})
	},
}

func init() {
	plansCmd.AddCommand(createCmd, runCmd, getCmd, listCmd, updateCmd, updateStepCmd, checkCmd, reviewCmd, reviewsCmd)
}
