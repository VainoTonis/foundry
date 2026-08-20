package db

import (
	"encoding/json"
	"time"
)

type Project struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	RepoPath  string    `json:"repo_path"`
	CreatedAt time.Time `json:"created_at"`
}

// Spec is owned by a Repository (identified by RepositoryID). The physical
// SQL column backing this ownership remains project_id; RepositoryID is
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
// column backing this ownership remains project_id; RepositoryID is the
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

type Plan struct {
	ID        int64     `json:"id"`
	ProjectID *int64    `json:"project_id,omitempty"`
	RepoName  string    `json:"repo_name"` // retained for legacy plans and display
	Title     string    `json:"title"`
	Summary   string    `json:"summary"`
	Content   string    `json:"content"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
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

type Feedback struct {
	ID                int64     `json:"id"`
	Body              string    `json:"body,omitempty"`
	Model             string    `json:"model,omitempty"`
	SessionID         string    `json:"session_id,omitempty"`
	Processed         bool      `json:"processed"`
	Dimension         *string   `json:"dimension,omitempty"`
	Target            *string   `json:"target,omitempty"`
	Score             *int      `json:"score,omitempty"`
	Tags              []string  `json:"tags,omitempty"`
	Evidence          *string   `json:"evidence,omitempty"`
	Impact            *string   `json:"impact,omitempty"`
	RecommendedAction *string   `json:"recommended_action,omitempty"`
	Owner             *string   `json:"owner,omitempty"`
	Status            string    `json:"status"`
	CreatedAt         time.Time `json:"created_at"`
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
