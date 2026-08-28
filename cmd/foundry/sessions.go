package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/tonis2/foundry/internal/apiclient"
)

// piSelfSessionPrefix mirrors the "pi:" prefix Foundry's own telemetry
// ingest already uses to identify a Pi orchestrator session by its
// source_session_id (see cerberusPiParentSessionPrefix in
// internal/httpserver/cerberus_events.go). Reusing it here, rather than
// inventing a second convention, keeps --self's resolved identifier
// consistent with how Pi sessions are already named elsewhere.
const piSelfSessionPrefix = "pi:"

var sessionsCmd = &cobra.Command{Use: "sessions", Short: "Manage agent sessions"}

type attachSessionRequest struct {
	Session    string `json:"session"`
	PlanStepID *int64 `json:"plan_step_id,omitempty"`
}

var attachSessionPlanID int64
var attachSessionStepID int64
var attachSessionSelf bool

var attachCmd = &cobra.Command{
	Use:   "attach [session]",
	Short: "Explicitly attach an externally-launched agent session to a plan (and optionally a plan step)",
	Long: "Attach an agent session (identified by its display name, i.e. agent_sessions.session) to a " +
		"plan that Foundry itself never launched the session for, so there is no session-start event to " +
		"derive attribution from. Records the link with method 'explicit'. Requires exactly one of the " +
		"<session> argument or --self: pass <session> explicitly, or pass --self to resolve it from the " +
		"current Pi session (PI_SESSION_ID) as \"pi:<PI_SESSION_ID>\", the same convention Foundry's own " +
		"telemetry already uses for Pi orchestrator sessions. --self only works inside a Pi session.",
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		var session string
		switch {
		case attachSessionSelf && len(args) == 1:
			return fmt.Errorf("exactly one of <session> or --self is required, not both")
		case attachSessionSelf:
			piSessionID := os.Getenv("PI_SESSION_ID")
			if piSessionID == "" {
				return fmt.Errorf("--self only works inside a Pi session (PI_SESSION_ID is unset or empty); " +
					"pass the session name explicitly instead")
			}
			session = piSelfSessionPrefix + piSessionID
		case len(args) == 1:
			session = args[0]
		default:
			return fmt.Errorf("exactly one of <session> or --self is required")
		}
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
	attachCmd.Flags().BoolVar(&attachSessionSelf, "self", false, "resolve the session from PI_SESSION_ID (\"pi:<PI_SESSION_ID>\") instead of a positional argument")
	sessionsCmd.AddCommand(attachCmd)
}
