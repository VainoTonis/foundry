package webui

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/tonis2/foundry/internal/db"
)

// cerberusSessionView augments a KnownCerberusSession (a session with
// lifecycle attribution: a workflow phase, spec draft, or externally
// registered run) with a live Cerberus status lookup.
type cerberusSessionView struct {
	db.KnownCerberusSession
	CerberusStatus string `json:"cerberus_status"`
	CerberusError  string `json:"cerberus_error,omitempty"`
}

// sessionOverviewView is the session-centric merge of lifecycle
// attribution (from cerberusSessionView / KnownCerberusSession) and
// captured agent telemetry (agent_sessions). Sessions that only exist in
// one source still render: a lifecycle-attributed session with no
// telemetry yet shows HasTelemetry=false, and a telemetry-only session
// (an agent_sessions row with no matching phase/draft/external record)
// shows HasAttribution=false with its KnownCerberusSession fields
// synthesized from the agent session alone.
type sessionOverviewView struct {
	db.KnownCerberusSession
	CerberusStatus string `json:"cerberus_status,omitempty"`
	CerberusError  string `json:"cerberus_error,omitempty"`

	HasAttribution bool            `json:"has_attribution"`
	HasTelemetry   bool            `json:"has_telemetry"`
	AgentSession   db.AgentSession `json:"agent_session,omitempty"`
	Lifecycle      string          `json:"lifecycle"`
	LifecycleNote  string          `json:"lifecycle_note,omitempty"`
	RepositoryFact string          `json:"repository_fact,omitempty"`
	ModelFact      string          `json:"model_fact,omitempty"`
}

const sessionStaleAfter = 15 * time.Minute

func agentLifecycle(a db.AgentSession, now time.Time) (string, string) {
	if a.LifecycleState == "closed" || a.EndedAt != nil {
		return "closed", "producer reported session end"
	}
	last := a.LastEventAt
	if last.IsZero() {
		last = a.StartedAt
	}
	if !last.IsZero() && now.Sub(last) > sessionStaleAfter {
		if a.LifecycleState == "unknown" {
			return "stale", "inferred from old activity; lifecycle provenance is unknown"
		}
		return "stale", "no recent telemetry"
	}
	if a.LifecycleState == "active" {
		return "active", "producer reported active"
	}
	return "active", "inferred from recent activity; lifecycle provenance is unknown"
}

func applyAgentFacts(v *sessionOverviewView, a db.AgentSession) {
	v.Lifecycle, v.LifecycleNote = agentLifecycle(a, time.Now())
	if a.RepoPath != nil && strings.TrimSpace(*a.RepoPath) != "" {
		v.RepositoryFact = *a.RepoPath
	} else if v.RepositoryName != "" {
		v.RepositoryFact = v.RepositoryName + " (workflow attribution only; telemetry repository unknown)"
	} else {
		v.RepositoryFact = "unknown"
	}
	if a.Model != nil && strings.TrimSpace(*a.Model) != "" {
		v.ModelFact = *a.Model
	} else {
		v.ModelFact = "unknown"
	}
}

// telemetryOnlyStatus derives a display status for a telemetry-only
// session (no lifecycle attribution) from its agent_sessions row.
func telemetryOnlyStatus(a db.AgentSession) string {
	if a.EndedAt != nil {
		return "done"
	}
	return "running"
}

// telemetryOnlyLastUpdatedAt picks the most recent timestamp available on
// an agent_sessions row for display/sort purposes: EndedAt when the
// session has finished, otherwise StartedAt.
func telemetryOnlyLastUpdatedAt(a db.AgentSession) time.Time {
	if !a.LastEventAt.IsZero() {
		return a.LastEventAt
	}
	if a.EndedAt != nil {
		return *a.EndedAt
	}
	return a.StartedAt
}

// mergeSessionOverviews merges lifecycle-attributed sessions (known) with
// globally captured telemetry sessions (agents) by session identity
// (the "session" string, shared across both agent_sessions and the
// workflow_phase/spec_draft/external sources that back
// KnownCerberusSession). Known entries are deduplicated by identity,
// keeping the first occurrence, since a session could in principle be
// surfaced by more than one lifecycle source. Agent sessions with no
// matching lifecycle attribution are appended as telemetry-only entries
// so activity is never silently dropped from the session list. The
// result is ordered by LastUpdatedAt, most recent first.
func mergeSessionOverviews(known []cerberusSessionView, agents []db.AgentSession) []sessionOverviewView {
	agentBySession := make(map[string]db.AgentSession, len(agents))
	for _, a := range agents {
		agentBySession[a.Session] = a
	}

	seen := make(map[string]bool, len(known)+len(agents))
	views := make([]sessionOverviewView, 0, len(known)+len(agents))

	for _, k := range known {
		if seen[k.Session] {
			continue
		}
		seen[k.Session] = true
		v := sessionOverviewView{
			KnownCerberusSession: k.KnownCerberusSession,
			CerberusStatus:       k.CerberusStatus,
			CerberusError:        k.CerberusError,
			HasAttribution:       true,
		}
		if a, ok := agentBySession[k.Session]; ok {
			v.HasTelemetry = true
			v.AgentSession = a
			applyAgentFacts(&v, a)
		} else if k.SafeToClean {
			v.Lifecycle, v.LifecycleNote = "closed", "inferred from terminal Foundry record; producer telemetry unavailable"
		} else {
			v.Lifecycle, v.LifecycleNote = "active", "inferred from Foundry record; producer telemetry unavailable"
		}
		views = append(views, v)
	}

	for _, a := range agents {
		if seen[a.Session] {
			continue
		}
		seen[a.Session] = true
		v := sessionOverviewView{
			KnownCerberusSession: db.KnownCerberusSession{
				Session:       a.Session,
				Type:          "telemetry",
				FoundryStatus: telemetryOnlyStatus(a),
				LastUpdatedAt: telemetryOnlyLastUpdatedAt(a),
				UnsafeReason:  "no workflow phase, spec draft, or external record attributes this session",
			},
			HasAttribution: false,
			HasTelemetry:   true,
			AgentSession:   a,
		}
		applyAgentFacts(&v, a)
		views = append(views, v)
	}

	sort.SliceStable(views, func(i, j int) bool {
		if views[i].LastUpdatedAt.Equal(views[j].LastUpdatedAt) {
			return views[i].Session > views[j].Session
		}
		return views[i].LastUpdatedAt.After(views[j].LastUpdatedAt)
	})

	return views
}

func (s *Handler) handleUISessionsPage(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/sessions" {
		http.NotFound(w, r)
		return
	}
	s.renderShell(w, "sessions", "/sessions/fragment")
}

func (s *Handler) handleUISessionsFragment(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	requestedLifecycle := q.Get("lifecycle")
	params := db.AgentSessionPageParams{
		Limit: 51, Lifecycle: requestedLifecycle,
		// Select the common (last_updated_at,session) ordering even on the
		// first page. The value is ignored until BeforeAt is present.
		BeforeSession: "\U0010ffff",
	}
	if id, err := strconv.ParseInt(q.Get("repository_id"), 10, 64); err == nil && id > 0 {
		params.RepositoryID = &id
	}
	if at, err := time.Parse(time.RFC3339Nano, q.Get("before")); err == nil {
		params.BeforeAt = &at
		if session := q.Get("before_session"); session != "" {
			params.BeforeSession = session
		}
	}
	sessions, sessionErr := s.sessionOverviews(r.Context(), false, params)
	sessionErrMsg := ""
	if sessionErr != nil {
		sessionErrMsg = sessionErr.Error()
	}
	hasMore := len(sessions) > 50
	if hasMore {
		sessions = sessions[:50]
	}
	var nextAt, nextSession string
	if hasMore && len(sessions) > 0 {
		last := sessions[len(sessions)-1]
		nextAt = last.LastUpdatedAt.Format(time.RFC3339Nano)
		nextSession = last.Session
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := templates.ExecuteTemplate(w, "sessions.list", struct {
		Sessions     []sessionOverviewView
		SessionError string
		Lifecycle    string
		RepositoryID string
		HasMore      bool
		NextAt       string
		NextSession  string
	}{sessions, sessionErrMsg, requestedLifecycle, q.Get("repository_id"), hasMore, nextAt, nextSession}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (s *Handler) handleUISession(w http.ResponseWriter, r *http.Request) {
	name, suffix, ok := parseUISessionSuffix(r.URL.Path, "/sessions/")
	if !ok || (suffix != "" && suffix != "fragment") {
		http.NotFound(w, r)
		return
	}
	if suffix == "fragment" {
		s.handleUISessionFragment(w, r, name)
		return
	}
	s.renderShell(w, "sessions", fmt.Sprintf("/sessions/%s/fragment", name))
}

func (s *Handler) handleUISessionFragment(w http.ResponseWriter, r *http.Request, name string) {
	found, err := s.sessionOverviewByName(r.Context(), name)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	var telemetry *telemetrySessionView
	if found.HasTelemetry {
		turns, err := db.ListAgentTurnsBySession(r.Context(), s.pool, found.AgentSession.ID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		toolCalls, err := db.ListAgentToolCallsBySession(r.Context(), s.pool, found.AgentSession.ID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		messages, err := db.ListAgentMessagesBySession(r.Context(), s.pool, found.AgentSession.ID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		sv := buildTelemetrySessionView(found.AgentSession, turns, toolCalls, messages)
		telemetry = &sv
		models := make([]string, 0, len(turns)+1)
		if found.AgentSession.Model != nil && *found.AgentSession.Model != "" {
			models = append(models, *found.AgentSession.Model)
		}
		for _, turn := range turns {
			if turn.Model == "" || turn.Model == "unknown" || (len(models) > 0 && models[len(models)-1] == turn.Model) {
				continue
			}
			models = append(models, turn.Model)
		}
		if len(models) > 0 {
			found.ModelFact = strings.Join(models, " → ")
		}
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := templates.ExecuteTemplate(w, "sessions.detail", struct {
		Session   sessionOverviewView
		Telemetry *telemetrySessionView
	}{*found, telemetry}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// parseUISessionSuffix splits a "/sessions/<name>[/<suffix>]" path into the
// session name and optional trailing suffix (e.g. "fragment"). Session names
// are opaque strings (not numeric IDs), so this mirrors parseUIIDSuffix
// without the integer parsing.
func parseUISessionSuffix(path, prefix string) (string, string, bool) {
	if !strings.HasPrefix(path, prefix) {
		return "", "", false
	}
	rest := strings.Trim(strings.TrimPrefix(path, prefix), "/")
	if rest == "" {
		return "", "", false
	}
	parts := strings.SplitN(rest, "/", 2)
	if len(parts) == 1 {
		return parts[0], "", true
	}
	return parts[0], parts[1], true
}

func (s *Handler) knownCerberusSessionViews(ctx context.Context, withStatus bool) ([]cerberusSessionView, error) {
	known, err := db.ListKnownCerberusSessions(ctx, s.pool)
	if err != nil {
		return nil, err
	}
	views := make([]cerberusSessionView, 0, len(known))
	for _, k := range known {
		v := cerberusSessionView{KnownCerberusSession: k}
		if withStatus && s.cerb != nil {
			var status string
			var err error
			repoPath := strings.TrimSpace(k.RepositoryLocalPath)
			if repoPath != "" {
				status, err = s.cerb.WithRepo(repoPath).Status(ctx, k.Session)
			} else {
				status, err = s.cerb.Status(ctx, k.Session)
			}
			if err != nil {
				v.CerberusError = err.Error()
			} else {
				v.CerberusStatus = status
			}
		}
		views = append(views, v)
	}
	return views, nil
}

// sessionOverviews assembles the merged session list: known
// (lifecycle-attributed) sessions with their live Cerberus status, plus
// any agent_sessions rows without lifecycle attribution as telemetry-only
// entries. A failure loading either source fails the whole call, since a
// partial merge could misreport a session's attribution.
func (s *Handler) sessionOverviewByName(ctx context.Context, name string) (*sessionOverviewView, error) {
	known, err := s.knownCerberusSessionViews(ctx, false)
	if err != nil {
		return nil, err
	}
	matchingKnown := make([]cerberusSessionView, 0, 1)
	for _, k := range known {
		if k.Session == name {
			matchingKnown = append(matchingKnown, k)
			break
		}
	}
	agents := []db.AgentSession{}
	if a, getErr := db.GetAgentSessionBySession(ctx, s.pool, name); getErr == nil {
		agents = append(agents, a)
	} else if !errors.Is(getErr, db.ErrNotFound) {
		return nil, getErr
	}
	views := mergeSessionOverviews(matchingKnown, agents)
	if len(views) == 0 {
		return nil, db.ErrNotFound
	}
	return &views[0], nil
}

func (s *Handler) sessionOverviews(ctx context.Context, withStatus bool, params db.AgentSessionPageParams) ([]sessionOverviewView, error) {
	knownRows, err := db.ListKnownCerberusSessionsPage(ctx, s.pool, db.KnownCerberusSessionPageParams{
		Limit: params.Limit, BeforeAt: params.BeforeAt, BeforeSession: params.BeforeSession,
		Lifecycle: params.Lifecycle, RepositoryID: params.RepositoryID,
	})
	if err != nil {
		return nil, err
	}
	known := make([]cerberusSessionView, 0, len(knownRows))
	for _, k := range knownRows {
		v := cerberusSessionView{KnownCerberusSession: k}
		if withStatus && s.cerb != nil {
			var statusErr error
			if repoPath := strings.TrimSpace(k.RepositoryLocalPath); repoPath != "" {
				v.CerberusStatus, statusErr = s.cerb.WithRepo(repoPath).Status(ctx, k.Session)
			} else {
				v.CerberusStatus, statusErr = s.cerb.Status(ctx, k.Session)
			}
			if statusErr != nil {
				v.CerberusError = statusErr.Error()
			}
		}
		known = append(known, v)
	}
	// Persisted attribution owns identities present in both sources. Excluding
	// those identities from the telemetry page prevents them reappearing on a
	// later page when the two sources have different activity timestamps.
	params.ExcludeAttributed = true
	agents, err := db.ListAgentSessionsPage(ctx, s.pool, params)
	if err != nil {
		return nil, err
	}
	knownNames := make([]string, 0, len(known))
	for _, k := range known {
		knownNames = append(knownNames, k.Session)
	}
	attached, err := db.ListAgentSessionsBySessionNames(ctx, s.pool, knownNames)
	if err != nil {
		return nil, err
	}
	agents = append(agents, attached...)
	return mergeSessionOverviews(known, agents), nil
}
