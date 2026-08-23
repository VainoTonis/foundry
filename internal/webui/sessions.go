package webui

import (
	"context"
	"fmt"
	"net/http"
	"sort"
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
		}
		views = append(views, v)
	}

	for _, a := range agents {
		if seen[a.Session] {
			continue
		}
		seen[a.Session] = true
		views = append(views, sessionOverviewView{
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
		})
	}

	sort.SliceStable(views, func(i, j int) bool {
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
	sessions, sessionErr := s.sessionOverviews(r.Context(), true)
	sessionErrMsg := ""
	if sessionErr != nil {
		sessionErrMsg = sessionErr.Error()
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := templates.ExecuteTemplate(w, "sessions.list", struct {
		Sessions     []sessionOverviewView
		SessionError string
	}{sessions, sessionErrMsg}); err != nil {
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
	sessions, err := s.sessionOverviews(r.Context(), true)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	var found *sessionOverviewView
	for i := range sessions {
		if sessions[i].Session == name {
			found = &sessions[i]
			break
		}
	}
	if found == nil {
		http.NotFound(w, r)
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
func (s *Handler) sessionOverviews(ctx context.Context, withStatus bool) ([]sessionOverviewView, error) {
	known, err := s.knownCerberusSessionViews(ctx, withStatus)
	if err != nil {
		return nil, err
	}
	agents, err := db.ListAgentSessions(ctx, s.pool)
	if err != nil {
		return nil, err
	}
	return mergeSessionOverviews(known, agents), nil
}
