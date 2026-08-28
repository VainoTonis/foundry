// Package review builds the deterministic, bounded input a Steward
// two-pass plan review reads and is fingerprinted against.
package review

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/tonis2/foundry/internal/cerberus"
	"github.com/tonis2/foundry/internal/db"
)

// MaxFeedbackItems bounds how many open feedback rows a snapshot
// carries, most recent first.
const MaxFeedbackItems = 20

// MaxFeedbackBodyRunes bounds how much of a single feedback body a
// snapshot carries verbatim.
const MaxFeedbackBodyRunes = 500

// ContainerMountRoot is the stable base directory every local plan
// repository is mounted under inside a Steward review session.
const ContainerMountRoot = "/workspace/repositories"

// unavailableRemoteOnly is the disclosure recorded for a plan repository
// with no local working tree to mount.
const unavailableRemoteOnly = "remote-only: no local working tree available to mount read-only"

// openFeedbackStatus is the only feedback lifecycle status a snapshot
// carries; resolved or dismissed feedback has already been acted on.
const openFeedbackStatus = "open"

// containerPath derives a repository's stable container path from its
// plan position and a slug of its name, never from its host path.
func containerPath(position int, name string) string {
	return fmt.Sprintf("%s/%d-%s", ContainerMountRoot, position, slugifyRepositoryName(name))
}

// slugifyRepositoryName lowercases name and collapses every run of
// non-alphanumeric characters to a single '-', falling back to "repo"
// if nothing alphanumeric remains.
func slugifyRepositoryName(name string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(name)) {
		switch {
		case r >= 'a' && r <= 'z' || r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			if b.Len() > 0 && b.String()[b.Len()-1] != '-' {
				b.WriteByte('-')
			}
		}
	}
	slug := strings.TrimRight(b.String(), "-")
	if slug == "" {
		return "repo"
	}
	return slug
}

// RepositorySnapshot is the canonical, host-path-free description of one
// plan repository as disclosed to a review.
type RepositorySnapshot struct {
	Position          int    `json:"position"`
	RepositoryID      int64  `json:"repository_id"`
	Name              string `json:"name"`
	Primary           bool   `json:"primary"`
	Available         bool   `json:"available"`
	ContainerPath     string `json:"container_path,omitempty"`
	UnavailableReason string `json:"unavailable_reason,omitempty"`
}

// StepSnapshot is the canonical view of one plan step.
type StepSnapshot struct {
	Position int    `json:"position"`
	Text     string `json:"text"`
}

// FeedbackSnapshot is the canonical, capped view of one still-open
// feedback item carried into a review.
type FeedbackSnapshot struct {
	ID                int64    `json:"id"`
	Dimension         string   `json:"dimension,omitempty"`
	Target            string   `json:"target,omitempty"`
	Score             *int     `json:"score,omitempty"`
	Body              string   `json:"body,omitempty"`
	Tags              []string `json:"tags,omitempty"`
	Evidence          string   `json:"evidence,omitempty"`
	Impact            string   `json:"impact,omitempty"`
	RecommendedAction string   `json:"recommended_action,omitempty"`
	Owner             string   `json:"owner,omitempty"`
	// Repositories lists plan positions (RepositorySnapshot.Position)
	// this feedback item is scoped to, deduplicated and ascending.
	Repositories []int `json:"repositories,omitempty"`
}

// SessionAttributionSummary is a minimal-disclosure summary of how many
// agent sessions are currently linked to a plan under review, broken
// down by session_plan_links.method (see migration
// 040_session_plan_links.up.sql for the exact, closed set of methods).
// It deliberately carries only counts -- never session names, ids, or
// any other identifying detail -- and it is NOT part of PlanSnapshot: it
// reflects session attribution activity, not plan content, so it must
// never affect Snapshot.SHA256 or make an otherwise-still-valid review
// look stale. See buildPrompt's session attribution section in
// context.go for how it is rendered.
type SessionAttributionSummary struct {
	Total int `json:"total"`
	// ByMethod is keyed by one of db.SessionPlanLinkMethod* and holds
	// only methods with at least one linked session.
	ByMethod map[string]int `json:"by_method,omitempty"`
}

// PlanSnapshot is the exact, canonical content a review reads and is
// fingerprinted against.
type PlanSnapshot struct {
	PlanID            int64                `json:"plan_id"`
	Title             string               `json:"title"`
	Summary           string               `json:"summary"`
	Content           string               `json:"content"`
	Steps             []StepSnapshot       `json:"steps"`
	Repositories      []RepositorySnapshot `json:"repositories"`
	Feedback          []FeedbackSnapshot   `json:"feedback"`
	FeedbackTruncated bool                 `json:"feedback_truncated"`
}

// Snapshot pairs a built PlanSnapshot with its canonical JSON encoding
// and the SHA-256 fingerprint computed from that exact encoding.
type Snapshot struct {
	Plan   PlanSnapshot
	JSON   json.RawMessage
	SHA256 string
}

// BuildSnapshot canonicalizes plan, its ordered steps, and feedback
// scoped to it into a deterministic Snapshot. steps and feedback may be
// supplied in any order; BuildSnapshot re-sorts both before encoding, so
// caller-side ordering never affects the resulting fingerprint. It fails
// if plan has no repositories.
func BuildSnapshot(plan db.Plan, steps []db.PlanStep, feedback []db.Feedback) (Snapshot, error) {
	if plan.ID == 0 {
		return Snapshot{}, fmt.Errorf("build snapshot: plan id is required")
	}
	if len(plan.Repositories) == 0 {
		return Snapshot{}, fmt.Errorf("build snapshot: plan %d has no repositories to review", plan.ID)
	}

	repoPositionByID := make(map[int64]int, len(plan.Repositories))
	for _, pr := range plan.Repositories {
		repoPositionByID[pr.RepositoryID] = pr.Position
	}

	feedbackSnapshots, truncated := buildFeedbackSnapshots(feedback, repoPositionByID)

	ps := PlanSnapshot{
		PlanID:            plan.ID,
		Title:             strings.TrimSpace(plan.Title),
		Summary:           strings.TrimSpace(plan.Summary),
		Content:           strings.TrimSpace(plan.Content),
		Steps:             buildStepSnapshots(steps),
		Repositories:      buildRepositorySnapshots(plan.Repositories),
		Feedback:          feedbackSnapshots,
		FeedbackTruncated: truncated,
	}

	encoded, err := json.Marshal(ps)
	if err != nil {
		return Snapshot{}, fmt.Errorf("build snapshot: %w", err)
	}

	return Snapshot{Plan: ps, JSON: encoded, SHA256: sha256Hex(encoded)}, nil
}

// BuildMountManifest returns the read-only bind mounts Steward needs so
// every locally available repository in plan appears at the container
// path recorded for it in BuildSnapshot, in stable plan-position order.
// Remote-only repositories are omitted.
func BuildMountManifest(plan db.Plan) []cerberus.Mount {
	mounts := make([]cerberus.Mount, 0, len(plan.Repositories))
	for _, pr := range plan.Repositories {
		local := pr.Repository.LocalPath
		if local == nil || strings.TrimSpace(*local) == "" {
			continue
		}
		mounts = append(mounts, cerberus.Mount{
			Host:      *local,
			Container: containerPath(pr.Position, pr.Repository.Name),
			ReadOnly:  true,
		})
	}
	return mounts
}

func buildStepSnapshots(steps []db.PlanStep) []StepSnapshot {
	sorted := make([]db.PlanStep, len(steps))
	copy(sorted, steps)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Position < sorted[j].Position })

	out := make([]StepSnapshot, 0, len(sorted))
	for _, s := range sorted {
		out = append(out, StepSnapshot{Position: s.Position, Text: strings.TrimSpace(s.Text)})
	}
	return out
}

func buildRepositorySnapshots(repos []db.PlanRepository) []RepositorySnapshot {
	sorted := make([]db.PlanRepository, len(repos))
	copy(sorted, repos)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Position < sorted[j].Position })

	out := make([]RepositorySnapshot, 0, len(sorted))
	for _, pr := range sorted {
		rs := RepositorySnapshot{
			Position:     pr.Position,
			RepositoryID: pr.RepositoryID,
			Name:         pr.Repository.Name,
			Primary:      pr.Position == 0,
		}
		if pr.Repository.LocalPath != nil && strings.TrimSpace(*pr.Repository.LocalPath) != "" {
			rs.Available = true
			rs.ContainerPath = containerPath(pr.Position, pr.Repository.Name)
		} else {
			rs.Available = false
			rs.UnavailableReason = unavailableRemoteOnly
		}
		out = append(out, rs)
	}
	return out
}

// buildFeedbackSnapshots keeps only open feedback, orders it most-recent
// first (CreatedAt then ID), caps it at MaxFeedbackItems, and truncates
// each body to MaxFeedbackBodyRunes.
func buildFeedbackSnapshots(feedback []db.Feedback, repoPositionByID map[int64]int) ([]FeedbackSnapshot, bool) {
	open := make([]db.Feedback, 0, len(feedback))
	for _, f := range feedback {
		if f.Status == openFeedbackStatus {
			open = append(open, f)
		}
	}

	sort.SliceStable(open, func(i, j int) bool {
		if !open[i].CreatedAt.Equal(open[j].CreatedAt) {
			return open[i].CreatedAt.After(open[j].CreatedAt)
		}
		return open[i].ID > open[j].ID
	})

	truncated := len(open) > MaxFeedbackItems
	if truncated {
		open = open[:MaxFeedbackItems]
	}

	out := make([]FeedbackSnapshot, 0, len(open))
	for _, f := range open {
		fs := FeedbackSnapshot{
			ID:   f.ID,
			Body: capRunes(strings.TrimSpace(f.Body), MaxFeedbackBodyRunes),
		}
		if f.Dimension != nil {
			fs.Dimension = *f.Dimension
		}
		if f.Target != nil {
			fs.Target = *f.Target
		}
		if f.Score != nil {
			score := *f.Score
			fs.Score = &score
		}
		if len(f.Tags) > 0 {
			tags := make([]string, len(f.Tags))
			copy(tags, f.Tags)
			sort.Strings(tags)
			fs.Tags = tags
		}
		if f.Evidence != nil {
			fs.Evidence = *f.Evidence
		}
		if f.Impact != nil {
			fs.Impact = *f.Impact
		}
		if f.RecommendedAction != nil {
			fs.RecommendedAction = *f.RecommendedAction
		}
		if f.Owner != nil {
			fs.Owner = *f.Owner
		}
		if len(f.Repositories) > 0 {
			seen := make(map[int]bool, len(f.Repositories))
			positions := make([]int, 0, len(f.Repositories))
			for _, fr := range f.Repositories {
				pos, ok := repoPositionByID[fr.RepositoryID]
				if !ok || seen[pos] {
					continue
				}
				seen[pos] = true
				positions = append(positions, pos)
			}
			sort.Ints(positions)
			fs.Repositories = positions
		}
		out = append(out, fs)
	}
	return out, truncated
}

// capRunes truncates s to at most max runes, appending a stable
// "...(truncated)" marker when it does.
func capRunes(s string, max int) string {
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max]) + "...(truncated)"
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
