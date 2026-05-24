package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/tonis2/foundry/internal/cerberus"
	"github.com/tonis2/foundry/internal/db"
	"github.com/tonis2/foundry/internal/discover"
	"github.com/tonis2/foundry/internal/hub"
	"github.com/tonis2/foundry/internal/workflow"
)

// Server holds all handler dependencies.
type Server struct {
	pool            *pgxpool.Pool
	runner          *workflow.Runner
	cerb            *cerberus.Client
	mux             *http.ServeMux
	eventHub        *hub.EventHub
	defaultBudget   float64
	gitRoot         string
	cfgPath         string
	serverPort      int
	cerberusProfile string
	cerbEventsMu    sync.Mutex
	cerbBuffers     map[string]*cerberusTextBuffer
}

func NewServer(pool *pgxpool.Pool, runner *workflow.Runner, cerb *cerberus.Client, eventHub *hub.EventHub, defaultBudget float64, gitRoot string, cfgPath string, cerberusProfile string, serverPort int) *Server {
	s := &Server{pool: pool, runner: runner, cerb: cerb, eventHub: eventHub, defaultBudget: defaultBudget, gitRoot: gitRoot, cfgPath: cfgPath, serverPort: serverPort, cerberusProfile: cerberusProfile, cerbBuffers: make(map[string]*cerberusTextBuffer)}
	s.mux = http.NewServeMux()
	s.routes()
	return s
}

func (s *Server) callbackURL() string {
	return fmt.Sprintf("http://localhost:%d/api/cerberus/events", s.serverPort)
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

func (s *Server) routes() {
	s.mux.HandleFunc("/", s.handleUIShell)
	s.mux.HandleFunc("/backlog", s.handleUIBacklogPage)
	s.mux.HandleFunc("/backlog/fragment", s.handleUIBacklogFragment)
	s.mux.HandleFunc("/projects", s.handleUIProjectsPage)
	s.mux.HandleFunc("/projects/fragment", s.handleUIProjectsFragment)
	s.mux.HandleFunc("/projects/", s.handleUIProject)
	s.mux.HandleFunc("/settings", s.handleUISettingsPage)
	s.mux.HandleFunc("/settings/fragment", s.handleUISettingsFragment)
	s.mux.HandleFunc("/specs/", s.handleUISpec)
	s.mux.HandleFunc("/workflows/", s.handleUIWorkflow)
	s.mux.HandleFunc("/phases/", s.handleUIPhase)
	s.mux.HandleFunc("/spec-builder", s.handleUISpecBuilderPage)
	s.mux.HandleFunc("/spec-builder/fragment", s.handleUISpecBuilderStartFragment)
	s.mux.HandleFunc("/spec-builder/", s.handleUISpecBuilder)

	s.mux.HandleFunc("/api/projects", s.handleProjects)
	s.mux.HandleFunc("/api/projects/discover", s.handleDiscover)
	s.mux.HandleFunc("/api/projects/", s.handleProject)

	s.mux.HandleFunc("/api/specs", s.handleSpecs)
	s.mux.HandleFunc("/api/specs/", s.handleSpec)

	s.mux.HandleFunc("/api/workflows", s.handleWorkflows)
	s.mux.HandleFunc("/api/workflows/", s.handleWorkflow)

	s.mux.HandleFunc("/api/phases/", s.handlePhase)
	s.mux.HandleFunc("/api/settings", s.handleSettings)
	s.mux.HandleFunc("/api/profiles", s.handleProfiles)
	s.mux.HandleFunc("/api/profiles/", s.handleProfile)
	s.mux.HandleFunc("/api/cerberus/events", s.handleCerberusCallback)
	s.mux.HandleFunc("/api/spec-drafts", s.handleSpecDrafts)
	s.mux.HandleFunc("/api/spec-drafts/", s.handleSpecDraft)
}

// ---- server-rendered UI ----

var uiTemplates = template.Must(template.New("ui").Funcs(template.FuncMap{
	"date":     func(t time.Time) string { return t.Format("2006-01-02") },
	"datetime": func(t time.Time) string { return t.Format("2006-01-02 15:04:05") },
	"ptime": func(t *time.Time) string {
		if t == nil {
			return "—"
		}
		return t.Format("2006-01-02 15:04:05")
	},
	"money": func(f *float64) string {
		if f == nil {
			return "—"
		}
		return fmt.Sprintf("$%.4f", *f)
	},
	"strptr": func(s *string) string {
		if s == nil {
			return ""
		}
		return *s
	},
	"json": func(v any) string {
		b, _ := json.MarshalIndent(v, "", "  ")
		return string(b)
	},
}).Parse(`
{{define "shell"}}
<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>Foundry</title>
  <link rel="stylesheet" href="/style.css">
  <script src="https://unpkg.com/htmx.org@1.9.12"></script>
  <script defer src="/app.js"></script>
</head>
<body data-page="{{.Page}}">
  <header>
    <h1><a href="/" hx-get="/backlog/fragment" hx-target="#app" hx-push-url="/" class="brand">Foundry</a></h1>
    <nav>
      <a href="/backlog" data-nav="backlog" hx-get="/backlog/fragment" hx-target="#app" hx-push-url="/backlog">Backlog</a>
      <a href="/projects" data-nav="projects" hx-get="/projects/fragment" hx-target="#app" hx-push-url="/projects">Projects</a>
      <a href="/spec-builder" data-nav="builder" hx-get="/spec-builder/fragment" hx-target="#app" hx-push-url="/spec-builder">Spec Builder</a>
      <a href="/settings" data-nav="settings" hx-get="/settings/fragment" hx-target="#app" hx-push-url="/settings">Settings</a>
    </nav>
  </header>
  <main id="app" hx-get="{{.Fragment}}" hx-trigger="load" hx-swap="innerHTML"></main>
</body>
</html>
{{end}}

{{define "backlog"}}
<div data-page="backlog">
  <div class="page-header">
    <h2>Backlog</h2>
    <div class="card-actions">
      <a class="btn btn-primary" href="/spec-builder" hx-get="/spec-builder/fragment" hx-target="#app" hx-push-url="/spec-builder">Build with AI</a>
      <button class="btn btn-primary" popovertarget="new-spec">+ Spec</button>
      <button class="btn" popovertarget="new-project">+ Project</button>
    </div>
  </div>
  <div id="new-project" class="popover-card" popover>
    <h3>New Project</h3>
    <form data-json method="post" action="/api/projects" data-refresh="/backlog/fragment" data-target="#app">
      <div class="field"><label>Name</label><input name="name" required></div>
      <div class="field"><label>Repo path</label><input name="repo_path" required></div>
      <div class="field"><label>Memory repo path</label><input name="memory_repo_path" placeholder="Private memory repo path"></div>
      <button class="btn btn-primary">Create</button>
    </form>
  </div>
  <div id="new-spec" class="popover-card" popover>
    <h3>New Spec</h3>
    <form data-json method="post" action="/api/specs" data-refresh="/backlog/fragment" data-target="#app">
      <div class="field"><label>Title</label><input name="title" required></div>
      <div class="field"><label>Project</label><select name="project_id" required>{{range .Projects}}<option value="{{.ID}}">{{.Name}}</option>{{end}}</select></div>
      <div class="field"><label>Content</label><textarea name="content" required># Feature title

Global context here.

## Phase 1: Bootstrap

What this phase does.</textarea></div>
      <button class="btn btn-primary">Create</button>
    </form>
  </div>
  {{if .SpecsByStatus}}
    {{range .Statuses}}
      {{$items := index $.SpecsByStatus .}}
      {{if $items}}
        <div class="group-label">{{.}}</div>
        {{range $items}}
          <article class="card">
            <div class="card-header"><a class="card-title" href="/specs/{{.ID}}" hx-get="/specs/{{.ID}}/fragment" hx-target="#app" hx-push-url="/specs/{{.ID}}">{{.Title}}</a><span class="chip chip-{{.Track}}">{{.Track}}</span><span class="chip chip-{{.Status}}">{{.Status}}</span></div>
            <div class="card-meta">Project #{{.ProjectID}} · {{date .CreatedAt}}</div>
            <div class="card-actions"><button class="btn btn-primary" data-json-post="/api/workflows" data-body='{"spec_id":{{.ID}}}' data-redirect-template="/workflows/{id}">Run Workflow</button></div>
          </article>
        {{end}}
      {{end}}
    {{end}}
  {{else}}<div class="empty">No specs yet. Create one to get started.</div>{{end}}
  {{if .Drafts}}
    <div class="group-label">spec builder drafts</div>
    {{range .Drafts}}<article class="card"><div class="card-header"><a class="card-title" href="/spec-builder/{{.ID}}" hx-get="/spec-builder/{{.ID}}/fragment" hx-target="#app" hx-push-url="/spec-builder/{{.ID}}">{{.Title}}</a><span class="chip chip-running">{{.Status}}</span></div><div class="card-meta">{{date .CreatedAt}}</div></article>{{end}}
  {{end}}
</div>
{{end}}

{{define "projects"}}
<div data-page="projects">
  <div class="page-header"><h2>Projects</h2><div class="card-actions"><button class="btn" popovertarget="new-project-page">+ Project</button><button class="btn btn-primary" hx-get="/projects/fragment?discover=1" hx-target="#app" hx-push-url="/projects">Discover repos</button></div></div>
  <div id="new-project-page" class="popover-card" popover>
    <h3>New Project</h3>
    <form data-json data-include-empty method="post" action="/api/projects" data-refresh="/projects/fragment" data-target="#app">
      <div class="field"><label>Name</label><input name="name" required></div>
      <div class="field"><label>Target repo path</label><input name="repo_path" required></div>
      <div class="field"><label>Memory repo path</label><input name="memory_repo_path" placeholder="Private memory repo path"></div>
      <button class="btn btn-primary">Create</button>
    </form>
  </div>
  {{if .Projects}}<div class="group-label">Registered projects</div>{{range .Projects}}<article class="card"><div class="card-header"><a class="card-title" href="/projects/{{.ID}}" hx-get="/projects/{{.ID}}/fragment" hx-target="#app" hx-push-url="/projects/{{.ID}}">{{.Name}}</a></div><div class="card-meta">target: {{.RepoPath}} · memory: {{if .MemoryRepoPath}}{{.MemoryRepoPath}}{{else}}—{{end}}</div><div class="card-actions"><a class="btn" href="/projects/{{.ID}}" hx-get="/projects/{{.ID}}/fragment" hx-target="#app" hx-push-url="/projects/{{.ID}}">View / edit</a></div></article>{{end}}{{else}}<div class="empty">No projects yet. Create one or click Discover repos to scan configured git root.</div>{{end}}
  {{if .DiscoverErr}}<div class="empty">{{.DiscoverErr}}</div>{{end}}
  {{if .Repos}}<div class="group-label">Discovered repos</div>{{range .Repos}}<article class="card"><div class="card-header"><span class="card-title">{{.Name}}</span>{{if .Imported}}<span class="chip chip-done">imported</span>{{end}}</div><div class="card-meta">target: {{.Path}}{{if .Imported}} · memory: {{if .MemoryRepoPath}}{{.MemoryRepoPath}}{{else}}—{{end}}{{end}}</div>{{if not .Imported}}<div class="card-actions"><button class="btn btn-primary" data-json-post="/api/projects" data-body='{"name":{{printf "%q" .Name}},"repo_path":{{printf "%q" .Path}},"memory_repo_path":""}' data-refresh="/projects/fragment" data-target="#app">Import</button></div>{{end}}</article>{{end}}{{end}}
</div>
{{end}}

{{define "projectDetail"}}
<div data-page="projects">
  <a class="back" href="/projects" hx-get="/projects/fragment" hx-target="#app" hx-push-url="/projects">← Projects</a>
  <div class="page-header"><div><h2>{{.Project.Name}}</h2><div class="card-meta">Project #{{.Project.ID}} · created {{date .Project.CreatedAt}}</div></div><button class="btn btn-danger" data-json-delete="/api/projects/{{.Project.ID}}" data-redirect="/projects" data-confirm="Delete this project and its specs/workflows?">Delete</button></div>
  <form data-json data-include-empty data-method="PATCH" method="post" action="/api/projects/{{.Project.ID}}" data-refresh="/projects/{{.Project.ID}}/fragment" data-target="#app">
    <div class="field"><label>Name</label><input name="name" value="{{.Project.Name}}" required></div>
    <div class="field"><label>Target repo path</label><input name="repo_path" value="{{.Project.RepoPath}}" required></div>
    <div class="field"><label>Memory repo path</label><input name="memory_repo_path" value="{{.Project.MemoryRepoPath}}" placeholder="Private memory repo path"></div>
    <button class="btn btn-primary">Save changes</button>
  </form>
</div>
{{end}}

{{define "specDetail"}}
<div data-page="backlog">
  <a class="back" href="/backlog" hx-get="/backlog/fragment" hx-target="#app" hx-push-url="/backlog">← Backlog</a>
  <div class="page-header"><div><h2>{{.Spec.Title}}</h2><div class="card-meta">Spec #{{.Spec.ID}} · Project #{{.Spec.ProjectID}} · {{date .Spec.CreatedAt}}</div></div><div class="card-actions"><span class="chip chip-{{.Spec.Track}}">{{.Spec.Track}}</span><span class="chip chip-{{.Spec.Status}}">{{.Spec.Status}}</span></div></div>
  <div class="card-actions"><button class="btn btn-primary" data-json-post="/api/workflows" data-body='{"spec_id":{{.Spec.ID}}}' data-redirect-template="/workflows/{id}">Run Workflow</button>{{if eq .Spec.Track "poc"}}<button class="btn" data-json-post="/api/specs/{{.Spec.ID}}/promote" data-refresh="/specs/{{.Spec.ID}}/fragment" data-target="#app">Promote to polish</button>{{end}}</div>
  <div class="section"><h3>Content</h3><pre class="doc-box">{{.Spec.Content}}</pre></div>
  <div class="section"><h3>Workflows</h3>{{if .Workflows}}{{range .Workflows}}<article class="card"><div class="card-header"><a class="card-title" href="/workflows/{{.ID}}" hx-get="/workflows/{{.ID}}/fragment" hx-target="#app" hx-push-url="/workflows/{{.ID}}">Workflow #{{.ID}}</a><span class="chip chip-{{.Status}}">{{.Status}}</span></div><div class="card-meta">{{.Track}} · budget {{money .MaxCostUSD}} · {{date .CreatedAt}}</div></article>{{end}}{{else}}<div class="empty">No workflows yet.</div>{{end}}</div>
</div>
{{end}}

{{define "workflowDetail"}}
<div data-page="backlog" data-workflow-stream="/api/workflows/{{.Workflow.ID}}/stream" data-refresh="/workflows/{{.Workflow.ID}}/fragment">
  <a class="back" href="/specs/{{.Spec.ID}}" hx-get="/specs/{{.Spec.ID}}/fragment" hx-target="#app" hx-push-url="/specs/{{.Spec.ID}}">← {{.Spec.Title}}</a>
  <div class="page-header"><div><h2>Workflow #{{.Workflow.ID}}</h2><div class="card-meta">Spec #{{.Spec.ID}} · {{.Workflow.Track}} · created {{datetime .Workflow.CreatedAt}}</div></div><span class="chip chip-{{.Workflow.Status}}">{{.Workflow.Status}}</span></div>
  <div class="card-actions"><button class="btn" data-json-post="/api/workflows/{{.Workflow.ID}}/resume" data-refresh="/workflows/{{.Workflow.ID}}/fragment" data-target="#app">Resume</button><button class="btn btn-danger" data-json-post="/api/workflows/{{.Workflow.ID}}/stop" data-refresh="/workflows/{{.Workflow.ID}}/fragment" data-target="#app">Stop</button></div>
  <div class="section"><h3>Phases</h3>{{range .Phases}}<article class="phase-row" id="phase-{{.ID}}"><div class="phase-pos">{{.Position}}</div><div class="phase-body"><div class="card-header"><span class="phase-name">{{.Name}}</span><span class="chip chip-{{.Status}}">{{.Status}}</span>{{if .ReviewVerdict}}<span class="chip chip-{{strptr .ReviewVerdict}}">{{strptr .ReviewVerdict}}</span>{{end}}</div><div class="phase-goal">{{.Goal}}</div><div class="card-meta">cost {{money .CostUSD}} · started {{ptime .StartedAt}} · finished {{ptime .FinishedAt}}</div><div class="card-actions"><button class="btn" hx-get="/phases/{{.ID}}/logs/fragment" hx-target="#phase-panel" hx-swap="innerHTML">Logs</button><button class="btn" hx-get="/phases/{{.ID}}/diff/fragment" hx-target="#phase-panel" hx-swap="innerHTML">Diff</button><button class="btn btn-primary" data-json-post="/api/phases/{{.ID}}/approve" data-refresh="/workflows/{{$.Workflow.ID}}/fragment" data-target="#app">Approve</button><button class="btn btn-danger" data-json-post="/api/phases/{{.ID}}/reject" data-refresh="/workflows/{{$.Workflow.ID}}/fragment" data-target="#app">Reject</button><button class="btn" data-json-post="/api/phases/{{.ID}}/clean" data-refresh="/workflows/{{$.Workflow.ID}}/fragment" data-target="#app">Clean</button></div></div></article>{{end}}</div>
  <div id="phase-panel" class="section"><div class="empty">Select logs or diff for a phase.</div></div>
</div>
{{end}}

{{define "phaseLogs"}}
<div><h3>Logs · Phase #{{.Phase.ID}} {{.Phase.Name}}</h3><div class="log-box" data-log-stream="/api/phases/{{.Phase.ID}}/logs/stream">{{range .Logs}}<div class="log-line"><span class="log-ts">{{datetime .Ts}}</span>{{.Line}}</div>{{end}}</div></div>
{{end}}

{{define "phaseDiff"}}
<div><h3>Diff · Phase #{{.Phase.ID}} {{.Phase.Name}}</h3>{{if .Error}}<div class="empty">{{.Error}}</div>{{else}}<pre class="diff-box">{{.Diff}}</pre>{{end}}</div>
{{end}}

{{define "builderStart"}}
<div data-page="builder">
  <div class="page-header"><h2>Spec Builder</h2></div>
  <form data-json method="post" action="/api/spec-drafts" data-redirect-template="/spec-builder/{id}">
    <div class="field"><label>Project</label><select name="project_id"><option value="">No project</option>{{range .Projects}}<option value="{{.ID}}">{{.Name}}</option>{{end}}</select></div>
    <div class="field"><label>What should be built?</label><textarea name="description" required placeholder="Describe the feature, constraints, and expected phases."></textarea></div>
    <button class="btn btn-primary">Start builder</button>
  </form>
  {{if .Drafts}}<div class="group-label">Resume active drafts</div>{{range .Drafts}}<article class="card"><div class="card-header"><a class="card-title" href="/spec-builder/{{.ID}}" hx-get="/spec-builder/{{.ID}}/fragment" hx-target="#app" hx-push-url="/spec-builder/{{.ID}}">{{.Title}}</a><span class="chip chip-running">{{.Status}}</span></div><div class="card-meta">{{datetime .UpdatedAt}}</div></article>{{end}}{{end}}
</div>
{{end}}

{{define "draftMessages"}}{{range .Messages}}<div class="chat-msg chat-msg-{{.Role}}"><div class="chat-msg-label">{{.Role}}</div><div class="chat-msg-body">{{.Content}}</div></div>{{end}}{{end}}

{{define "builderDetail"}}
<div data-page="builder" data-draft-stream="/api/spec-drafts/{{.Draft.ID}}/stream" data-draft-id="{{.Draft.ID}}">
  <a class="back" href="/spec-builder" hx-get="/spec-builder/fragment" hx-target="#app" hx-push-url="/spec-builder">← Spec Builder</a>
  <div class="page-header"><div><h2>{{.Draft.Title}}</h2><div class="card-meta">Draft #{{.Draft.ID}} · {{.Draft.Status}} · {{datetime .Draft.UpdatedAt}}</div></div><div class="card-actions"><button class="btn btn-primary" data-json-post="/api/spec-drafts/{{.Draft.ID}}/save" data-body='{"title":""}' data-redirect-template="/specs/{spec_id}">Save as spec</button><button class="btn btn-danger" data-json-delete="/api/spec-drafts/{{.Draft.ID}}" data-redirect="/backlog">Abandon</button></div></div>
  <div class="spec-builder-layout"><div class="spec-builder-chat"><div id="draft-messages" class="chat-messages">{{template "draftMessages" .}}</div><div id="draft-stream" class="chat-msg-streaming"></div><form data-json data-draft-message method="post" action="/api/spec-drafts/{{.Draft.ID}}/message"><div class="chat-input-row"><textarea class="chat-textarea" name="content" required placeholder="Reply to the spec builder…"></textarea><button class="btn btn-primary">Send</button></div></form></div><aside class="spec-preview-pane"><h3>Latest spec preview</h3><pre id="draft-preview" class="doc-box">{{if .Preview}}{{.Preview}}{{else}}Ask the builder to call update_spec with the full markdown spec.{{end}}</pre></aside></div>
</div>
{{end}}

{{define "settings"}}
<div data-page="settings">
  <h2 style="margin-bottom:1.25rem">Settings</h2>
  <form data-settings action="/api/settings" data-refresh="/settings/fragment" data-target="#app">
    {{range .Settings}}<div class="field"><label>{{.Key}}</label><input name="{{.Key}}" value="{{.Value}}"></div>{{end}}
    <p class="hint">Changes are written to config.yaml. Restart the server for most changes to take effect.</p>
    <button class="btn btn-primary">Save</button>
  </form>
  <h3 style="margin-top:2rem;margin-bottom:1rem">Profiles</h3>
  <form data-json method="post" action="/api/profiles" data-refresh="/settings/fragment" data-target="#app">
    <div class="field"><label>Name</label><input name="name" required></div>
    <div class="field"><label>Default model</label><input name="default_model"></div>
    <div class="field"><label>Default image</label><input name="default_image"></div>
    <div class="field"><label>AWS profile</label><input name="aws_profile"></div>
    <div class="field"><label>AWS region</label><input name="aws_region"></div>
    <div class="field"><label>Extra env (JSON)</label><textarea name="extra_env" placeholder='{"KEY":"value"}'></textarea></div>
    <button class="btn btn-primary">Create profile</button>
  </form>
  {{if .Profiles}}{{range .Profiles}}<article class="card"><form data-json data-include-empty data-method="PATCH" action="/api/profiles/{{.ID}}" data-refresh="/settings/fragment" data-target="#app"><div class="card-header"><span class="card-title">{{.Name}}</span></div><div class="field"><label>Name</label><input name="name" value="{{.Name}}" required></div><div class="field"><label>Default model</label><input name="default_model" value="{{.DefaultModel}}"></div><div class="field"><label>Default image</label><input name="default_image" value="{{.DefaultImage}}"></div><div class="field"><label>AWS profile</label><input name="aws_profile" value="{{.AWSProfile}}"></div><div class="field"><label>AWS region</label><input name="aws_region" value="{{.AWSRegion}}"></div><div class="field"><label>Extra env (JSON)</label><textarea name="extra_env">{{json .ExtraEnv}}</textarea></div><div class="card-actions"><button class="btn btn-primary">Save profile</button><button class="btn btn-danger" type="button" data-json-delete="/api/profiles/{{.ID}}" data-refresh="/settings/fragment" data-target="#app">Delete</button></div></form></article>{{end}}{{else}}<div class="empty">No profiles saved.</div>{{end}}
</div>
{{end}}
`))

type shellData struct{ Page, Fragment string }

func (s *Server) renderShell(w http.ResponseWriter, page, fragment string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := uiTemplates.ExecuteTemplate(w, "shell", shellData{Page: page, Fragment: fragment}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (s *Server) handleUIShell(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	s.renderShell(w, "backlog", "/backlog/fragment")
}
func (s *Server) handleUIBacklogPage(w http.ResponseWriter, r *http.Request) {
	s.renderShell(w, "backlog", "/backlog/fragment")
}
func (s *Server) handleUIProjectsPage(w http.ResponseWriter, r *http.Request) {
	s.renderShell(w, "projects", "/projects/fragment")
}
func (s *Server) handleUISettingsPage(w http.ResponseWriter, r *http.Request) {
	s.renderShell(w, "settings", "/settings/fragment")
}

func (s *Server) handleUIBacklogFragment(w http.ResponseWriter, r *http.Request) {
	projects, _ := db.ListProjects(r.Context(), s.pool)
	specs, err := db.ListSpecs(r.Context(), s.pool, db.ListSpecsFilter{})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	drafts, _ := db.ListSpecDrafts(r.Context(), s.pool)
	activeDrafts := make([]db.SpecDraft, 0)
	for _, d := range drafts {
		if d.Status == "active" {
			activeDrafts = append(activeDrafts, d)
		}
	}
	byStatus := map[string][]db.Spec{}
	for _, sp := range specs {
		byStatus[sp.Status] = append(byStatus[sp.Status], sp)
	}
	data := struct {
		Projects      []db.Project
		SpecsByStatus map[string][]db.Spec
		Statuses      []string
		Drafts        []db.SpecDraft
	}{projects, byStatus, []string{"running", "queued", "paused", "done", "failed", "dumpster"}, activeDrafts}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := uiTemplates.ExecuteTemplate(w, "backlog", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

type uiRepoItem struct {
	discover.Repo
	Imported       bool
	MemoryRepoPath string
}

func (s *Server) handleUIProjectsFragment(w http.ResponseWriter, r *http.Request) {
	projects, err := db.ListProjects(r.Context(), s.pool)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	var repos []uiRepoItem
	var discoverErr string
	if r.URL.Query().Get("discover") == "1" {
		if s.gitRoot == "" {
			discoverErr = "git_root not configured"
		} else if found, err := discover.FindRepos(s.gitRoot); err != nil {
			discoverErr = err.Error()
		} else {
			byPath := map[string]db.Project{}
			for _, p := range projects {
				byPath[p.RepoPath] = p
			}
			for _, repo := range found {
				p, imported := byPath[repo.Path]
				repos = append(repos, uiRepoItem{Repo: repo, Imported: imported, MemoryRepoPath: p.MemoryRepoPath})
			}
		}
	}
	data := struct {
		Projects    []db.Project
		Repos       []uiRepoItem
		DiscoverErr string
	}{projects, repos, discoverErr}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := uiTemplates.ExecuteTemplate(w, "projects", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (s *Server) handleUIProject(w http.ResponseWriter, r *http.Request) {
	id, frag, ok := parseUIID(r.URL.Path, "/projects/")
	if !ok {
		http.NotFound(w, r)
		return
	}
	if frag {
		s.handleUIProjectFragment(w, r, id)
		return
	}
	s.renderShell(w, "projects", fmt.Sprintf("/projects/%d/fragment", id))
}

func (s *Server) handleUIProjectFragment(w http.ResponseWriter, r *http.Request, id int64) {
	p, err := db.GetProject(r.Context(), s.pool, id)
	if errors.Is(err, db.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := uiTemplates.ExecuteTemplate(w, "projectDetail", struct{ Project db.Project }{p}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (s *Server) handleUISpec(w http.ResponseWriter, r *http.Request) {
	id, frag, ok := parseUIID(r.URL.Path, "/specs/")
	if !ok {
		http.NotFound(w, r)
		return
	}
	if frag {
		s.handleUISpecFragment(w, r, id)
		return
	}
	s.renderShell(w, "backlog", fmt.Sprintf("/specs/%d/fragment", id))
}

func (s *Server) handleUISpecFragment(w http.ResponseWriter, r *http.Request, id int64) {
	sp, err := db.GetSpec(r.Context(), s.pool, id)
	if errors.Is(err, db.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	wfs, _ := db.ListWorkflowsBySpec(r.Context(), s.pool, id)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := uiTemplates.ExecuteTemplate(w, "specDetail", struct {
		Spec      db.Spec
		Workflows []db.Workflow
	}{sp, wfs}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (s *Server) handleUIWorkflow(w http.ResponseWriter, r *http.Request) {
	id, suffix, ok := parseUIIDSuffix(r.URL.Path, "/workflows/")
	if !ok || (suffix != "" && suffix != "fragment") {
		http.NotFound(w, r)
		return
	}
	if suffix == "fragment" {
		s.handleUIWorkflowFragment(w, r, id)
		return
	}
	s.renderShell(w, "backlog", fmt.Sprintf("/workflows/%d/fragment", id))
}

func (s *Server) handleUIWorkflowFragment(w http.ResponseWriter, r *http.Request, id int64) {
	wf, err := db.GetWorkflow(r.Context(), s.pool, id)
	if errors.Is(err, db.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	sp, _ := db.GetSpec(r.Context(), s.pool, wf.SpecID)
	phases, err := db.ListPhasesByWorkflow(r.Context(), s.pool, id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := uiTemplates.ExecuteTemplate(w, "workflowDetail", struct {
		Workflow db.Workflow
		Spec     db.Spec
		Phases   []db.Phase
	}{wf, sp, phases}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (s *Server) handleUIPhase(w http.ResponseWriter, r *http.Request) {
	id, suffix, ok := parseUIIDSuffix(r.URL.Path, "/phases/")
	if !ok {
		http.NotFound(w, r)
		return
	}
	switch suffix {
	case "logs/fragment":
		s.handleUIPhaseLogsFragment(w, r, id)
	case "diff/fragment":
		s.handleUIPhaseDiffFragment(w, r, id)
	default:
		http.NotFound(w, r)
	}
}

func (s *Server) handleUIPhaseLogsFragment(w http.ResponseWriter, r *http.Request, id int64) {
	ph, err := db.GetPhase(r.Context(), s.pool, id)
	if errors.Is(err, db.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	logs, _ := db.ListRecentPhaseLogs(r.Context(), s.pool, id, 300)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := uiTemplates.ExecuteTemplate(w, "phaseLogs", struct {
		Phase db.Phase
		Logs  []db.PhaseLog
	}{ph, logs}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (s *Server) handleUIPhaseDiffFragment(w http.ResponseWriter, r *http.Request, id int64) {
	ph, err := db.GetPhase(r.Context(), s.pool, id)
	if errors.Is(err, db.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	var diff, msg string
	if ph.CerberusSession == nil {
		msg = "No Cerberus session for this phase yet."
	} else if d, err := s.cerb.Diff(r.Context(), *ph.CerberusSession); err != nil {
		msg = err.Error()
	} else {
		diff = d
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := uiTemplates.ExecuteTemplate(w, "phaseDiff", struct {
		Phase       db.Phase
		Diff, Error string
	}{ph, diff, msg}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (s *Server) handleUISpecBuilderPage(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/spec-builder" {
		http.NotFound(w, r)
		return
	}
	s.renderShell(w, "builder", "/spec-builder/fragment")
}

func (s *Server) handleUISpecBuilderStartFragment(w http.ResponseWriter, r *http.Request) {
	projects, _ := db.ListProjects(r.Context(), s.pool)
	drafts, _ := db.ListSpecDrafts(r.Context(), s.pool)
	active := []db.SpecDraft{}
	for _, d := range drafts {
		if d.Status == "active" {
			active = append(active, d)
		}
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := uiTemplates.ExecuteTemplate(w, "builderStart", struct {
		Projects []db.Project
		Drafts   []db.SpecDraft
	}{projects, active}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (s *Server) handleUISpecBuilder(w http.ResponseWriter, r *http.Request) {
	id, suffix, ok := parseUIIDSuffix(r.URL.Path, "/spec-builder/")
	if !ok || (suffix != "" && suffix != "fragment") {
		http.NotFound(w, r)
		return
	}
	if suffix == "fragment" {
		s.handleUISpecBuilderDetailFragment(w, r, id)
		return
	}
	s.renderShell(w, "builder", fmt.Sprintf("/spec-builder/%d/fragment", id))
}

type uiChatMessage struct{ Role, Content string }

func (s *Server) handleUISpecBuilderDetailFragment(w http.ResponseWriter, r *http.Request, id int64) {
	draft, err := db.GetSpecDraft(r.Context(), s.pool, id)
	if errors.Is(err, db.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	var msgs []uiChatMessage
	_ = json.Unmarshal(draft.Messages, &msgs)
	preview := extractFinalSpec(draft.Messages)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := uiTemplates.ExecuteTemplate(w, "builderDetail", struct {
		Draft    db.SpecDraft
		Messages []uiChatMessage
		Preview  string
	}{draft, msgs, preview}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (s *Server) handleUISettingsFragment(w http.ResponseWriter, r *http.Request) {
	data, err := os.ReadFile(s.cfgPath)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	profiles, _ := db.ListProfiles(r.Context(), s.pool)
	type setting struct{ Key, Value string }
	var settings []setting
	for _, line := range strings.Split(string(data), "\n") {
		parts := strings.SplitN(line, ":", 2)
		if len(parts) == 2 && strings.TrimSpace(parts[0]) != "" {
			settings = append(settings, setting{Key: strings.TrimSpace(parts[0]), Value: strings.Trim(strings.TrimSpace(parts[1]), "\"")})
		}
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := uiTemplates.ExecuteTemplate(w, "settings", struct {
		Settings []setting
		Profiles []db.Profile
	}{settings, profiles}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// ---- projects ----

func (s *Server) handleProjects(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		var body struct {
			Name           string `json:"name"`
			RepoPath       string `json:"repo_path"`
			MemoryRepoPath string `json:"memory_repo_path"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			jsonErr(w, err.Error(), http.StatusBadRequest)
			return
		}
		p, err := db.CreateProject(r.Context(), s.pool, body.Name, body.RepoPath, body.MemoryRepoPath)
		if err != nil {
			jsonErr(w, err.Error(), http.StatusInternalServerError)
			return
		}
		jsonOK(w, p, http.StatusCreated)
	case http.MethodGet:
		list, err := db.ListProjects(r.Context(), s.pool)
		if err != nil {
			jsonErr(w, err.Error(), http.StatusInternalServerError)
			return
		}
		jsonOK(w, list, http.StatusOK)
	default:
		jsonErr(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleDiscover(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonErr(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.gitRoot == "" {
		jsonErr(w, "git_root not configured", http.StatusConflict)
		return
	}
	repos, err := discover.FindRepos(s.gitRoot)
	if err != nil {
		jsonErr(w, err.Error(), http.StatusInternalServerError)
		return
	}
	// cross-reference with already-registered projects so UI can mark which are imported
	existing, _ := db.ListProjects(r.Context(), s.pool)
	byPath := make(map[string]db.Project, len(existing))
	for _, p := range existing {
		byPath[p.RepoPath] = p
	}
	type repoItem struct {
		discover.Repo
		Imported       bool   `json:"imported"`
		MemoryRepoPath string `json:"memory_repo_path"`
	}
	out := make([]repoItem, 0, len(repos))
	for _, repo := range repos {
		p, imported := byPath[repo.Path]
		out = append(out, repoItem{Repo: repo, Imported: imported, MemoryRepoPath: p.MemoryRepoPath})
	}
	jsonOK(w, out, http.StatusOK)
}

func (s *Server) handleProject(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r.URL.Path, "/api/projects/")
	if err != nil {
		jsonErr(w, "invalid id", http.StatusBadRequest)
		return
	}

	switch r.Method {
	case http.MethodGet:
		p, err := db.GetProject(r.Context(), s.pool, id)
		if errors.Is(err, db.ErrNotFound) {
			jsonErr(w, "not found", http.StatusNotFound)
			return
		}
		if err != nil {
			jsonErr(w, err.Error(), http.StatusInternalServerError)
			return
		}
		jsonOK(w, p, http.StatusOK)
	case http.MethodPatch:
		var body struct {
			Name           *string `json:"name"`
			RepoPath       *string `json:"repo_path"`
			MemoryRepoPath *string `json:"memory_repo_path"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			jsonErr(w, err.Error(), http.StatusBadRequest)
			return
		}
		p, err := db.UpdateProject(r.Context(), s.pool, id, db.UpdateProjectParams{
			Name:           body.Name,
			RepoPath:       body.RepoPath,
			MemoryRepoPath: body.MemoryRepoPath,
		})
		if errors.Is(err, db.ErrNotFound) {
			jsonErr(w, "not found", http.StatusNotFound)
			return
		}
		if err != nil {
			jsonErr(w, err.Error(), http.StatusInternalServerError)
			return
		}
		jsonOK(w, p, http.StatusOK)
	case http.MethodDelete:
		if err := db.DeleteProject(r.Context(), s.pool, id); errors.Is(err, db.ErrNotFound) {
			jsonErr(w, "not found", http.StatusNotFound)
			return
		} else if err != nil {
			jsonErr(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		jsonErr(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// ---- specs ----

func (s *Server) handleSpecs(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		var body struct {
			ProjectID int64           `json:"project_id"`
			Title     string          `json:"title"`
			Content   string          `json:"content"`
			Tags      json.RawMessage `json:"tags"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			jsonErr(w, err.Error(), http.StatusBadRequest)
			return
		}
		tags := []byte("[]")
		if body.Tags != nil {
			tags = body.Tags
		}
		sp, err := db.CreateSpec(r.Context(), s.pool, body.ProjectID, body.Title, body.Content, tags)
		if err != nil {
			jsonErr(w, err.Error(), http.StatusInternalServerError)
			return
		}
		jsonOK(w, sp, http.StatusCreated)
	case http.MethodGet:
		f := db.ListSpecsFilter{
			Status: r.URL.Query().Get("status"),
		}
		if pid := r.URL.Query().Get("project_id"); pid != "" {
			f.ProjectID, _ = strconv.ParseInt(pid, 10, 64)
		}
		list, err := db.ListSpecs(r.Context(), s.pool, f)
		if err != nil {
			jsonErr(w, err.Error(), http.StatusInternalServerError)
			return
		}
		jsonOK(w, list, http.StatusOK)
	default:
		jsonErr(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleSpec(w http.ResponseWriter, r *http.Request) {
	// routes under /api/specs/:id and /api/specs/:id/promote
	path := strings.TrimPrefix(r.URL.Path, "/api/specs/")
	parts := strings.SplitN(path, "/", 2)
	id, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		jsonErr(w, "invalid id", http.StatusBadRequest)
		return
	}
	suffix := ""
	if len(parts) == 2 {
		suffix = parts[1]
	}

	switch {
	case suffix == "workflows" && r.Method == http.MethodGet:
		wfs, err := db.ListWorkflowsBySpec(r.Context(), s.pool, id)
		if err != nil {
			jsonErr(w, err.Error(), http.StatusInternalServerError)
			return
		}
		jsonOK(w, wfs, http.StatusOK)
	case suffix == "promote" && r.Method == http.MethodPost:
		sp, err := db.GetSpec(r.Context(), s.pool, id)
		if errors.Is(err, db.ErrNotFound) {
			jsonErr(w, "not found", http.StatusNotFound)
			return
		}
		if err != nil {
			jsonErr(w, err.Error(), http.StatusInternalServerError)
			return
		}
		track := "polish"
		sp, err = db.UpdateSpec(r.Context(), s.pool, sp.ID, db.UpdateSpecParams{Track: &track})
		if err != nil {
			jsonErr(w, err.Error(), http.StatusInternalServerError)
			return
		}
		jsonOK(w, sp, http.StatusOK)
	case suffix == "" && r.Method == http.MethodGet:
		sp, err := db.GetSpec(r.Context(), s.pool, id)
		if errors.Is(err, db.ErrNotFound) {
			jsonErr(w, "not found", http.StatusNotFound)
			return
		}
		if err != nil {
			jsonErr(w, err.Error(), http.StatusInternalServerError)
			return
		}
		jsonOK(w, sp, http.StatusOK)
	case suffix == "" && r.Method == http.MethodPatch:
		var body struct {
			Title   *string         `json:"title"`
			Content *string         `json:"content"`
			Tags    json.RawMessage `json:"tags"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			jsonErr(w, err.Error(), http.StatusBadRequest)
			return
		}
		var tags []byte
		if body.Tags != nil {
			tags = body.Tags
		}
		sp, err := db.UpdateSpec(r.Context(), s.pool, id, db.UpdateSpecParams{
			Title:   body.Title,
			Content: body.Content,
			Tags:    tags,
		})
		if errors.Is(err, db.ErrNotFound) {
			jsonErr(w, "not found", http.StatusNotFound)
			return
		}
		if err != nil {
			jsonErr(w, err.Error(), http.StatusInternalServerError)
			return
		}
		jsonOK(w, sp, http.StatusOK)
	case suffix == "" && r.Method == http.MethodDelete:
		_, err := db.GetSpec(r.Context(), s.pool, id)
		if errors.Is(err, db.ErrNotFound) {
			jsonErr(w, "not found", http.StatusNotFound)
			return
		}
		if err != nil {
			jsonErr(w, err.Error(), http.StatusInternalServerError)
			return
		}
		err = db.DeleteSpec(r.Context(), s.pool, id)
		if err != nil {
			jsonErr(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		jsonErr(w, "not found", http.StatusNotFound)
	}
}

// ---- workflows ----

func (s *Server) handleWorkflows(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonErr(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		SpecID     int64    `json:"spec_id"`
		MaxCostUSD *float64 `json:"max_cost_usd"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonErr(w, err.Error(), http.StatusBadRequest)
		return
	}
	sp, err := db.GetSpec(r.Context(), s.pool, body.SpecID)
	if errors.Is(err, db.ErrNotFound) {
		jsonErr(w, "spec not found", http.StatusNotFound)
		return
	}
	if err != nil {
		jsonErr(w, err.Error(), http.StatusInternalServerError)
		return
	}
	maxCost := body.MaxCostUSD
	if maxCost == nil {
		def := s.defaultBudget
		maxCost = &def
	}
	wf, err := db.CreateWorkflow(r.Context(), s.pool, sp.ID, sp.Track, maxCost)
	if err != nil {
		jsonErr(w, err.Error(), http.StatusInternalServerError)
		return
	}
	// mark spec running
	runStatus := "running"
	_, _ = db.UpdateSpec(r.Context(), s.pool, sp.ID, db.UpdateSpecParams{Status: &runStatus})

	s.runner.Start(wf.ID)
	jsonOK(w, wf, http.StatusCreated)
}

func (s *Server) handleWorkflow(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/workflows/")
	parts := strings.SplitN(path, "/", 2)
	id, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		jsonErr(w, "invalid id", http.StatusBadRequest)
		return
	}
	suffix := ""
	if len(parts) == 2 {
		suffix = parts[1]
	}

	switch {
	case suffix == "" && r.Method == http.MethodGet:
		wf, err := db.GetWorkflow(r.Context(), s.pool, id)
		if errors.Is(err, db.ErrNotFound) {
			jsonErr(w, "not found", http.StatusNotFound)
			return
		}
		if err != nil {
			jsonErr(w, err.Error(), http.StatusInternalServerError)
			return
		}
		jsonOK(w, wf, http.StatusOK)
	case suffix == "" && r.Method == http.MethodDelete:
		s.runner.Stop(id)
		if err := db.DeleteWorkflow(r.Context(), s.pool, id); errors.Is(err, db.ErrNotFound) {
			jsonErr(w, "not found", http.StatusNotFound)
			return
		} else if err != nil {
			jsonErr(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	case suffix == "phases" && r.Method == http.MethodGet:
		phases, err := db.ListPhasesByWorkflow(r.Context(), s.pool, id)
		if err != nil {
			jsonErr(w, err.Error(), http.StatusInternalServerError)
			return
		}
		jsonOK(w, phases, http.StatusOK)
	case suffix == "resume" && r.Method == http.MethodPost:
		phases, err := db.ListPhasesByWorkflow(r.Context(), s.pool, id)
		if err != nil {
			jsonErr(w, err.Error(), http.StatusInternalServerError)
			return
		}
		for _, ph := range phases {
			if ph.Status == "failed" {
				pending := "pending"
				zero := 0
				_, _ = db.UpdatePhase(r.Context(), s.pool, ph.ID, db.UpdatePhaseParams{
					Status:     &pending,
					RetryCount: &zero,
				})
				break
			}
		}
		_ = db.UpdateWorkflowStatus(r.Context(), s.pool, id, "running")
		s.runner.Start(id)
		wf, _ := db.GetWorkflow(r.Context(), s.pool, id)
		jsonOK(w, wf, http.StatusOK)
	case suffix == "stop" && r.Method == http.MethodPost:
		s.runner.Stop(id)
		jsonOK(w, map[string]string{"status": "stopping"}, http.StatusOK)
	case suffix == "stream":
		s.streamWorkflow(w, r, id)
	default:
		jsonErr(w, "not found", http.StatusNotFound)
	}
}

// ---- phases ----

func (s *Server) handlePhase(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/phases/")
	parts := strings.SplitN(path, "/", 2)
	id, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		jsonErr(w, "invalid id", http.StatusBadRequest)
		return
	}
	suffix := ""
	if len(parts) == 2 {
		suffix = parts[1]
	}

	switch {
	case suffix == "" && r.Method == http.MethodGet:
		ph, err := db.GetPhase(r.Context(), s.pool, id)
		if errors.Is(err, db.ErrNotFound) {
			jsonErr(w, "not found", http.StatusNotFound)
			return
		}
		if err != nil {
			jsonErr(w, err.Error(), http.StatusInternalServerError)
			return
		}
		jsonOK(w, ph, http.StatusOK)
	case suffix == "logs" && r.Method == http.MethodGet:
		logs, err := db.ListPhaseLogs(r.Context(), s.pool, id)
		if err != nil {
			jsonErr(w, err.Error(), http.StatusInternalServerError)
			return
		}
		jsonOK(w, logs, http.StatusOK)
	case suffix == "logs/stream":
		s.streamLogs(w, r, id)
	case suffix == "diff" && r.Method == http.MethodGet:
		ph, err := db.GetPhase(r.Context(), s.pool, id)
		if errors.Is(err, db.ErrNotFound) {
			jsonErr(w, "not found", http.StatusNotFound)
			return
		}
		if err != nil {
			jsonErr(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if ph.CerberusSession == nil {
			jsonErr(w, "no cerberus session", http.StatusConflict)
			return
		}
		diff, err := s.cerb.Diff(r.Context(), *ph.CerberusSession)
		if err != nil {
			jsonErr(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprint(w, diff)
	case suffix == "approve" && r.Method == http.MethodPost:
		done := "done"
		pass := "pass"
		now := time.Now()
		_, err := db.UpdatePhase(r.Context(), s.pool, id, db.UpdatePhaseParams{
			Status:        &done,
			ReviewVerdict: &pass,
			FinishedAt:    &now,
		})
		if errors.Is(err, db.ErrNotFound) {
			jsonErr(w, "not found", http.StatusNotFound)
			return
		}
		if err != nil {
			jsonErr(w, err.Error(), http.StatusInternalServerError)
			return
		}
		ph, _ := db.GetPhase(r.Context(), s.pool, id)
		jsonOK(w, ph, http.StatusOK)
	case suffix == "reject" && r.Method == http.MethodPost:
		failed := "failed"
		fail := "fail"
		now := time.Now()
		_, err := db.UpdatePhase(r.Context(), s.pool, id, db.UpdatePhaseParams{
			Status:        &failed,
			ReviewVerdict: &fail,
			FinishedAt:    &now,
		})
		if errors.Is(err, db.ErrNotFound) {
			jsonErr(w, "not found", http.StatusNotFound)
			return
		}
		if err != nil {
			jsonErr(w, err.Error(), http.StatusInternalServerError)
			return
		}
		ph, _ := db.GetPhase(r.Context(), s.pool, id)
		jsonOK(w, ph, http.StatusOK)
	case suffix == "clean" && r.Method == http.MethodPost:
		ph, err := db.GetPhase(r.Context(), s.pool, id)
		if errors.Is(err, db.ErrNotFound) {
			jsonErr(w, "not found", http.StatusNotFound)
			return
		}
		if err != nil {
			jsonErr(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if ph.CerberusSession != nil {
			if err := s.cerb.Clean(r.Context(), *ph.CerberusSession); err != nil {
				jsonErr(w, err.Error(), http.StatusInternalServerError)
				return
			}
			removeProfileFile(*ph.CerberusSession)
		}
		jsonOK(w, map[string]string{"status": "cleaned"}, http.StatusOK)
	default:
		jsonErr(w, "not found", http.StatusNotFound)
	}
}

func (s *Server) streamLogs(w http.ResponseWriter, r *http.Request, phaseID int64) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		jsonErr(w, "streaming not supported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	var lastID int64
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			logs, err := db.StreamPhaseLogs(r.Context(), s.pool, phaseID, lastID)
			if err != nil {
				return
			}
			for _, l := range logs {
				data, _ := json.Marshal(l)
				fmt.Fprintf(w, "data: %s\n\n", data)
				lastID = l.ID
			}
			flusher.Flush()
			// stop streaming if phase is terminal
			ph, err := db.GetPhase(r.Context(), s.pool, phaseID)
			if err != nil {
				return
			}
			if ph.Status == "done" || ph.Status == "failed" {
				fmt.Fprintf(w, "event: done\ndata: {}\n\n")
				flusher.Flush()
				return
			}
		}
	}
}

func (s *Server) writeWorkflowSnapshot(ctx context.Context, w io.Writer, workflowID int64) bool {
	wf, err := db.GetWorkflow(ctx, s.pool, workflowID)
	if err != nil {
		log.Printf("workflow snapshot: get workflow %d: %v", workflowID, err)
		return false
	}
	phases, err := db.ListPhasesByWorkflow(ctx, s.pool, workflowID)
	if err != nil {
		log.Printf("workflow snapshot: list phases for workflow %d: %v", workflowID, err)
		return false
	}
	data, _ := json.Marshal(map[string]any{
		"event":    "snapshot",
		"workflow": wf,
		"phases":   phases,
	})
	fmt.Fprintf(w, "event: snapshot\ndata: %s\n\n", data)
	return true
}

func (s *Server) streamWorkflow(w http.ResponseWriter, r *http.Request, workflowID int64) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		jsonErr(w, "streaming not supported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	key := fmt.Sprintf("wf:%d", workflowID)
	ch := s.eventHub.Subscribe(key)
	defer s.eventHub.Unsubscribe(key, ch)

	// Send a database-backed snapshot first. If the browser reconnects after
	// dropped high-volume live events, this catches it up to durable state.
	if !s.writeWorkflowSnapshot(r.Context(), w, workflowID) {
		return
	}
	flusher.Flush()

	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-heartbeat.C:
			fmt.Fprintf(w, "event: heartbeat\ndata: {}\n\n")
			flusher.Flush()
		case data, ok := <-ch:
			if !ok {
				return
			}
			var evt struct {
				Event string `json:"event"`
			}
			if json.Unmarshal(data, &evt) == nil && evt.Event != "" {
				fmt.Fprintf(w, "event: %s\ndata: %s\n\n", evt.Event, data)
			} else {
				fmt.Fprintf(w, "data: %s\n\n", data)
			}
			flusher.Flush()
		}
	}
}

// helpers

func jsonOK(w http.ResponseWriter, v any, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func jsonErr(w http.ResponseWriter, msg string, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func pathID(path, prefix string) (int64, error) {
	s := strings.TrimPrefix(path, prefix)
	s = strings.TrimSuffix(s, "/")
	return strconv.ParseInt(s, 10, 64)
}

func parseUIID(path, prefix string) (id int64, fragment bool, ok bool) {
	id, suffix, ok := parseUIIDSuffix(path, prefix)
	return id, suffix == "fragment", ok && (suffix == "" || suffix == "fragment")
}

func parseUIIDSuffix(path, prefix string) (int64, string, bool) {
	if !strings.HasPrefix(path, prefix) {
		return 0, "", false
	}
	rest := strings.Trim(strings.TrimPrefix(path, prefix), "/")
	if rest == "" {
		return 0, "", false
	}
	parts := strings.SplitN(rest, "/", 2)
	id, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return 0, "", false
	}
	if len(parts) == 1 {
		return id, "", true
	}
	return id, parts[1], true
}

// ---- spec-drafts ----

const specBuilderPrompt = `You are a spec writer for Foundry, a spec-driven development loop that runs AI agents.

Your job: help the user write a Foundry spec — a markdown document that defines what should be built and how it should be broken into phases for an AI agent to execute.

## Intent context

Before drafting or materially updating a spec, read the key intent files in the project repository when they exist. Use file path references only; do not inline wiki contents into the prompt or generated spec.

Default intent files to inspect:
- intent/README.md
- intent/Product Model.md
- intent/Principles.md
- intent/Constraints.md
- intent/Open Questions.md
- relevant linked pages under intent/ when the request or those files point to them

Generated specs should link back to durable intent using Obsidian-style links where relevant, for example:

Related intent: [[Product Model]], [[Principles]], [[Constraints]]

Choose only relevant intent links. Do not invent pages unless the spec truly introduces a durable concept that belongs in intent. If intent files are missing, continue without failing and do not paste placeholder wiki content.

## Spec format

A spec is markdown with this structure:

# Feature title

Related intent: [[Product Model]], [[Principles]], [[Constraints]]

Global context — background, constraints, anything the agent needs to know.
This is prepended to every phase prompt automatically.

## Phase 1: Name
What this phase should accomplish. This becomes the exact prompt body sent to the agent.
Be specific: what files to create/edit, what the output should be, how to verify it works.

## Phase 2: Name
...

Rules:
- Sections starting with ## Phase N: become executable phases (N must be sequential integers starting at 1)
- Everything before the first phase = global context (shared across all phases)
- Each phase goal should be independently executable by an AI agent in a fresh container
- Phases should be small enough that one agent can complete them in a single session
- Prefer explicit over clever — spell out what files to touch, what functions to write

## Good example

# User authentication

Stack: Go + pgx + stdlib net/http. No frameworks, no ORMs.
Project already has: users table (id, email, password_hash, created_at).

## Phase 1: Password hashing utilities
Create internal/auth/hash.go with HashPassword(plain string) (string, error) using bcrypt cost 12, and CheckPassword(plain, hash string) bool. Add internal/auth/hash_test.go covering both. No external deps beyond golang.org/x/crypto.

## Phase 2: Login endpoint
Add POST /api/login to internal/api/handlers.go. Accept {email, password} JSON. Return {token} on success, 401 on failure.

## Phase 3: Auth middleware
Add AuthMiddleware(next http.Handler) http.Handler in internal/api/middleware.go. Reads Authorization: Bearer <token>, validates JWT, sets user_id in context.

Whenever you produce or update the spec, call the update_spec tool with the full markdown content. Do not write the spec in plain text or in code blocks — always use the update_spec tool. Call it after every meaningful change to the spec, not just at the end.`

func (s *Server) handleSpecDrafts(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		list, err := db.ListSpecDrafts(r.Context(), s.pool)
		if err != nil {
			jsonErr(w, err.Error(), http.StatusInternalServerError)
			return
		}
		jsonOK(w, list, http.StatusOK)
	case http.MethodPost:
		var body struct {
			ProjectID   *int64 `json:"project_id"`
			Description string `json:"description"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			jsonErr(w, err.Error(), http.StatusBadRequest)
			return
		}
		draft, err := db.CreateSpecDraft(r.Context(), s.pool, body.ProjectID, "(untitled)")
		if err != nil {
			jsonErr(w, err.Error(), http.StatusInternalServerError)
			return
		}
		session := cerberus.DraftSessionName(draft.ID)
		if _, err := db.UpdateSpecDraft(r.Context(), s.pool, draft.ID, db.UpdateSpecDraftParams{CerberusSession: &session}); err != nil {
			jsonErr(w, err.Error(), http.StatusInternalServerError)
			return
		}
		draft.CerberusSession = session

		initialPrompt := specBuilderPrompt
		if body.Description != "" {
			initialPrompt += "\n\nThe user's request:\n" + body.Description
		}
		if body.ProjectID != nil {
			if proj, err := db.GetProject(r.Context(), s.pool, *body.ProjectID); err == nil {
				initialPrompt += "\n\nProject name: " + proj.Name + "\nThe project code is mounted at /workspace inside your container."
			}
		}

		pool := s.pool
		cerb := s.cerb
		draftID := draft.ID
		cbURL := s.callbackURL()
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
			defer cancel()
			profilePath, profileErr := s.writeProfileFile(ctx, session)
			if profileErr != nil {
				log.Printf("spec-builder: write profile file: %v (proceeding without profile)", profileErr)
			}
			if profilePath != "" {
				cerb.SetProfile(profilePath)
			}
			if err := cerb.Chat(ctx, session, initialPrompt, cbURL); err != nil {
				log.Printf("spec-builder chat start error: %v", err)
				errStatus := "error"
				db.UpdateSpecDraft(ctx, pool, draftID, db.UpdateSpecDraftParams{Status: &errStatus})
				return
			}
		}()

		jsonOK(w, draft, http.StatusCreated)
	default:
		jsonErr(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleSpecDraft(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/spec-drafts/")
	parts := strings.SplitN(path, "/", 2)
	id, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		jsonErr(w, "invalid id", http.StatusBadRequest)
		return
	}
	suffix := ""
	if len(parts) == 2 {
		suffix = parts[1]
	}

	switch {
	case suffix == "stream":
		s.streamDraftEvents(w, r, id)
		return

	case suffix == "messages" && r.Method == http.MethodGet:
		draft, err := db.GetSpecDraft(r.Context(), s.pool, id)
		if errors.Is(err, db.ErrNotFound) {
			jsonErr(w, "not found", http.StatusNotFound)
			return
		}
		if err != nil {
			jsonErr(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(draft.Messages)

	case suffix == "" && r.Method == http.MethodGet:
		draft, err := db.GetSpecDraft(r.Context(), s.pool, id)
		if errors.Is(err, db.ErrNotFound) {
			jsonErr(w, "not found", http.StatusNotFound)
			return
		}
		if err != nil {
			jsonErr(w, err.Error(), http.StatusInternalServerError)
			return
		}
		jsonOK(w, draft, http.StatusOK)

	case suffix == "message" && r.Method == http.MethodPost:
		var body struct {
			Content string `json:"content"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			jsonErr(w, err.Error(), http.StatusBadRequest)
			return
		}
		draft, err := db.GetSpecDraft(r.Context(), s.pool, id)
		if errors.Is(err, db.ErrNotFound) {
			jsonErr(w, "not found", http.StatusNotFound)
			return
		}
		if err != nil {
			jsonErr(w, err.Error(), http.StatusInternalServerError)
			return
		}
		msgs := appendMessage(draft.Messages, "user", body.Content)
		draft, err = db.UpdateSpecDraft(r.Context(), s.pool, id, db.UpdateSpecDraftParams{Messages: msgs})
		if err != nil {
			jsonErr(w, err.Error(), http.StatusInternalServerError)
			return
		}
		cbURL := s.callbackURL()
		cerb := s.cerb
		session := draft.CerberusSession
		pool := s.pool
		draftID := draft.ID
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
			defer cancel()
			if err := cerb.Message(ctx, session, body.Content, cbURL); err != nil {
				log.Printf("spec-builder message error: %v", err)
				errStatus := "error"
				db.UpdateSpecDraft(ctx, pool, draftID, db.UpdateSpecDraftParams{Status: &errStatus})
			}
		}()
		jsonOK(w, draft, http.StatusOK)

	case suffix == "save" && r.Method == http.MethodPost:
		var saveBody struct {
			Title string `json:"title"`
		}
		if err := json.NewDecoder(r.Body).Decode(&saveBody); err != nil {
			jsonErr(w, err.Error(), http.StatusBadRequest)
			return
		}
		draft, err := db.GetSpecDraft(r.Context(), s.pool, id)
		if errors.Is(err, db.ErrNotFound) {
			jsonErr(w, "not found", http.StatusNotFound)
			return
		}
		if err != nil {
			jsonErr(w, err.Error(), http.StatusInternalServerError)
			return
		}
		specContent := extractFinalSpec(draft.Messages)
		if specContent == "" {
			jsonErr(w, "could not extract spec from conversation — ask the agent to update the spec with full spec content", http.StatusUnprocessableEntity)
			return
		}
		if err := s.cerb.Close(r.Context(), draft.CerberusSession); err != nil {
			log.Printf("spec-builder close error: %v", err)
		}
		if err := s.cerb.Clean(r.Context(), draft.CerberusSession); err != nil {
			log.Printf("spec-builder clean error: %v", err)
		}
		db.DeleteCerberusEvents(r.Context(), s.pool, draft.CerberusSession)
		removeProfileFile(draft.CerberusSession)
		var projID int64
		if draft.ProjectID != nil {
			projID = *draft.ProjectID
		}
		title := saveBody.Title
		if title == "" {
			title = extractSpecTitle(specContent)
		}
		if title == "" {
			title = draft.Title
		}
		sp, err := db.CreateSpec(r.Context(), s.pool, projID, title, specContent, []byte("[]"))
		if err != nil {
			jsonErr(w, err.Error(), http.StatusInternalServerError)
			return
		}
		saved := "saved"
		db.UpdateSpecDraft(r.Context(), s.pool, id, db.UpdateSpecDraftParams{Status: &saved, Title: &title})
		jsonOK(w, map[string]int64{"spec_id": sp.ID}, http.StatusCreated)

	case suffix == "" && r.Method == http.MethodDelete:
		draft, err := db.GetSpecDraft(r.Context(), s.pool, id)
		if errors.Is(err, db.ErrNotFound) {
			jsonErr(w, "not found", http.StatusNotFound)
			return
		}
		if err != nil {
			jsonErr(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if draft.CerberusSession != "" {
			if err := s.cerb.Close(r.Context(), draft.CerberusSession); err != nil {
				log.Printf("spec-builder close on delete: %v", err)
			}
			if err := s.cerb.Clean(r.Context(), draft.CerberusSession); err != nil {
				log.Printf("spec-builder clean on delete: %v", err)
			}
			db.DeleteCerberusEvents(r.Context(), s.pool, draft.CerberusSession)
			removeProfileFile(draft.CerberusSession)
		}
		if err := db.DeleteSpecDraft(r.Context(), s.pool, id); err != nil {
			jsonErr(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)

	default:
		jsonErr(w, "not found", http.StatusNotFound)
	}
}

func extractFinalSpec(messages []byte) string {
	type msg struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	var msgs []msg
	if err := json.Unmarshal(messages, &msgs); err != nil {
		return ""
	}
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role != "assistant" {
			continue
		}
		content := strings.TrimSpace(msgs[i].Content)
		if strings.HasPrefix(content, "# ") && strings.Contains(content, "\n## Phase 1:") {
			return content
		}
	}
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role != "assistant" {
			continue
		}
		content := msgs[i].Content
		idx := strings.Index(content, "FINAL SPEC:")
		if idx == -1 {
			continue
		}
		after := content[idx+len("FINAL SPEC:"):]
		start := strings.Index(after, "```")
		if start == -1 {
			return strings.TrimSpace(after)
		}
		after = after[start+3:]
		if strings.HasPrefix(after, "markdown") {
			after = after[8:]
		}
		end := strings.Index(after, "```")
		if end == -1 {
			return strings.TrimSpace(after)
		}
		return strings.TrimSpace(after[:end])
	}
	return ""
}

func extractSpecTitle(specContent string) string {
	for _, line := range strings.Split(specContent, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "# ") && !strings.HasPrefix(line, "## ") {
			return strings.TrimSpace(line[2:])
		}
	}
	return ""
}

func appendMessage(existing []byte, role, content string) []byte {
	type msg struct {
		Role    string `json:"role"`
		Content string `json:"content"`
		Ts      string `json:"ts"`
	}
	var msgs []msg
	if len(existing) > 0 {
		_ = json.Unmarshal(existing, &msgs)
	}
	msgs = append(msgs, msg{Role: role, Content: content, Ts: time.Now().Format(time.RFC3339)})
	b, _ := json.Marshal(msgs)
	return b
}

// ---- cerberus callback ----

func (s *Server) handleCerberusCallback(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonErr(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		jsonErr(w, "read body: "+err.Error(), http.StatusBadRequest)
		return
	}
	if err := s.handleCompactCerberusEvent(r.Context(), raw); err != nil {
		code := http.StatusInternalServerError
		if strings.HasPrefix(err.Error(), "invalid json") || strings.Contains(err.Error(), "session and type required") {
			code = http.StatusBadRequest
		}
		jsonErr(w, err.Error(), code)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) assembleAndAppend(ctx context.Context, session string, isTurnComplete bool) {
	if !isTurnComplete {
		return
	}

	drafts, _ := db.ListSpecDrafts(ctx, s.pool)
	var draft *db.SpecDraft
	for _, d := range drafts {
		if d.CerberusSession == session {
			draft = &d
			break
		}
	}
	if draft == nil {
		return
	}

	events, err := db.ListCerberusEvents(ctx, s.pool, session, 0)
	if err != nil {
		log.Printf("assemble messages: %v", err)
		return
	}

	var buf strings.Builder
	var assistantMsgs []string
	for _, e := range events {
		switch e.EventType {
		case "text_delta":
			var p struct {
				Content string `json:"content"`
			}
			json.Unmarshal(e.Payload, &p)
			buf.WriteString(p.Content)
		case "message_end":
			if buf.Len() > 0 {
				assistantMsgs = append(assistantMsgs, buf.String())
				buf.Reset()
			}
		case "tool_use":
			var p struct {
				ToolName  string `json:"tool_name"`
				ToolInput string `json:"tool_input"`
			}
			json.Unmarshal(e.Payload, &p)
			if p.ToolName == "update_spec" {
				var toolInput struct {
					Content string `json:"content"`
				}
				if err := json.Unmarshal([]byte(p.ToolInput), &toolInput); err == nil {
					assistantMsgs = append(assistantMsgs, toolInput.Content)
				}
			}
		}
	}
	if buf.Len() > 0 {
		assistantMsgs = append(assistantMsgs, buf.String())
	}

	msgs := draft.Messages
	for _, content := range assistantMsgs {
		msgs = appendMessage(msgs, "assistant", content)
	}
	if len(assistantMsgs) > 0 {
		db.UpdateSpecDraft(ctx, s.pool, draft.ID, db.UpdateSpecDraftParams{Messages: msgs})
	}
	db.DeleteCerberusEvents(ctx, s.pool, session)
}

func (s *Server) streamDraftEvents(w http.ResponseWriter, r *http.Request, draftID int64) {
	draft, err := db.GetSpecDraft(r.Context(), s.pool, draftID)
	if errors.Is(err, db.ErrNotFound) {
		jsonErr(w, "not found", http.StatusNotFound)
		return
	}
	if err != nil {
		jsonErr(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if draft.CerberusSession == "" {
		jsonErr(w, "no session", http.StatusConflict)
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		jsonErr(w, "streaming not supported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	lastIDStr := r.URL.Query().Get("after")
	if lastIDStr == "" {
		lastIDStr = r.Header.Get("Last-Event-ID")
	}
	var lastID int64
	if lastIDStr != "" {
		lastID, _ = strconv.ParseInt(lastIDStr, 10, 64)
	}

	catchUp, _ := db.ListCerberusEvents(r.Context(), s.pool, draft.CerberusSession, lastID)
	for _, e := range catchUp {
		writeSSEvent(w, e)
		lastID = e.ID
	}
	flusher.Flush()

	ch := s.eventHub.Subscribe(draft.CerberusSession)
	defer s.eventHub.Unsubscribe(draft.CerberusSession, ch)

	for {
		select {
		case <-r.Context().Done():
			return
		case data, ok := <-ch:
			if !ok {
				return
			}
			var e db.CerberusEvent
			if json.Unmarshal(data, &e) == nil {
				writeSSEvent(w, e)
			} else {
				fmt.Fprintf(w, "data: %s\n\n", data)
			}
			flusher.Flush()
		}
	}
}

func writeSSEvent(w http.ResponseWriter, e db.CerberusEvent) {
	fmt.Fprintf(w, "id: %d\n", e.ID)
	fmt.Fprintf(w, "event: %s\n", e.EventType)
	fmt.Fprintf(w, "data: %s\n\n", e.Payload)
}

// ---- settings ----

func (s *Server) handleSettings(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		data, err := os.ReadFile(s.cfgPath)
		if err != nil {
			jsonErr(w, "cannot read config: "+err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/x-yaml")
		w.Write(data)
	case http.MethodPatch:
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			jsonErr(w, err.Error(), http.StatusBadRequest)
			return
		}
		// read current yaml as text, update matching key: value lines
		data, err := os.ReadFile(s.cfgPath)
		if err != nil {
			jsonErr(w, "cannot read config: "+err.Error(), http.StatusInternalServerError)
			return
		}
		updated := applyYAMLPatch(string(data), body)
		if err := os.WriteFile(s.cfgPath, []byte(updated), 0644); err != nil {
			jsonErr(w, "cannot write config: "+err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/x-yaml")
		w.Write([]byte(updated))
	default:
		jsonErr(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// applyYAMLPatch does a naive line-by-line replacement of top-level YAML keys.
func applyYAMLPatch(yaml string, patch map[string]any) string {
	lines := strings.Split(yaml, "\n")
	replaced := map[string]bool{}
	for i, line := range lines {
		for k, v := range patch {
			prefix := k + ":"
			if strings.HasPrefix(strings.TrimSpace(line), prefix) {
				lines[i] = fmt.Sprintf("%s: %s", k, yamlValue(v))
				replaced[k] = true
			}
		}
	}
	for k, v := range patch {
		if !replaced[k] {
			lines = append(lines, fmt.Sprintf("%s: %s", k, yamlValue(v)))
		}
	}
	return strings.Join(lines, "\n")
}

// yamlValue formats a value for a YAML line.
// Strings get quoted; numbers and booleans are written bare.
func yamlValue(v any) string {
	s := fmt.Sprint(v)
	// if it parses as a number or bool, write bare
	if _, err := strconv.ParseFloat(s, 64); err == nil {
		return s
	}
	if _, err := strconv.ParseBool(s); err == nil {
		return s
	}
	return fmt.Sprintf("%q", s)
}

// ---- profiles ----

// profileFilePath returns the fixed path for a session's profile file.
func profileFilePath(session string) string {
	return "/tmp/foundry-profile-" + session + ".json"
}

// writeProfileFile looks up the server's active profile from the DB and writes it to a
// fixed path derived from the session name. Returns empty string (no error) when no
// profile is configured or the profile is not found. The file persists until
// removeProfileFile is called at session cleanup.
func (s *Server) writeProfileFile(ctx context.Context, session string) (string, error) {
	if s.cerberusProfile == "" {
		return "", nil
	}
	p, err := db.GetProfileByName(ctx, s.pool, s.cerberusProfile)
	if err == db.ErrNotFound {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("lookup profile %q: %w", s.cerberusProfile, err)
	}
	payload := map[string]any{}
	if p.DefaultModel != "" {
		payload["default_model"] = p.DefaultModel
	}
	if p.DefaultImage != "" {
		payload["default_image"] = p.DefaultImage
	}
	if p.AWSProfile != "" {
		payload["aws_profile"] = p.AWSProfile
	}
	if p.AWSRegion != "" {
		payload["aws_region"] = p.AWSRegion
	}
	if len(p.ExtraEnv) > 0 {
		payload["extra_env"] = p.ExtraEnv
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal profile: %w", err)
	}
	path := profileFilePath(session)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return "", fmt.Errorf("write profile file: %w", err)
	}
	return path, nil
}

// removeProfileFile deletes the profile file for a session if it exists.
func removeProfileFile(session string) {
	os.Remove(profileFilePath(session))
}

func (s *Server) handleProfiles(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		profiles, err := db.ListProfiles(r.Context(), s.pool)
		if err != nil {
			jsonErr(w, "list profiles: "+err.Error(), http.StatusInternalServerError)
			return
		}
		if profiles == nil {
			profiles = []db.Profile{}
		}
		jsonOK(w, profiles, http.StatusOK)

	case http.MethodPost:
		var body struct {
			Name         string            `json:"name"`
			DefaultModel string            `json:"default_model"`
			DefaultImage string            `json:"default_image"`
			AWSProfile   string            `json:"aws_profile"`
			AWSRegion    string            `json:"aws_region"`
			ExtraEnv     map[string]string `json:"extra_env"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			jsonErr(w, err.Error(), http.StatusBadRequest)
			return
		}
		if body.Name == "" {
			jsonErr(w, "name is required", http.StatusBadRequest)
			return
		}
		if body.ExtraEnv == nil {
			body.ExtraEnv = map[string]string{}
		}
		p, err := db.CreateProfile(r.Context(), s.pool, body.Name, body.DefaultModel, body.DefaultImage, body.AWSProfile, body.AWSRegion, body.ExtraEnv)
		if err != nil {
			jsonErr(w, "create profile: "+err.Error(), http.StatusInternalServerError)
			return
		}
		jsonOK(w, p, http.StatusCreated)

	default:
		jsonErr(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleProfile(w http.ResponseWriter, r *http.Request) {
	idStr := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/profiles/"), "/")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		jsonErr(w, "invalid id", http.StatusBadRequest)
		return
	}
	switch r.Method {
	case http.MethodGet:
		p, err := db.GetProfile(r.Context(), s.pool, id)
		if errors.Is(err, db.ErrNotFound) {
			jsonErr(w, "not found", http.StatusNotFound)
			return
		}
		if err != nil {
			jsonErr(w, "get profile: "+err.Error(), http.StatusInternalServerError)
			return
		}
		jsonOK(w, p, http.StatusOK)
	case http.MethodPatch:
		var body struct {
			Name         *string           `json:"name"`
			DefaultModel *string           `json:"default_model"`
			DefaultImage *string           `json:"default_image"`
			AWSProfile   *string           `json:"aws_profile"`
			AWSRegion    *string           `json:"aws_region"`
			ExtraEnv     map[string]string `json:"extra_env"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			jsonErr(w, err.Error(), http.StatusBadRequest)
			return
		}
		if body.Name != nil && *body.Name == "" {
			jsonErr(w, "name is required", http.StatusBadRequest)
			return
		}
		p, err := db.UpdateProfile(r.Context(), s.pool, id, db.UpdateProfileParams{
			Name: body.Name, DefaultModel: body.DefaultModel, DefaultImage: body.DefaultImage,
			AWSProfile: body.AWSProfile, AWSRegion: body.AWSRegion, ExtraEnv: body.ExtraEnv,
		})
		if errors.Is(err, db.ErrNotFound) {
			jsonErr(w, "not found", http.StatusNotFound)
			return
		}
		if err != nil {
			jsonErr(w, "update profile: "+err.Error(), http.StatusInternalServerError)
			return
		}
		jsonOK(w, p, http.StatusOK)
	case http.MethodDelete:
		if err := db.DeleteProfile(r.Context(), s.pool, id); errors.Is(err, db.ErrNotFound) {
			jsonErr(w, "not found", http.StatusNotFound)
			return
		} else if err != nil {
			jsonErr(w, "delete profile: "+err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		jsonErr(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func RecoverOrphanDrafts(ctx context.Context, pool *pgxpool.Pool, cerb *cerberus.Client) {
	drafts, err := db.ListSpecDrafts(ctx, pool)
	if err != nil {
		log.Printf("orphan recovery: list drafts: %v", err)
		return
	}
	errStatus := "error"
	for _, d := range drafts {
		if d.Status != "active" {
			continue
		}
		if d.CerberusSession == "" {
			db.UpdateSpecDraft(ctx, pool, d.ID, db.UpdateSpecDraftParams{Status: &errStatus})
			continue
		}
		status, err := cerb.Status(ctx, d.CerberusSession)
		if err != nil || strings.Contains(status, "not found") || strings.Contains(status, "done") || strings.Contains(status, "failed") {
			log.Printf("orphan recovery: marking draft %d as error (status=%q err=%v)", d.ID, status, err)
			db.UpdateSpecDraft(ctx, pool, d.ID, db.UpdateSpecDraftParams{Status: &errStatus})
			continue
		}
		// session is alive (waiting) — leave it alone, user can resume from the UI
		if strings.Contains(status, "waiting") {
			log.Printf("orphan recovery: draft %d session %s is alive and waiting — keeping active", d.ID, d.CerberusSession)
		}
	}
}
