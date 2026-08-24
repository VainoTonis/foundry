package db

import (
	"encoding/json"
	"time"

	"github.com/tonis2/foundry/internal/repository"
)

// Spec is owned by a Repository (identified by RepositoryID). The physical
// SQL column backing this ownership is repository_id; RepositoryID is
// the externally-facing name for that same column.
type Spec struct {
	ID           int64     `json:"id"`
	Title        string    `json:"title"`
	Content      string    `json:"content"`
	Track        string    `json:"track"`
	Status       string    `json:"status"`
	RepositoryID int64     `json:"repository_id"`
	Tags         []byte    `json:"tags"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type Workflow struct {
	ID         int64      `json:"id"`
	SpecID     int64      `json:"spec_id"`
	Track      string     `json:"track"`
	Status     string     `json:"status"`
	MaxCostUSD *float64   `json:"max_cost_usd"`
	CreatedAt  time.Time  `json:"created_at"`
	FinishedAt *time.Time `json:"finished_at"`
}

type Phase struct {
	ID                int64      `json:"id"`
	WorkflowID        int64      `json:"workflow_id"`
	Position          int        `json:"position"`
	ParallelGroup     *int       `json:"parallel_group,omitempty"`
	Name              string     `json:"name"`
	Goal              string     `json:"goal"`
	PromptSent        *string    `json:"prompt_sent"`
	Status            string     `json:"status"`
	RetryCount        int        `json:"retry_count"`
	TimeoutSeconds    int        `json:"timeout_seconds"`
	CerberusSession   *string    `json:"cerberus_session"`
	CerberusCommit    *string    `json:"cerberus_commit"`
	CostUSD           *float64   `json:"cost_usd"`
	StartedAt         *time.Time `json:"started_at"`
	FinishedAt        *time.Time `json:"finished_at"`
	ReviewVerdict     *string    `json:"review_verdict"`
	ReviewNotes       *string    `json:"review_notes"`
	AdjustedPrompt    *string    `json:"adjusted_prompt"`
	DecisionSummary   *string    `json:"decision_summary"`
	DecisionRationale *string    `json:"decision_rationale"`
	FilesTouched      []byte     `json:"files_touched"`
	PhaseFeedback     []byte     `json:"phase_feedback"`
}

type PhaseLog struct {
	ID      int64     `json:"id"`
	PhaseID int64     `json:"phase_id"`
	Line    string    `json:"line"`
	Ts      time.Time `json:"ts"`
}

type KnownCerberusSession struct {
	Session             string     `json:"session"`
	Type                string     `json:"type"`
	FoundryStatus       string     `json:"foundry_status"`
	RepositoryID        *int64     `json:"repository_id,omitempty"`
	RepositoryName      string     `json:"repository_name"`
	RepositoryLocalPath string     `json:"repository_local_path"`
	SpecID              *int64     `json:"spec_id,omitempty"`
	SpecTitle           string     `json:"spec_title"`
	WorkflowID          *int64     `json:"workflow_id,omitempty"`
	PhaseID             *int64     `json:"phase_id,omitempty"`
	PhaseName           string     `json:"phase_name"`
	DraftID             *int64     `json:"draft_id,omitempty"`
	DraftTitle          string     `json:"draft_title"`
	LastUpdatedAt       time.Time  `json:"last_updated_at"`
	FinishedAt          *time.Time `json:"finished_at,omitempty"`
	SafeToClean         bool       `json:"safe_to_clean"`
	UnsafeReason        string     `json:"unsafe_reason,omitempty"`
}

type AppSetting struct {
	Key       string    `json:"key"`
	Value     string    `json:"value"`
	UpdatedAt time.Time `json:"updated_at"`
}

// SpecDraft is owned by a Repository (identified by RepositoryID), which
// may be unset for drafts not yet attached to one. The physical SQL
// column backing this ownership is repository_id; RepositoryID is the
// externally-facing name for that same column.
type SpecDraft struct {
	ID                    int64           `json:"id"`
	RepositoryID          *int64          `json:"repository_id"`
	Title                 string          `json:"title"`
	CerberusSession       string          `json:"cerberus_session"`
	Messages              json.RawMessage `json:"messages"`
	Status                string          `json:"status"`
	OriginalIntent        string          `json:"original_intent"`
	CurrentDecisionNeeded string          `json:"current_decision_needed"`
	CreatedAt             time.Time       `json:"created_at"`
	UpdatedAt             time.Time       `json:"updated_at"`
}

type DraftAttempt struct {
	ID              int64     `json:"id"`
	DraftID         int64     `json:"draft_id"`
	AttemptNumber   int       `json:"attempt_number"`
	CerberusSession string    `json:"cerberus_session"`
	Status          string    `json:"status"`
	Prompt          string    `json:"prompt"`
	Result          string    `json:"result"`
	ErrorMessage    string    `json:"error_message"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

type DraftAttemptEvent struct {
	ID        int64           `json:"id"`
	DraftID   int64           `json:"draft_id"`
	AttemptID *int64          `json:"attempt_id"`
	EventType string          `json:"event_type"`
	Payload   json.RawMessage `json:"payload"`
	CreatedAt time.Time       `json:"created_at"`
}

type DraftDecision struct {
	ID        int64           `json:"id"`
	DraftID   int64           `json:"draft_id"`
	Prompt    string          `json:"prompt"`
	Options   json.RawMessage `json:"options"`
	Decision  string          `json:"decision"`
	Rationale string          `json:"rationale"`
	Status    string          `json:"status"`
	CreatedAt time.Time       `json:"created_at"`
	UpdatedAt time.Time       `json:"updated_at"`
}

type CerberusEvent struct {
	ID        int64           `json:"id"`
	Session   string          `json:"session"`
	EventType string          `json:"event_type"`
	Payload   json.RawMessage `json:"payload"`
	CreatedAt time.Time       `json:"created_at"`
}

type ExternalCerberusSession struct {
	ID          int64     `json:"id"`
	Session     string    `json:"session"`
	Repo        string    `json:"repo"`
	Status      string    `json:"status"`
	FirstSeenAt time.Time `json:"first_seen_at"`
	LastSeenAt  time.Time `json:"last_seen_at"`
}

type Profile struct {
	ID           int64             `json:"id"`
	Name         string            `json:"name"`
	DefaultModel string            `json:"default_model"`
	DefaultImage string            `json:"default_image"`
	AWSProfile   string            `json:"aws_profile"`
	AWSRegion    string            `json:"aws_region"`
	ExtraEnv     map[string]string `json:"extra_env"`
	CreatedAt    time.Time         `json:"created_at"`
	UpdatedAt    time.Time         `json:"updated_at"`
}

// PlanRepository is one ordered member of a Plan's repository membership
// (the plan_repositories table). Position 0 is the plan's primary
// repository. Repository carries the resolved Repository fields
// (name/locators) needed by callers without a further lookup.
type PlanRepository struct {
	Position     int                   `json:"position"`
	RepositoryID int64                 `json:"repository_id"`
	Repository   repository.Repository `json:"repository"`
}

type Plan struct {
	ID           int64            `json:"id"`
	Repositories []PlanRepository `json:"repositories"`
	Title        string           `json:"title"`
	Summary      string           `json:"summary"`
	Content      string           `json:"content"`
	Status       string           `json:"status"`
	CreatedAt    time.Time        `json:"created_at"`
	UpdatedAt    time.Time        `json:"updated_at"`
}

type PlanStep struct {
	ID            int64     `json:"id"`
	PlanID        int64     `json:"plan_id"`
	Position      int       `json:"position"`
	Text          string    `json:"text"`
	Status        string    `json:"status"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
	ParallelGroup *int      `json:"parallel_group,omitempty"`
}

// FeedbackRepository is one member of a Feedback's repository membership
// (the feedback_repositories table). Unlike PlanRepository this is an
// unordered set: feedback has no notion of a primary repository.
type FeedbackRepository struct {
	RepositoryID int64                 `json:"repository_id"`
	Repository   repository.Repository `json:"repository"`
}

type Feedback struct {
	ID                int64                `json:"id"`
	Body              string               `json:"body,omitempty"`
	Model             string               `json:"model,omitempty"`
	SessionID         string               `json:"session_id,omitempty"`
	Processed         bool                 `json:"processed"`
	Dimension         *string              `json:"dimension,omitempty"`
	Target            *string              `json:"target,omitempty"`
	Score             *int                 `json:"score,omitempty"`
	Tags              []string             `json:"tags,omitempty"`
	Evidence          *string              `json:"evidence,omitempty"`
	Impact            *string              `json:"impact,omitempty"`
	RecommendedAction *string              `json:"recommended_action,omitempty"`
	Owner             *string              `json:"owner,omitempty"`
	Status            string               `json:"status"`
	CreatedAt         time.Time            `json:"created_at"`
	Repositories      []FeedbackRepository `json:"repositories,omitempty"`
	ScopeStatus       string               `json:"scope_status"`
}

type AgentSession struct {
	ID                    int64      `json:"id"`
	Session               string     `json:"session"`
	SourceSessionID       string     `json:"source_session_id"`
	Kind                  string     `json:"kind"`
	Origin                string     `json:"origin"`
	RepositoryID          *int64     `json:"repository_id,omitempty"`
	PhaseID               *int64     `json:"phase_id,omitempty"`
	RepoPath              *string    `json:"repo_path,omitempty"`
	Model                 *string    `json:"model,omitempty"`
	ParentSession         *string    `json:"parent_session,omitempty"`
	SchemaVersion         string     `json:"schema_version"`
	LastEventAt           time.Time  `json:"last_event_at"`
	CloseReason           string     `json:"close_reason"`
	LifecycleState        string     `json:"lifecycle_state"`
	StartEventSeen        bool       `json:"start_event_seen"`
	EndEventSeen          bool       `json:"end_event_seen"`
	ParentSourceSessionID *string    `json:"parent_source_session_id,omitempty"`
	StartedAt             time.Time  `json:"started_at"`
	EndedAt               *time.Time `json:"ended_at,omitempty"`
	InputTokens           int64      `json:"input_tokens"`
	OutputTokens          int64      `json:"output_tokens"`
	CacheReadTokens       int64      `json:"cache_read_tokens"`
	CacheWriteTokens      int64      `json:"cache_write_tokens"`
	CostUSD               float64    `json:"cost_usd"`
	ToolCallCount         int64      `json:"tool_call_count"`
	TurnCount             int64      `json:"turn_count"`
	NextSeq               int64      `json:"next_seq"`
}

type AgentTurn struct {
	ID               int64     `json:"id"`
	AgentSessionID   int64     `json:"agent_session_id"`
	Seq              int64     `json:"seq"`
	TurnIndex        *int64    `json:"turn_index,omitempty"`
	SourceMessageID  *string   `json:"source_message_id,omitempty"`
	InputTokens      int64     `json:"input_tokens"`
	OutputTokens     int64     `json:"output_tokens"`
	CacheReadTokens  int64     `json:"cache_read_tokens"`
	CacheWriteTokens int64     `json:"cache_write_tokens"`
	CostUSD          float64   `json:"cost_usd"`
	Model            string    `json:"model"`
	Provider         string    `json:"provider"`
	ThinkingLevel    string    `json:"thinking_level"`
	StopReason       string    `json:"stop_reason"`
	Ts               time.Time `json:"ts"`
}

type AgentToolCall struct {
	ID                      int64      `json:"id"`
	AgentSessionID          int64      `json:"agent_session_id"`
	Seq                     int64      `json:"seq"`
	ResultSeq               *int64     `json:"result_seq,omitempty"`
	ToolCallID              *string    `json:"tool_call_id,omitempty"`
	ToolName                string     `json:"tool_name"`
	ToolInput               *string    `json:"tool_input,omitempty"`
	ToolInputRedacted       bool       `json:"tool_input_redacted"`
	ToolInputOmitted        bool       `json:"tool_input_omitted"`
	ToolInputTruncated      bool       `json:"tool_input_truncated"`
	ToolInputSHA256         *string    `json:"tool_input_sha256,omitempty"`
	ToolInputOriginalBytes  *int64     `json:"tool_input_original_bytes,omitempty"`
	ToolResult              *string    `json:"tool_result,omitempty"`
	ToolResultRedacted      bool       `json:"tool_result_redacted"`
	ToolResultOmitted       bool       `json:"tool_result_omitted"`
	ToolResultTruncated     bool       `json:"tool_result_truncated"`
	ToolResultSHA256        *string    `json:"tool_result_sha256,omitempty"`
	ToolResultOriginalBytes *int64     `json:"tool_result_original_bytes,omitempty"`
	IsError                 *bool      `json:"is_error,omitempty"`
	DurationMs              *int64     `json:"duration_ms,omitempty"`
	CreatedAt               time.Time  `json:"created_at"`
	FinishedAt              *time.Time `json:"finished_at,omitempty"`
}

type AgentMessage struct {
	ID                   int64     `json:"id"`
	AgentSessionID       int64     `json:"agent_session_id"`
	Seq                  int64     `json:"seq"`
	Role                 string    `json:"role"`
	SourceMessageID      *string   `json:"source_message_id,omitempty"`
	TurnIndex            *int64    `json:"turn_index,omitempty"`
	InputSource          string    `json:"input_source"`
	IsFinal              bool      `json:"is_final"`
	Content              *string   `json:"content,omitempty"`
	ContentRedacted      bool      `json:"content_redacted"`
	ContentTruncated     bool      `json:"content_truncated"`
	ContentSHA256        *string   `json:"content_sha256,omitempty"`
	ContentOriginalBytes *int64    `json:"content_original_bytes,omitempty"`
	CreatedAt            time.Time `json:"created_at"`
}

type TelemetryReceipt struct {
	ID         int64     `json:"id"`
	ProducerID string    `json:"producer_id"`
	EventID    string    `json:"event_id"`
	ClientSeq  int64     `json:"client_seq"`
	EventType  string    `json:"event_type"`
	Session    string    `json:"session"`
	ReceivedAt time.Time `json:"received_at"`
}

type KnowledgeFeedback struct {
	ID         int64     `json:"id"`
	Kind       string    `json:"kind"`
	NotePath   *string   `json:"note_path,omitempty"`
	Topic      *string   `json:"topic,omitempty"`
	Evidence   string    `json:"evidence"`
	Suggestion *string   `json:"suggestion,omitempty"`
	Origin     string    `json:"origin"`
	Status     string    `json:"status"`
	CreatedAt  time.Time `json:"created_at"`
}
