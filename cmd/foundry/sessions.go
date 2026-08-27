package main

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/tonis2/foundry/internal/apiclient"
)

var sessionsCmd = &cobra.Command{Use: "sessions", Short: "Manage agent sessions"}

type attachSessionRequest struct {
	Session    string `json:"session"`
	PlanStepID *int64 `json:"plan_step_id,omitempty"`
}

var attachSessionPlanID int64
var attachSessionStepID int64

var attachCmd = &cobra.Command{
	Use:   "attach <session>",
	Short: "Explicitly attach an externally-launched agent session to a plan (and optionally a plan step)",
	Long: "Attach an agent session (identified by its display name, i.e. agent_sessions.session) to a " +
		"plan that Foundry itself never launched the session for, so there is no session-start event to " +
		"derive attribution from. Records the link with method 'explicit'.",
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		session := args[0]
		if session == "" {
			return fmt.Errorf("session is required")
		}
		if attachSessionPlanID <= 0 {
			return fmt.Errorf("--plan-id is required and must be a positive integer")
		}
		request := attachSessionRequest{Session: session}
		if attachSessionStepID > 0 {
			request.PlanStepID = &attachSessionStepID
		}
		client := apiclient.NewClient(apiURL)
		var link apiclient.SessionPlanLink
		if err := client.Post(fmt.Sprintf("/api/plans/%d/sessions", attachSessionPlanID), request, &link); err != nil {
			return fmt.Errorf("failed to attach session to plan: %w", err)
		}
		return writeJSON(cmd.OutOrStdout(), link)
	},
}

func init() {
	attachCmd.Flags().Int64Var(&attachSessionPlanID, "plan-id", 0, "plan ID to attach the session to (required)")
	attachCmd.Flags().Int64Var(&attachSessionStepID, "plan-step-id", 0, "optional plan step ID to attach the session to")
	sessionsCmd.AddCommand(attachCmd)
}
