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
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/tonis2/foundry/internal/cerberus"
	"github.com/tonis2/foundry/internal/db"
	"github.com/tonis2/foundry/internal/discover"
	"github.com/tonis2/foundry/internal/hub"
	"github.com/tonis2/foundry/internal/memory"
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
	memoryRepoPath  string
	cfgPath         string
	serverPort      int
	cerberusProfile string
	cerbEventsMu    sync.Mutex
	cerbBuffers     map[string]*cerberusTextBuffer
}

func NewServer(pool *pgxpool.Pool, runner *workflow.Runner, cerb *cerberus.Client, eventHub *hub.EventHub, defaultBudget float64, gitRoot string, memoryRepoPath string, cfgPath string, cerberusProfile string, serverPort int) *Server {
	s := &Server{pool: pool, runner: runner, cerb: cerb, eventHub: eventHub, defaultBudget: defaultBudget, gitRoot: gitRoot, memoryRepoPath: memoryRepoPath, cfgPath: cfgPath, serverPort: serverPort, cerberusProfile: cerberusProfile, cerbBuffers: make(map[string]*cerberusTextBuffer)}
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
	s.mux.HandleFunc("/backlog/projects", s.handleUIBacklogCreateProject)
	s.mux.HandleFunc("/backlog/specs", s.handleUIBacklogCreateSpec)
	s.mux.HandleFunc("/backlog/workflows", s.handleUIBacklogCreateWorkflow)
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

	s.mux.HandleFunc("/api/export", s.handleExport)
	s.mux.HandleFunc("/api/projects", s.handleProjects)
	s.mux.HandleFunc("/api/projects/discover", s.handleDiscover)
	s.mux.HandleFunc("/api/projects/", s.handleProject)

	s.mux.HandleFunc("/api/specs", s.handleSpecs)
	s.mux.HandleFunc("/api/specs/", s.handleSpec)

	s.mux.HandleFunc("/api/workflows", s.handleWorkflows)
	s.mux.HandleFunc("/api/workflows/", s.handleWorkflow)
	s.mux.HandleFunc("/api/memory-updates/", s.handleMemoryUpdate)

	s.mux.HandleFunc("/api/phases/", s.handlePhase)
	s.mux.HandleFunc("/api/settings", s.handleSettings)
	s.mux.HandleFunc("/api/profiles", s.handleProfiles)
	s.mux.HandleFunc("/api/profiles/", s.handleProfile)
	s.mux.HandleFunc("/api/cerberus/sessions", s.handleCerberusSessions)
	s.mux.HandleFunc("/api/cerberus/sessions/", s.handleCerberusSession)
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
	"cleanSessionURL": func(session string) string {
		return "/api/cerberus/sessions/" + session + "/clean"
	},
	"phaseProgress":    phaseProgress,
	"phaseFillClass":   phaseFillClass,
	"phaseStatusLabel": phaseStatusLabel,
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
  <a class="skip-link" href="#app">Skip to content</a>
  <header class="top-frame">
    <div class="brand-slab">
      <a href="/" hx-get="/backlog/fragment" hx-target="#app" hx-push-url="/" class="brand">Foundry</a>
      <span class="brand-subtitle">intent / evidence / decisions</span>
    </div>
    <nav class="nav-slabs" aria-label="Primary navigation">
      <a href="/backlog" data-nav="backlog" hx-get="/backlog/fragment" hx-target="#app" hx-push-url="/backlog">Foundry / backlog</a>
      <a href="/projects" data-nav="projects" hx-get="/projects/fragment" hx-target="#app" hx-push-url="/projects">Projects</a>
      <a href="/spec-builder" data-nav="builder" hx-get="/spec-builder/fragment" hx-target="#app" hx-push-url="/spec-builder">Spec Builder</a>
      <a href="/settings" data-nav="settings" hx-get="/settings/fragment" hx-target="#app" hx-push-url="/settings">Settings</a>
    </nav>
  </header>
  <div id="action-errors" class="error-region" role="alert" aria-live="assertive" hidden></div>
  <main id="app" tabindex="-1" hx-get="{{.Fragment}}" hx-trigger="load" hx-swap="innerHTML"></main>
</body>
</html>
{{end}}

{{define "backlog"}}
<div data-page="backlog">
  <div class="page-header command-header">
    <div>
      <p class="eyebrow">Foundry / backlog</p>
      <h2>Executable intent queue</h2>
      <p class="hint">Scan status, ownership, and the next safe action without opening each spec.</p>
    </div>
    <div class="card-actions">
      <a class="btn btn-primary" href="/spec-builder" hx-get="/spec-builder/fragment" hx-target="#app" hx-push-url="/spec-builder">Build with AI</a>
      <button class="btn btn-primary" popovertarget="new-spec">+ Spec</button>
      <button class="btn" popovertarget="new-project">+ Project</button>
    </div>
  </div>
  <div id="new-project" class="popover-card" popover>
    <h3>New Project</h3>
    <p class="hint">Register the target repository and its approved-memory namespace.</p>
    <form method="post" action="/backlog/projects" hx-post="/backlog/projects" hx-target="#app" hx-swap="innerHTML">
      <div class="field"><label>Name</label><input name="name" required></div>
      <div class="field"><label>Repo path</label><input name="repo_path" required placeholder="/workspace or /home/me/src/app"></div>
      <div class="field"><label>Memory namespace</label><input name="memory_namespace" placeholder="Defaults to project name"><p class="hint">Leave blank to use the project name.</p></div>
      <button class="btn btn-primary">Create project</button>
    </form>
  </div>
  <div id="new-spec" class="popover-card" popover>
    <h3>New Spec</h3>
    <p class="hint">Specs become runnable when they include <code>## Phase N:</code> sections.</p>
    <form method="post" action="/backlog/specs" hx-post="/backlog/specs" hx-target="#app" hx-swap="innerHTML">
      <div class="field"><label>Title</label><input name="title" required></div>
      <div class="field"><label>Project</label><select name="project_id" required>{{range .Projects}}<option value="{{.ID}}">{{.Name}}</option>{{end}}</select></div>
      <div class="field"><label>Content</label><textarea name="content" required># Feature title

Global context here.

## Phase 1: Bootstrap

What this phase does.</textarea></div>
      <button class="btn btn-primary">Create spec</button>
    </form>
  </div>
  {{if .HasSpecs}}
    <div class="worklist" aria-label="Specs grouped by status">
    {{range .Groups}}
      {{if .Items}}
        <div class="group-label">{{.Label}}</div>
        {{range .Items}}
          <article class="work-row work-row-{{.Status}}">
            <div class="work-main">
              <a class="work-title" href="/specs/{{.ID}}" hx-get="/specs/{{.ID}}/fragment" hx-target="#app" hx-push-url="/specs/{{.ID}}">{{.Title}}</a>
              <div class="work-meta"><strong>{{.ProjectName}}</strong> · created {{date .CreatedAt}} · last {{date .UpdatedAt}}</div>
            </div>
            <div class="work-signals"><span class="chip chip-{{.Status}}">{{.Status}}</span><span class="chip chip-{{.Track}}">{{.Track}}</span></div>
            <div class="work-next"><form method="post" action="/backlog/workflows" hx-post="/backlog/workflows"><input type="hidden" name="spec_id" value="{{.ID}}"><button class="btn btn-primary">Run workflow</button></form></div>
          </article>
        {{end}}
      {{end}}
    {{end}}
    </div>
  {{else}}<div class="empty empty-action">No specs yet. Create a spec or use Spec Builder to turn intent into executable phases.</div>{{end}}
  {{if .Drafts}}
    <div class="group-label">spec builder drafts</div>
    {{range .Drafts}}<article class="work-row"><div class="work-main"><a class="work-title" href="/spec-builder/{{.ID}}" hx-get="/spec-builder/{{.ID}}/fragment" hx-target="#app" hx-push-url="/spec-builder/{{.ID}}">{{.Title}}</a><div class="work-meta">draft created {{date .CreatedAt}}</div></div><div class="work-signals"><span class="chip chip-running">{{.Status}}</span></div><div class="work-next"><a class="btn" href="/spec-builder/{{.ID}}" hx-get="/spec-builder/{{.ID}}/fragment" hx-target="#app" hx-push-url="/spec-builder/{{.ID}}">Continue draft</a></div></article>{{end}}
  {{end}}
</div>
{{end}}

{{define "projects"}}
<div data-page="projects">
  <div class="page-header command-header"><div><p class="eyebrow">Foundry / projects</p><h2>Project registry</h2><p class="hint">Each project binds a target repo to one approved-memory namespace.</p></div><div class="card-actions"><button class="btn" popovertarget="new-project-page">+ Project</button><button class="btn btn-primary" hx-get="/projects/fragment?discover=1" hx-target="#app" hx-push-url="/projects">Discover repos</button></div></div>
  <div id="new-project-page" class="popover-card" popover>
    <h3>New Project</h3>
    <p class="hint">Use repo paths relative to the machine running Foundry.</p>
    <form method="post" action="/projects" hx-post="/projects" hx-target="#app" hx-swap="innerHTML">
      <div class="field"><label>Name</label><input name="name" required></div>
      <div class="field"><label>Target repo path</label><input name="repo_path" required></div>
      <div class="field"><label>Memory namespace</label><input name="memory_namespace" placeholder="Defaults to project name"><p class="hint">Leave blank to use the project name.</p></div>
      <button class="btn btn-primary">Create project</button>
    </form>
  </div>
  <div class="context-slab"><span>Approved memory repo</span><strong>{{if .MemoryRepoPath}}{{.MemoryRepoPath}}{{else}}not configured{{end}}</strong></div>
  {{if .Projects}}<div class="group-label">Registered projects</div><div class="project-grid">{{range .Projects}}<article class="project-slab"><div class="project-head"><a class="project-title" href="/projects/{{.ID}}" hx-get="/projects/{{.ID}}/fragment" hx-target="#app" hx-push-url="/projects/{{.ID}}">{{.Name}}</a><span class="chip chip-{{.MemoryClass}}">{{.MemoryState}}</span></div><dl class="fact-list"><div><dt>Target repo</dt><dd>{{.RepoPath}}</dd></div><div><dt>Memory namespace</dt><dd>{{if .MemoryNamespace}}{{.MemoryNamespace}}{{else}}—{{end}}</dd></div></dl><div class="card-actions"><a class="btn btn-primary" href="/projects/{{.ID}}" hx-get="/projects/{{.ID}}/fragment" hx-target="#app" hx-push-url="/projects/{{.ID}}">View / edit</a></div></article>{{end}}</div>{{else}}<div class="empty empty-action">No projects yet. Create one manually or discover repositories from the configured git root.</div>{{end}}
  {{if .DiscoverErr}}<div class="empty empty-error">Discovery failed: {{.DiscoverErr}}</div>{{end}}
  {{if .Repos}}<div class="group-label">Discovered repos</div>{{range .Repos}}<article class="work-row"><div class="work-main"><span class="work-title">{{.Name}}</span><div class="work-meta">target: {{.Path}}{{if .Imported}} · namespace: {{if .MemoryNamespace}}{{.MemoryNamespace}}{{else}}—{{end}}{{end}}</div></div><div class="work-signals">{{if .Imported}}<span class="chip chip-done">imported</span>{{else}}<span class="chip chip-pending">new</span>{{end}}</div>{{if not .Imported}}<div class="work-next"><form method="post" action="/projects?discover=1" hx-post="/projects?discover=1" hx-target="#app" hx-swap="innerHTML"><input type="hidden" name="name" value="{{.Name}}"><input type="hidden" name="repo_path" value="{{.Path}}"><input type="hidden" name="memory_namespace" value="{{.Name}}"><button class="btn btn-primary">Import project</button></form></div>{{end}}</article>{{end}}{{end}}
</div>
{{end}}

{{define "projectDetail"}}
<div data-page="projects">
  <div class="context-nav"><a class="btn" href="/projects" hx-get="/projects/fragment" hx-target="#app" hx-push-url="/projects">← Projects</a><a class="btn" href="/backlog" hx-get="/backlog/fragment" hx-target="#app" hx-push-url="/backlog">Backlog</a></div>
  <div class="page-header"><div><p class="eyebrow">Project #{{.Project.ID}}</p><h2>{{.Project.Name}}</h2><div class="card-meta">created {{date .Project.CreatedAt}}</div></div><button class="btn btn-danger" hx-delete="/projects/{{.Project.ID}}" hx-confirm="Delete this project and its specs/workflows?">Delete project</button></div>
  <div class="project-layout">
    <form class="panel-form" method="post" action="/projects/{{.Project.ID}}" hx-patch="/projects/{{.Project.ID}}" hx-target="#app" hx-swap="innerHTML">
      <h3>Edit project</h3>
      <div class="field"><label>Name</label><input name="name" value="{{.Project.Name}}" required></div>
      <div class="field"><label>Target repo path</label><input name="repo_path" value="{{.Project.RepoPath}}" required></div>
      <div class="field"><label>Memory namespace</label><input name="memory_namespace" value="{{.Project.MemoryNamespace}}" placeholder="Defaults to project name"><p class="hint">Namespace is read from the configured private memory repo.</p></div>
      <button class="btn btn-primary">Save changes</button>
    </form>
    <aside class="context-panel"><h3>Project context</h3><dl class="fact-list"><div><dt>Target repo</dt><dd>{{.Project.RepoPath}}</dd></div><div><dt>Memory namespace</dt><dd>{{.Project.MemoryNamespace}}</dd></div><div><dt>Approved memory</dt><dd>{{if .MemoryError}}error{{else if .Memory.Markdown}}{{len .Memory.Files}} file(s) available{{else}}none yet{{end}}</dd></div></dl></aside>
  </div>
  <div class="section"><h3>Approved memory</h3>{{if .MemoryError}}<div class="empty empty-error">{{.MemoryError}}</div>{{else if .Memory.Markdown}}<div class="card-meta">{{len .Memory.Files}} file(s) from {{.Memory.Root}}</div><pre class="doc-box">{{.Memory.Markdown}}</pre>{{else}}<div class="empty empty-action">No approved markdown memory found for this namespace. Accepted memory updates will appear here.</div>{{end}}</div>
</div>
{{end}}

{{define "specDetail"}}
<div data-page="backlog">
  <div class="context-nav"><a class="btn" href="/backlog" hx-get="/backlog/fragment" hx-target="#app" hx-push-url="/backlog">← Backlog</a><a class="btn" href="/projects/{{.Project.ID}}" hx-get="/projects/{{.Project.ID}}/fragment" hx-target="#app" hx-push-url="/projects/{{.Project.ID}}">Project: {{.Project.Name}}</a></div>
  <div class="context-slab spec-context"><span>Executable spec</span><strong>{{.Project.Name}} / Spec #{{.Spec.ID}}</strong><em>Runnable when content contains <code>## Phase N:</code> headings.</em></div>
  <div class="page-header spec-hero"><div><p class="eyebrow">Spec detail</p><h2>{{.Spec.Title}}</h2><div class="card-meta">Created {{date .Spec.CreatedAt}} · last {{date .Spec.UpdatedAt}}</div></div><div class="status-stack"><span class="chip chip-{{.Spec.Status}}">{{.Spec.Status}}</span><span class="chip chip-{{.Spec.Track}}">{{.Spec.Track}}</span></div></div>
  <div class="primary-action-slab"><div><strong>Next safe action</strong><p class="hint">Start an auditable workflow from this exact markdown intent.</p></div><div class="card-actions"><button class="btn btn-primary" data-json-post="/api/workflows" data-body='{"spec_id":{{.Spec.ID}}}' data-redirect-template="/workflows/{id}">Run workflow</button>{{if eq .Spec.Track "poc"}}<button class="btn" data-json-post="/api/specs/{{.Spec.ID}}/promote" data-refresh="/specs/{{.Spec.ID}}/fragment" data-target="#app">Promote to polish</button>{{end}}</div></div>
  <div class="spec-detail-grid"><section class="document-surface"><div class="section-title-row"><h3>Spec content</h3><span class="chip chip-pending">markdown source</span></div><pre class="doc-box spec-doc">{{.Spec.Content}}</pre></section>
  <aside class="history-panel"><h3>Workflow history</h3>{{if .Workflows}}<div class="history-list">{{range .Workflows}}<a class="history-row" href="/workflows/{{.ID}}" hx-get="/workflows/{{.ID}}/fragment" hx-target="#app" hx-push-url="/workflows/{{.ID}}"><span class="history-id">#{{.ID}}</span><span class="chip chip-{{.Status}}">{{.Status}}</span><span class="history-meta">{{.Track}} · budget {{money .MaxCostUSD}} · {{date .CreatedAt}}</span></a>{{end}}</div>{{else}}<div class="empty empty-action">No workflows yet. Run the workflow to produce logs, diffs, review notes, and decisions.</div>{{end}}</aside></div>
</div>
{{end}}

{{define "workflowDetail"}}
<div class="workflow-diffdesk" data-page="backlog" data-workflow-id="{{.Workflow.ID}}" data-workflow-stream="/api/workflows/{{.Workflow.ID}}/stream" data-refresh="/workflows/{{.Workflow.ID}}/fragment" data-selected-phase="{{.InitialPhase.ID}}" data-selected-tab="diff">
  <nav class="workflow-local-nav" aria-label="Workflow context"><a href="/backlog" hx-get="/backlog/fragment" hx-target="#app" hx-push-url="/backlog">Foundry</a><a href="/projects/{{.Project.ID}}" hx-get="/projects/{{.Project.ID}}/fragment" hx-target="#app" hx-push-url="/projects/{{.Project.ID}}">{{.Project.Name}}</a></nav>
  <header class="workflow-hero">
    <div><p class="eyebrow">Workflow #{{.Workflow.ID}} / {{.CurrentPhaseName}}</p><h1>{{.Spec.Title}}</h1><div class="workflow-meta">Workflow #{{.Workflow.ID}} / {{.CurrentPhaseName}}</div></div>
    <span class="chip chip-{{.Workflow.Status}}" data-workflow-status data-status="{{.Workflow.Status}}">{{phaseStatusLabel .Workflow.Status}}</span>
  </header>
  <div class="workflow-phase-strip" aria-label="Workflow phases">{{range .Phases}}<button type="button" class="phase-tile phase-row-{{.Status}} {{if eq .ID $.InitialPhase.ID}}is-selected{{end}}" id="phase-{{.ID}}" data-workflow-phase-select data-phase-row="{{.ID}}" data-phase-id="{{.ID}}" data-phase-name="{{.Name}}" data-phase-status="{{.Status}}"><span class="phase-tile-name">{{.Name}}</span>{{if eq .Status "failed"}}<span class="chip chip-{{.Status}}" data-phase-status-chip="{{.ID}}" data-status="{{.Status}}">{{phaseStatusLabel .Status}}</span>{{else if .ReviewVerdict}}<span class="chip chip-{{strptr .ReviewVerdict}}" data-phase-status-chip="{{.ID}}" data-status="{{strptr .ReviewVerdict}}">{{phaseStatusLabel (strptr .ReviewVerdict)}}</span>{{else}}<span class="chip chip-{{.Status}}" data-phase-status-chip="{{.ID}}" data-status="{{.Status}}">{{phaseStatusLabel .Status}}</span>{{end}}<span class="phase-progress" aria-hidden="true"><span class="phase-progress-fill {{phaseFillClass .Status}}" data-phase-fill="{{.ID}}" style="width: {{phaseProgress .Status}}%"></span></span></button>{{else}}<div class="empty empty-action">No phases have been created for this workflow yet.</div>{{end}}</div>
  <section class="workflow-work-window" aria-label="Workflow work window">
    <div class="work-window-head"><div><h2>Work window</h2><p class="hint">Select a phase, then inspect changed code or full-width logs.</p></div><div class="work-tabs"><button type="button" class="work-tab is-selected" data-work-window-tab="diff">Diff</button><button type="button" class="work-tab" data-work-window-tab="logs">Logs</button></div></div>
    {{if .HasInitialPhase}}<div class="selected-phase-actions"><div><strong data-selected-phase-name>{{.InitialPhase.Name}}</strong><div class="card-meta">Phase actions apply to the selected strip item.</div></div><div class="card-actions"><button class="btn btn-primary" data-json-post="/api/phases/{{.InitialPhase.ID}}/approve" data-phase-action="approve" data-refresh="/workflows/{{$.Workflow.ID}}/fragment" data-target="#app">Approve</button><button class="btn btn-danger" data-json-post="/api/phases/{{.InitialPhase.ID}}/reject" data-phase-action="reject" data-refresh="/workflows/{{$.Workflow.ID}}/fragment" data-target="#app">Reject</button><button class="btn" data-json-post="/api/phases/{{.InitialPhase.ID}}/clean" data-phase-action="clean" data-refresh="/workflows/{{$.Workflow.ID}}/fragment" data-target="#app">Clean</button></div></div><div id="workflow-work-body" class="workflow-work-body" data-phase-detail-panel="{{.InitialPhase.ID}}" hx-get="/phases/{{.InitialPhase.ID}}/diff/fragment" hx-trigger="load" hx-swap="innerHTML"></div>{{else}}<div id="workflow-work-body" class="workflow-work-body empty empty-action">No selected phase.</div>{{end}}
  </section>
  <details class="workflow-actions"><summary>Workflow actions and evidence</summary><div class="card-actions"><button class="btn" data-json-post="/api/workflows/{{.Workflow.ID}}/resume" data-refresh="/workflows/{{.Workflow.ID}}/fragment" data-target="#app">Resume</button>{{if eq .Workflow.Status "failed"}}<button class="btn btn-primary" data-json-post="/api/workflows/{{.Workflow.ID}}/follow-up" data-redirect-template="/workflows/{id}">Follow-up run</button>{{end}}<button class="btn btn-danger" data-json-post="/api/workflows/{{.Workflow.ID}}/stop" data-refresh="/workflows/{{.Workflow.ID}}/fragment" data-target="#app">Stop</button></div><div class="evidence-grid"><section><h3>Approved memory used</h3>{{if .MemoryError}}<div class="empty empty-error">{{.MemoryError}}</div>{{else if .Memory.Markdown}}<div class="card-meta">{{len .Memory.Files}} file(s) from {{.Memory.Root}}</div><pre class="doc-box">{{.Memory.Markdown}}</pre>{{else}}<div class="empty empty-action">No approved markdown memory found for this workflow's project namespace.</div>{{end}}</section><section><h3>Memory update review</h3>{{if .MemoryUpdateError}}<div class="empty empty-error">{{.MemoryUpdateError}}</div>{{end}}{{if .MemoryUpdate}}<div class="card"><div class="card-header"><span class="card-title">Memory update #{{.MemoryUpdate.ID}}</span><span class="chip chip-{{.MemoryUpdate.Status}}">{{.MemoryUpdate.Status}}</span></div>{{if .MemoryUpdate.MemoryPath}}<div class="card-meta">written to {{.MemoryUpdate.MemoryPath}}</div>{{end}}{{if .MemoryUpdate.ReviewerComment}}<div class="card-meta">comment: {{.MemoryUpdate.ReviewerComment}}</div>{{end}}<pre class="doc-box">{{.MemoryUpdate.ProposalMarkdown}}</pre><div class="card-actions"><button class="btn btn-primary" data-json-post="/api/workflows/{{.Workflow.ID}}/memory-update/accept" data-refresh="/workflows/{{.Workflow.ID}}/fragment" data-target="#app">Accept</button><button class="btn btn-danger" data-json-post="/api/workflows/{{.Workflow.ID}}/memory-update/reject" data-refresh="/workflows/{{.Workflow.ID}}/fragment" data-target="#app">Reject</button></div><form data-json method="post" action="/api/workflows/{{.Workflow.ID}}/memory-update/revise" data-refresh="/workflows/{{.Workflow.ID}}/fragment" data-target="#app"><div class="field"><label>Revision comment</label><textarea name="comment" required></textarea></div><button class="btn">Revise with comment</button></form></div>{{else}}<form data-json method="post" action="/api/workflows/{{.Workflow.ID}}/memory-update" data-refresh="/workflows/{{.Workflow.ID}}/fragment" data-target="#app"><div class="field"><label>Feedback for memory</label><textarea name="feedback" placeholder="What durable context should be remembered from this workflow?"></textarea></div><button class="btn btn-primary">Create memory update proposal</button></form>{{end}}</section></div></details>
</div>
{{end}}

{{define "phaseLogs"}}
<div><h3>Logs · Phase #{{.Phase.ID}} {{.Phase.Name}}</h3><div class="log-box" data-log-stream="/api/phases/{{.Phase.ID}}/logs/stream?after_id={{.LastLogID}}" data-log-last-id="{{.LastLogID}}">{{range .Logs}}<div class="log-line" data-log-id="{{.ID}}"><span class="log-ts">{{datetime .Ts}}</span>{{.Line}}</div>{{end}}</div></div>
{{end}}

{{define "phaseDiff"}}
<div><h3>Diff · Phase #{{.Phase.ID}} {{.Phase.Name}}</h3>{{if .Error}}<div class="empty">{{.Error}}</div>{{else}}<pre class="diff-box">{{.Diff}}</pre>{{end}}</div>
{{end}}

{{define "builderStart"}}
<div data-page="builder">
  <div class="page-header command-header"><div><p class="eyebrow">Spec Builder</p><h2>Turn intent into executable phases</h2><p class="hint">Choose a project, describe the change, then review the generated markdown before saving it as a spec.</p></div></div>
  <div class="builder-start-grid"><form class="panel-form builder-start-form" data-json method="post" action="/api/spec-drafts" data-redirect-template="/spec-builder/{id}">
    <h3>Start a draft</h3>
    <div class="field"><label>Project</label><select name="project_id" required>{{range .Projects}}<option value="{{.ID}}">{{.Name}}</option>{{end}}</select><p class="hint">Approved memory from this project's namespace is used as context.</p></div>
    <div class="field"><label>What should be built?</label><textarea name="description" required placeholder="Describe the feature, constraints, evidence needed, and expected phases."></textarea><p class="hint">For runnable work, ask for markdown with visible ## Phase N: headings.</p></div>
    <button class="btn btn-primary">Start builder</button>
  </form><aside class="context-panel"><h3>What the assistant does</h3><dl class="fact-list"><div><dt>Input</dt><dd>Your project, prompt, and approved memory.</dd></div><div><dt>Output</dt><dd>A reviewable markdown spec preview.</dd></div><div><dt>Safe point</dt><dd>You must save the draft before it becomes executable backlog work.</dd></div></dl></aside></div>
  {{if .Drafts}}<div class="group-label">Resume active drafts</div><div class="worklist">{{range .Drafts}}<article class="work-row"><div class="work-main"><a class="work-title" href="/spec-builder/{{.ID}}" hx-get="/spec-builder/{{.ID}}/fragment" hx-target="#app" hx-push-url="/spec-builder/{{.ID}}">{{.Title}}</a><div class="work-meta">updated {{datetime .UpdatedAt}}</div></div><div class="work-signals"><span class="chip chip-running">{{.Status}}</span></div><div class="work-next"><a class="btn btn-primary" href="/spec-builder/{{.ID}}" hx-get="/spec-builder/{{.ID}}/fragment" hx-target="#app" hx-push-url="/spec-builder/{{.ID}}">Continue</a></div></article>{{end}}</div>{{else}}<div class="empty empty-action">No active drafts. Start a draft to shape intent before it enters the backlog.</div>{{end}}
</div>
{{end}}

{{define "draftMessages"}}{{range .Messages}}<div class="chat-msg chat-msg-{{.Role}}"><div class="chat-msg-label">{{.Role}}</div><div class="chat-msg-body">{{.Content}}</div></div>{{end}}{{end}}

{{define "builderDetail"}}
<div data-page="builder" data-draft-stream="/api/spec-drafts/{{.Draft.ID}}/stream" data-draft-id="{{.Draft.ID}}">
  <div class="context-nav"><a class="btn" href="/spec-builder" hx-get="/spec-builder/fragment" hx-target="#app" hx-push-url="/spec-builder">← Spec Builder</a>{{if .HasProject}}<a class="btn" href="/projects/{{.Project.ID}}" hx-get="/projects/{{.Project.ID}}/fragment" hx-target="#app" hx-push-url="/projects/{{.Project.ID}}">Project: {{.Project.Name}}</a>{{end}}</div>
  <div class="page-header spec-hero"><div><p class="eyebrow">Draft #{{.Draft.ID}}</p><h2>{{.Draft.Title}}</h2><div class="card-meta">{{if .HasProject}}{{.Project.Name}} · {{end}}{{.Draft.Status}} · updated {{datetime .Draft.UpdatedAt}}</div></div><div class="card-actions"><button class="btn btn-primary" data-json-post="/api/spec-drafts/{{.Draft.ID}}/save" data-body='{"title":""}' data-redirect-template="/specs/{spec_id}">Save as spec</button><button class="btn btn-danger" data-json-delete="/api/spec-drafts/{{.Draft.ID}}" data-redirect="/backlog">Abandon</button></div></div>
  <div class="builder-status-slab"><span class="chip chip-running">{{.Draft.Status}}</span><strong>The assistant is shaping a spec. Review the preview before saving.</strong><span class="hint">Streaming errors remain in the page error region and chat.</span></div>
  <div class="spec-builder-layout"><section class="spec-builder-chat panel-form"><div class="section-title-row"><h3>Builder conversation</h3><span class="chip chip-streaming">live draft</span></div><div id="draft-messages" class="chat-messages">{{template "draftMessages" .}}</div><div id="draft-stream" class="chat-msg-streaming"></div><form data-json data-draft-message method="post" action="/api/spec-drafts/{{.Draft.ID}}/message"><div class="chat-input-row"><textarea class="chat-textarea" name="content" required placeholder="Reply with constraints, corrections, or ask for clearer phases…"></textarea><button class="btn btn-primary">Send</button></div></form></section><aside class="spec-preview-pane"><div class="section-title-row"><h3>Latest generated spec preview</h3><span class="chip chip-pending">review before save</span></div><pre id="draft-preview" class="doc-box spec-doc">{{if .Preview}}{{.Preview}}{{else}}Ask the builder to call update_spec with the full markdown spec, including ## Phase N: headings for executable work.{{end}}</pre></aside></div>
  <details class="section memory-details"><summary>Approved memory used</summary>{{if .MemoryError}}<div class="empty empty-error">{{.MemoryError}}</div>{{else if .Memory.Markdown}}<div class="card-meta">{{len .Memory.Files}} file(s) from {{.Memory.Root}}</div><pre class="doc-box">{{.Memory.Markdown}}</pre>{{else}}<div class="empty empty-action">No project memory loaded for this draft.</div>{{end}}</details>
</div>
{{end}}

{{define "settings"}}
<div data-page="settings">
  <h2 style="margin-bottom:1.25rem">Settings</h2>
  <form data-settings action="/api/settings" data-refresh="/settings/fragment" data-target="#app">
    {{range .Settings}}{{if not .IsVerbosity}}<div class="field"><label>{{.Key}}</label><input name="{{.Key}}" value="{{.Value}}"></div>{{end}}{{end}}
    <div class="field"><label for="verbosity-level">Verbosity level</label>{{if .HasVerbosity}}<select id="verbosity-level" name="{{.VerbosityKey}}"><option value="quiet" {{if eq .VerbosityValue "quiet"}}selected{{end}}>Quiet</option><option value="normal" {{if eq .VerbosityValue "normal"}}selected{{end}}>Normal</option><option value="verbose" {{if eq .VerbosityValue "verbose"}}selected{{end}}>Verbose</option></select>{{else}}<select id="verbosity-level" disabled><option>Normal</option></select><p class="hint">Static UI placeholder: this install has no verbosity key in config.yaml yet.</p>{{end}}</div>
    <p class="hint">Changes are written to config.yaml. Restart the server for most changes to take effect.</p>
    <button class="btn btn-primary">Save</button>
  </form>
  <h3 style="margin-top:2rem;margin-bottom:1rem">Cerberus sessions</h3>
  {{if .SessionError}}<div class="empty">{{.SessionError}}</div>{{end}}
  {{if .Sessions}}{{range .Sessions}}<article class="card"><div class="card-header"><span class="card-title">{{.Session}}</span><span class="chip chip-{{.FoundryStatus}}">{{.FoundryStatus}}</span>{{if .SafeToClean}}<span class="chip chip-done">safe cleanup</span>{{else}}<span class="chip chip-running">active / protected</span>{{end}}</div><div class="card-meta">{{.Type}}{{if .ProjectName}} · {{.ProjectName}}{{end}}{{if .SpecTitle}} · spec: {{.SpecTitle}}{{end}}{{if .PhaseName}} · phase: {{.PhaseName}}{{end}}{{if .DraftTitle}} · draft: {{.DraftTitle}}{{end}} · updated {{datetime .LastUpdatedAt}}</div><div class="card-meta">Cerberus: {{if .CerberusStatus}}{{.CerberusStatus}}{{else}}unknown{{end}}{{if .CerberusError}} · {{.CerberusError}}{{end}}{{if .UnsafeReason}} · {{.UnsafeReason}}{{end}}</div><div class="card-actions"><button class="btn" data-json-post="{{cleanSessionURL .Session}}" data-refresh="/settings/fragment" data-target="#app" {{if not .SafeToClean}}disabled title="{{.UnsafeReason}}"{{end}}>Clean session</button></div></article>{{end}}{{else}}<div class="empty">No Foundry-known Cerberus sessions.</div>{{end}}
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

func phaseStatusLabel(status string) string {
	if status == "" {
		return "unknown"
	}
	return strings.ReplaceAll(status, "_", " ")
}

func phaseProgress(status string) int {
	switch status {
	case "done", "failed":
		return 100
	case "running":
		return 40
	default:
		return 0
	}
}

func phaseFillClass(status string) string {
	switch status {
	case "done":
		return "phase-progress-done"
	case "running":
		return "phase-progress-running"
	case "failed":
		return "phase-progress-failed"
	case "awaiting_review":
		return "phase-progress-review"
	default:
		return "phase-progress-muted"
	}
}

func selectInitialPhase(phases []db.Phase) (db.Phase, bool) {
	if len(phases) == 0 {
		return db.Phase{}, false
	}
	for _, ph := range phases {
		if ph.Status == "running" {
			return ph, true
		}
	}
	for _, ph := range phases {
		if ph.Status == "awaiting_review" {
			return ph, true
		}
	}
	for _, ph := range phases {
		if ph.Status == "failed" {
			return ph, true
		}
	}
	for i := len(phases) - 1; i >= 0; i-- {
		if phases[i].Status == "done" {
			return phases[i], true
		}
	}
	return phases[0], true
}

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
	if r.URL.Path != "/projects" {
		http.NotFound(w, r)
		return
	}
	switch r.Method {
	case http.MethodGet:
		s.renderShell(w, "projects", "/projects/fragment")
	case http.MethodPost:
		s.handleUIProjectCreate(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}
func (s *Server) handleUISettingsPage(w http.ResponseWriter, r *http.Request) {
	s.renderShell(w, "settings", "/settings/fragment")
}

type uiSpecRow struct {
	db.Spec
	ProjectName string
}

type uiSpecGroup struct {
	Label string
	Items []uiSpecRow
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
	projectNames := map[int64]string{}
	for _, p := range projects {
		projectNames[p.ID] = p.Name
	}
	groups := []uiSpecGroup{{Label: "Needs attention"}, {Label: "Running / queued"}, {Label: "Ready to run"}, {Label: "Completed"}, {Label: "Other states"}}
	for _, sp := range specs {
		row := uiSpecRow{Spec: sp, ProjectName: projectNames[sp.ProjectID]}
		if row.ProjectName == "" {
			row.ProjectName = fmt.Sprintf("Project #%d", sp.ProjectID)
		}
		switch sp.Status {
		case "failed", "blocked", "awaiting_review", "review", "paused":
			groups[0].Items = append(groups[0].Items, row)
		case "running", "queued":
			groups[1].Items = append(groups[1].Items, row)
		case "pending", "idle", "draft":
			groups[2].Items = append(groups[2].Items, row)
		case "done", "accepted":
			groups[3].Items = append(groups[3].Items, row)
		default:
			groups[4].Items = append(groups[4].Items, row)
		}
	}
	data := struct {
		Projects []db.Project
		Groups   []uiSpecGroup
		HasSpecs bool
		Drafts   []db.SpecDraft
	}{projects, groups, len(specs) > 0, activeDrafts}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := uiTemplates.ExecuteTemplate(w, "backlog", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (s *Server) handleUIBacklogCreateProject(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	name := strings.TrimSpace(r.FormValue("name"))
	repoPath := strings.TrimSpace(r.FormValue("repo_path"))
	memoryNamespace := strings.TrimSpace(r.FormValue("memory_namespace"))
	if memoryNamespace == "" {
		memoryNamespace = name
	}
	if _, err := db.CreateProject(r.Context(), s.pool, name, repoPath, memoryNamespace); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.handleUIBacklogFragment(w, r)
}

func (s *Server) handleUIBacklogCreateSpec(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	projectID, err := strconv.ParseInt(r.FormValue("project_id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid project_id", http.StatusBadRequest)
		return
	}
	if _, err := db.CreateSpec(r.Context(), s.pool, projectID, strings.TrimSpace(r.FormValue("title")), r.FormValue("content"), []byte("[]")); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.handleUIBacklogFragment(w, r)
}

func (s *Server) handleUIBacklogCreateWorkflow(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	specID, err := strconv.ParseInt(r.FormValue("spec_id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid spec_id", http.StatusBadRequest)
		return
	}
	sp, err := db.GetSpec(r.Context(), s.pool, specID)
	if errors.Is(err, db.ErrNotFound) {
		http.Error(w, "spec not found", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	maxCost := &s.defaultBudget
	if raw := strings.TrimSpace(r.FormValue("max_cost_usd")); raw != "" {
		parsed, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			http.Error(w, "invalid max_cost_usd", http.StatusBadRequest)
			return
		}
		maxCost = &parsed
	}
	wf, err := db.CreateWorkflow(r.Context(), s.pool, sp.ID, sp.Track, maxCost)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	runStatus := "running"
	_, _ = db.UpdateSpec(r.Context(), s.pool, sp.ID, db.UpdateSpecParams{Status: &runStatus})
	s.runner.Start(wf.ID)
	w.Header().Set("HX-Redirect", fmt.Sprintf("/workflows/%d", wf.ID))
	w.WriteHeader(http.StatusCreated)
}

type uiRepoItem struct {
	discover.Repo
	Imported        bool
	MemoryNamespace string
}

type uiProjectRow struct {
	db.Project
	MemoryState string
	MemoryClass string
}

func projectMemoryState(repoPath string, p db.Project) (string, string) {
	mem, err := memory.LoadApproved(repoPath, p.MemoryNamespace)
	if err != nil {
		return "memory error", "error"
	}
	if strings.TrimSpace(mem.Markdown) == "" {
		return "no approved memory", "pending"
	}
	return fmt.Sprintf("%d memory file(s)", len(mem.Files)), "accepted"
}

func (s *Server) handleUIProjectsFragment(w http.ResponseWriter, r *http.Request) {
	projects, err := db.ListProjects(r.Context(), s.pool)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	projectRows := make([]uiProjectRow, 0, len(projects))
	for _, p := range projects {
		state, class := projectMemoryState(s.memoryRepoPath, p)
		projectRows = append(projectRows, uiProjectRow{Project: p, MemoryState: state, MemoryClass: class})
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
				repos = append(repos, uiRepoItem{Repo: repo, Imported: imported, MemoryNamespace: p.MemoryNamespace})
			}
		}
	}
	data := struct {
		Projects       []uiProjectRow
		Repos          []uiRepoItem
		DiscoverErr    string
		MemoryRepoPath string
	}{projectRows, repos, discoverErr, s.memoryRepoPath}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := uiTemplates.ExecuteTemplate(w, "projects", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (s *Server) handleUIProjectCreate(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	name := strings.TrimSpace(r.FormValue("name"))
	repoPath := strings.TrimSpace(r.FormValue("repo_path"))
	memoryNamespace := strings.TrimSpace(r.FormValue("memory_namespace"))
	if memoryNamespace == "" {
		memoryNamespace = name
	}
	if _, err := db.CreateProject(r.Context(), s.pool, name, repoPath, memoryNamespace); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.handleUIProjectsFragment(w, r)
}

func (s *Server) handleUIProject(w http.ResponseWriter, r *http.Request) {
	id, frag, ok := parseUIID(r.URL.Path, "/projects/")
	if !ok {
		http.NotFound(w, r)
		return
	}
	if frag {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		s.handleUIProjectFragment(w, r, id)
		return
	}
	switch r.Method {
	case http.MethodGet:
		s.renderShell(w, "projects", fmt.Sprintf("/projects/%d/fragment", id))
	case http.MethodPatch, http.MethodPost:
		s.handleUIProjectUpdate(w, r, id)
	case http.MethodDelete:
		s.handleUIProjectDelete(w, r, id)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleUIProjectUpdate(w http.ResponseWriter, r *http.Request, id int64) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	name := strings.TrimSpace(r.FormValue("name"))
	repoPath := strings.TrimSpace(r.FormValue("repo_path"))
	memoryNamespace := strings.TrimSpace(r.FormValue("memory_namespace"))
	if _, err := db.UpdateProject(r.Context(), s.pool, id, db.UpdateProjectParams{
		Name:            &name,
		RepoPath:        &repoPath,
		MemoryNamespace: &memoryNamespace,
	}); errors.Is(err, db.ErrNotFound) {
		http.NotFound(w, r)
		return
	} else if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.handleUIProjectFragment(w, r, id)
}

func (s *Server) handleUIProjectDelete(w http.ResponseWriter, r *http.Request, id int64) {
	if err := db.DeleteProject(r.Context(), s.pool, id); errors.Is(err, db.ErrNotFound) {
		http.NotFound(w, r)
		return
	} else if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("HX-Redirect", "/projects")
	w.WriteHeader(http.StatusNoContent)
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
	mem, memErr := memory.LoadApproved(s.memoryRepoPath, p.MemoryNamespace)
	memErrMsg := ""
	if memErr != nil {
		memErrMsg = memErr.Error()
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := uiTemplates.ExecuteTemplate(w, "projectDetail", struct {
		Project     db.Project
		Memory      memory.Slice
		MemoryError string
	}{p, mem, memErrMsg}); err != nil {
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
	proj, _ := db.GetProject(r.Context(), s.pool, sp.ProjectID)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := uiTemplates.ExecuteTemplate(w, "specDetail", struct {
		Spec      db.Spec
		Project   db.Project
		Workflows []db.Workflow
	}{sp, proj, wfs}); err != nil {
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
	proj, _ := db.GetProject(r.Context(), s.pool, sp.ProjectID)
	mem, memErr := memory.LoadApproved(s.memoryRepoPath, proj.MemoryNamespace)
	memErrMsg := ""
	if memErr != nil {
		memErrMsg = memErr.Error()
	}
	phases, err := db.ListPhasesByWorkflow(r.Context(), s.pool, id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	var memUpdate *db.MemoryUpdateJob
	var memUpdateErr string
	if job, err := db.GetLatestMemoryUpdateJobByWorkflow(r.Context(), s.pool, id); err == nil {
		memUpdate = &job
	} else if !errors.Is(err, db.ErrNotFound) {
		memUpdateErr = err.Error()
	}
	initialPhase, hasInitialPhase := selectInitialPhase(phases)
	currentPhaseName := "no phase"
	if hasInitialPhase {
		currentPhaseName = initialPhase.Name
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := uiTemplates.ExecuteTemplate(w, "workflowDetail", struct {
		Workflow          db.Workflow
		Spec              db.Spec
		Project           db.Project
		Phases            []db.Phase
		InitialPhase      db.Phase
		HasInitialPhase   bool
		CurrentPhaseName  string
		Memory            memory.Slice
		MemoryError       string
		MemoryUpdate      *db.MemoryUpdateJob
		MemoryUpdateError string
	}{wf, sp, proj, phases, initialPhase, hasInitialPhase, currentPhaseName, mem, memErrMsg, memUpdate, memUpdateErr}); err != nil {
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
	var lastLogID int64
	if len(logs) > 0 {
		lastLogID = logs[len(logs)-1].ID
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := uiTemplates.ExecuteTemplate(w, "phaseLogs", struct {
		Phase     db.Phase
		Logs      []db.PhaseLog
		LastLogID int64
	}{ph, logs, lastLogID}); err != nil {
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
	var mem memory.Slice
	var memErrMsg string
	var proj db.Project
	hasProject := false
	if draft.ProjectID != nil {
		if p, err := db.GetProject(r.Context(), s.pool, *draft.ProjectID); err == nil {
			proj = p
			hasProject = true
			if loaded, err := memory.LoadApproved(s.memoryRepoPath, proj.MemoryNamespace); err == nil {
				mem = loaded
			} else {
				memErrMsg = err.Error()
			}
		}
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := uiTemplates.ExecuteTemplate(w, "builderDetail", struct {
		Draft       db.SpecDraft
		Messages    []uiChatMessage
		Preview     string
		Project     db.Project
		HasProject  bool
		Memory      memory.Slice
		MemoryError string
	}{draft, msgs, preview, proj, hasProject, mem, memErrMsg}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

type cerberusSessionView struct {
	db.KnownCerberusSession
	CerberusStatus string `json:"cerberus_status"`
	CerberusError  string `json:"cerberus_error,omitempty"`
}

func (s *Server) handleUISettingsFragment(w http.ResponseWriter, r *http.Request) {
	data, err := os.ReadFile(s.cfgPath)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	profiles, _ := db.ListProfiles(r.Context(), s.pool)
	sessions, sessionErr := s.knownCerberusSessionViews(r.Context(), true)
	sessionErrMsg := ""
	if sessionErr != nil {
		sessionErrMsg = sessionErr.Error()
	}
	type setting struct {
		Key, Value  string
		IsVerbosity bool
	}
	var settings []setting
	var verbosityKey, verbosityValue string
	for _, line := range strings.Split(string(data), "\n") {
		parts := strings.SplitN(line, ":", 2)
		if len(parts) == 2 && strings.TrimSpace(parts[0]) != "" {
			key := strings.TrimSpace(parts[0])
			value := strings.Trim(strings.TrimSpace(parts[1]), "\"")
			isVerbosity := key == "verbosity" || key == "ui_verbosity" || key == "log_verbosity"
			if isVerbosity && verbosityKey == "" {
				verbosityKey, verbosityValue = key, value
			}
			settings = append(settings, setting{Key: key, Value: value, IsVerbosity: isVerbosity})
		}
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := uiTemplates.ExecuteTemplate(w, "settings", struct {
		Settings       []setting
		Profiles       []db.Profile
		Sessions       []cerberusSessionView
		SessionError   string
		HasVerbosity   bool
		VerbosityKey   string
		VerbosityValue string
	}{settings, profiles, sessions, sessionErrMsg, verbosityKey != "", verbosityKey, verbosityValue}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (s *Server) knownCerberusSessionViews(ctx context.Context, withStatus bool) ([]cerberusSessionView, error) {
	known, err := db.ListKnownCerberusSessions(ctx, s.pool)
	if err != nil {
		return nil, err
	}
	views := make([]cerberusSessionView, 0, len(known))
	for _, k := range known {
		v := cerberusSessionView{KnownCerberusSession: k}
		if withStatus && s.cerb != nil {
			if strings.TrimSpace(k.ProjectRepo) != "" {
				s.cerb.SetRepoPath(k.ProjectRepo)
			}
			status, err := s.cerb.Status(ctx, k.Session)
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

func (s *Server) handleCerberusSessions(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		views, err := s.knownCerberusSessionViews(r.Context(), true)
		if err != nil {
			jsonErr(w, err.Error(), http.StatusInternalServerError)
			return
		}
		jsonOK(w, views, http.StatusOK)
	default:
		jsonErr(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleCerberusSession(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/cerberus/sessions/")
	if strings.HasSuffix(path, "/clean") && r.Method == http.MethodPost {
		session := strings.TrimSuffix(path, "/clean")
		force := r.URL.Query().Get("force") == "1" || r.URL.Query().Get("force") == "true"
		var body struct {
			Force bool `json:"force"`
		}
		if r.Body != nil {
			if err := json.NewDecoder(r.Body).Decode(&body); err == nil && body.Force {
				force = true
			}
		}
		s.cleanKnownCerberusSession(w, r, session, force)
		return
	}
	jsonErr(w, "not found", http.StatusNotFound)
}

func (s *Server) cleanKnownCerberusSession(w http.ResponseWriter, r *http.Request, session string, force bool) {
	known, err := db.ListKnownCerberusSessions(r.Context(), s.pool)
	if err != nil {
		jsonErr(w, err.Error(), http.StatusInternalServerError)
		return
	}
	var item *db.KnownCerberusSession
	for i := range known {
		if known[i].Session == session {
			item = &known[i]
			break
		}
	}
	if item == nil {
		jsonErr(w, "unknown Foundry session", http.StatusNotFound)
		return
	}
	if !item.SafeToClean && !force {
		jsonErr(w, "refusing to clean active session: "+item.UnsafeReason, http.StatusConflict)
		return
	}
	if strings.TrimSpace(item.ProjectRepo) != "" {
		s.cerb.SetRepoPath(item.ProjectRepo)
	}
	if err := s.cerb.Clean(r.Context(), item.Session); err != nil {
		jsonErr(w, err.Error(), http.StatusInternalServerError)
		return
	}
	db.DeleteCerberusEvents(r.Context(), s.pool, item.Session)
	removeProfileFile(item.Session)
	jsonOK(w, map[string]string{"status": "cleaned", "session": item.Session}, http.StatusOK)
}

// ---- export ----

func (s *Server) handleExport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonErr(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	type exportPhase struct {
		db.Phase
		Logs []db.PhaseLog `json:"logs"`
	}
	type exportWorkflow struct {
		db.Workflow
		Phases []exportPhase `json:"phases"`
	}
	type exportSpec struct {
		db.Spec
		Workflows []exportWorkflow `json:"workflows"`
	}
	type exportPayload struct {
		Projects         []db.Project         `json:"projects"`
		Specs            []exportSpec         `json:"specs"`
		MemoryUpdateJobs []db.MemoryUpdateJob `json:"memory_update_jobs"`
		SpecDrafts       []db.SpecDraft       `json:"spec_drafts"`
		Profiles         []db.Profile         `json:"profiles"`
	}

	ctx := r.Context()
	fail := func(err error) bool {
		if err != nil {
			jsonErr(w, err.Error(), http.StatusInternalServerError)
			return true
		}
		return false
	}

	projects, err := db.ListProjects(ctx, s.pool)
	if fail(err) {
		return
	}
	specs, err := db.ListSpecs(ctx, s.pool, db.ListSpecsFilter{})
	if fail(err) {
		return
	}

	exportSpecs := make([]exportSpec, 0, len(specs))
	for _, spec := range specs {
		workflows, err := db.ListWorkflowsBySpec(ctx, s.pool, spec.ID)
		if fail(err) {
			return
		}
		exportWorkflows := make([]exportWorkflow, 0, len(workflows))
		for _, workflow := range workflows {
			phases, err := db.ListPhasesByWorkflow(ctx, s.pool, workflow.ID)
			if fail(err) {
				return
			}
			exportPhases := make([]exportPhase, 0, len(phases))
			for _, phase := range phases {
				logs, err := db.ListPhaseLogs(ctx, s.pool, phase.ID)
				if fail(err) {
					return
				}
				exportPhases = append(exportPhases, exportPhase{Phase: phase, Logs: logs})
			}
			exportWorkflows = append(exportWorkflows, exportWorkflow{Workflow: workflow, Phases: exportPhases})
		}
		exportSpecs = append(exportSpecs, exportSpec{Spec: spec, Workflows: exportWorkflows})
	}

	memoryUpdateJobs, err := db.ListMemoryUpdateJobs(ctx, s.pool)
	if fail(err) {
		return
	}
	specDrafts, err := db.ListSpecDrafts(ctx, s.pool)
	if fail(err) {
		return
	}
	profiles, err := db.ListProfiles(ctx, s.pool)
	if fail(err) {
		return
	}

	jsonOK(w, exportPayload{Projects: projects, Specs: exportSpecs, MemoryUpdateJobs: memoryUpdateJobs, SpecDrafts: specDrafts, Profiles: profiles}, http.StatusOK)
}

// ---- projects ----

func (s *Server) handleProjects(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		var body struct {
			Name            string `json:"name"`
			RepoPath        string `json:"repo_path"`
			MemoryNamespace string `json:"memory_namespace"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			jsonErr(w, err.Error(), http.StatusBadRequest)
			return
		}
		memoryNamespace := strings.TrimSpace(body.MemoryNamespace)
		if memoryNamespace == "" {
			memoryNamespace = body.Name
		}
		p, err := db.CreateProject(r.Context(), s.pool, body.Name, body.RepoPath, memoryNamespace)
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
		Imported        bool   `json:"imported"`
		MemoryNamespace string `json:"memory_namespace"`
	}
	out := make([]repoItem, 0, len(repos))
	for _, repo := range repos {
		p, imported := byPath[repo.Path]
		out = append(out, repoItem{Repo: repo, Imported: imported, MemoryNamespace: p.MemoryNamespace})
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
			Name            *string `json:"name"`
			RepoPath        *string `json:"repo_path"`
			MemoryNamespace *string `json:"memory_namespace"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			jsonErr(w, err.Error(), http.StatusBadRequest)
			return
		}
		p, err := db.UpdateProject(r.Context(), s.pool, id, db.UpdateProjectParams{
			Name:            body.Name,
			RepoPath:        body.RepoPath,
			MemoryNamespace: body.MemoryNamespace,
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
				_, _ = db.UpdatePhase(r.Context(), s.pool, ph.ID, resumeFailedPhaseUpdate())
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
	case suffix == "follow-up" && r.Method == http.MethodPost:
		s.handleWorkflowFollowUp(w, r, id)
	case strings.HasPrefix(suffix, "memory-update"):
		s.handleWorkflowMemoryUpdate(w, r, id, strings.Trim(strings.TrimPrefix(suffix, "memory-update"), "/"))
	case suffix == "stream":
		s.streamWorkflow(w, r, id)
	default:
		jsonErr(w, "not found", http.StatusNotFound)
	}
}

func (s *Server) handleWorkflowFollowUp(w http.ResponseWriter, r *http.Request, workflowID int64) {
	wf, sp, _, err := s.workflowProject(r.Context(), workflowID)
	if errors.Is(err, db.ErrNotFound) {
		jsonErr(w, "not found", http.StatusNotFound)
		return
	}
	if err != nil {
		jsonErr(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if wf.Status != "failed" {
		jsonErr(w, "follow-up runs can only be created for failed workflows", http.StatusConflict)
		return
	}
	phases, err := db.ListPhasesByWorkflow(r.Context(), s.pool, workflowID)
	if err != nil {
		jsonErr(w, err.Error(), http.StatusInternalServerError)
		return
	}
	failed := make([]db.Phase, 0)
	for _, ph := range phases {
		if ph.Status == "failed" {
			failed = append(failed, ph)
		}
	}
	if len(failed) == 0 {
		jsonErr(w, "workflow has no failed phases", http.StatusConflict)
		return
	}

	content := s.buildFollowUpSpecContent(r.Context(), sp, wf, failed)
	newTitle := "Follow-up: " + sp.Title
	newSpec, err := db.CreateSpec(r.Context(), s.pool, sp.ProjectID, newTitle, content, sp.Tags)
	if err != nil {
		jsonErr(w, err.Error(), http.StatusInternalServerError)
		return
	}
	track := sp.Track
	running := "running"
	newSpec, err = db.UpdateSpec(r.Context(), s.pool, newSpec.ID, db.UpdateSpecParams{Track: &track, Status: &running})
	if err != nil {
		jsonErr(w, err.Error(), http.StatusInternalServerError)
		return
	}
	newWorkflow, err := db.CreateWorkflow(r.Context(), s.pool, newSpec.ID, newSpec.Track, wf.MaxCostUSD)
	if err != nil {
		jsonErr(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.runner.Start(newWorkflow.ID)
	jsonOK(w, newWorkflow, http.StatusCreated)
}

func (s *Server) buildFollowUpSpecContent(ctx context.Context, sp db.Spec, wf db.Workflow, failed []db.Phase) string {
	return buildFollowUpSpecContentWithContext(sp, s.buildFollowUpContext(ctx, wf, failed))
}

func buildFollowUpSpecContentWithContext(sp db.Spec, context string) string {
	content := strings.TrimSpace(sp.Content)
	idx := strings.Index(content, "\n## Phase ")
	if idx == -1 {
		return strings.TrimSpace(content + "\n\n" + context)
	}
	return strings.TrimSpace(content[:idx] + "\n\n" + context + "\n" + content[idx+1:])
}

func (s *Server) buildFollowUpContext(ctx context.Context, wf db.Workflow, failed []db.Phase) string {
	return buildFollowUpFailureContext(ctx, wf, failed, func(ctx context.Context, phaseID int64, limit int) ([]db.PhaseLog, error) {
		return db.ListRecentPhaseLogs(ctx, s.pool, phaseID, limit)
	})
}

func buildFollowUpFailureContext(ctx context.Context, wf db.Workflow, failed []db.Phase, recentLogs func(context.Context, int64, int) ([]db.PhaseLog, error)) string {
	var b strings.Builder
	b.WriteString("## Follow-up run context\n\n")
	b.WriteString(fmt.Sprintf("This spec was generated as a follow-up to failed workflow #%d. Use the failure context below to avoid repeating the same mistakes and to complete the original phases.\n", wf.ID))
	for _, ph := range failed {
		b.WriteString(fmt.Sprintf("\n### Failed phase %d: %s\n\n", ph.Position, ph.Name))
		b.WriteString(fmt.Sprintf("- Phase ID: %d\n- Status: %s\n- Retry count: %d\n", ph.ID, ph.Status, ph.RetryCount))
		if ph.ReviewVerdict != nil && strings.TrimSpace(*ph.ReviewVerdict) != "" {
			b.WriteString("- Review verdict: ")
			b.WriteString(strings.TrimSpace(*ph.ReviewVerdict))
			b.WriteString("\n")
		}
		if ph.ReviewNotes != nil && strings.TrimSpace(*ph.ReviewNotes) != "" {
			b.WriteString("\nReview notes:\n")
			b.WriteString(indentBlock(strings.TrimSpace(*ph.ReviewNotes)))
			b.WriteString("\n")
		}
		if ph.DecisionSummary != nil && strings.TrimSpace(*ph.DecisionSummary) != "" {
			b.WriteString("\nDecision summary:\n")
			b.WriteString(indentBlock(strings.TrimSpace(*ph.DecisionSummary)))
			b.WriteString("\n")
		}
		if ph.DecisionRationale != nil && strings.TrimSpace(*ph.DecisionRationale) != "" {
			b.WriteString("\nDecision rationale:\n")
			b.WriteString(indentBlock(strings.TrimSpace(*ph.DecisionRationale)))
			b.WriteString("\n")
		}
		if ph.PromptSent != nil && strings.TrimSpace(*ph.PromptSent) != "" {
			b.WriteString("\nPrompt sent excerpt:\n")
			b.WriteString(indentBlock(truncateString(strings.TrimSpace(*ph.PromptSent), 2000)))
			b.WriteString("\n")
		}
		var logs []db.PhaseLog
		if recentLogs != nil {
			var err error
			logs, err = recentLogs(ctx, ph.ID, 80)
			if err != nil {
				b.WriteString("\nLog summary: unavailable: ")
				b.WriteString(err.Error())
				b.WriteString("\n")
				continue
			}
		}
		if len(logs) > 0 {
			b.WriteString("\nRecent log summary (tail):\n")
			var lines []string
			for _, l := range logs {
				line := strings.TrimSpace(l.Line)
				if line != "" {
					lines = append(lines, line)
				}
			}
			b.WriteString(indentBlock(truncateString(strings.Join(lines, "\n"), 4000)))
			b.WriteString("\n")
		}
	}
	return strings.TrimSpace(b.String())
}

func indentBlock(s string) string {
	if s == "" {
		return ""
	}
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		lines[i] = "> " + line
	}
	return strings.Join(lines, "\n")
}

func truncateString(s string, max int) string {
	const marker = "\n... truncated ..."
	if max <= 0 || len(s) <= max {
		return s
	}
	if max <= len(marker) {
		return s[:max]
	}
	return s[:max-len(marker)] + marker
}

func (s *Server) handleWorkflowMemoryUpdate(w http.ResponseWriter, r *http.Request, workflowID int64, action string) {
	switch {
	case action == "" && r.Method == http.MethodGet:
		job, err := db.GetLatestMemoryUpdateJobByWorkflow(r.Context(), s.pool, workflowID)
		if errors.Is(err, db.ErrNotFound) {
			jsonErr(w, "not found", http.StatusNotFound)
			return
		}
		if err != nil {
			jsonErr(w, err.Error(), http.StatusInternalServerError)
			return
		}
		jsonOK(w, job, http.StatusOK)
	case action == "" && r.Method == http.MethodPost:
		var body struct {
			Feedback         string `json:"feedback"`
			Comment          string `json:"comment"`
			ProposalMarkdown string `json:"proposal_markdown"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			jsonErr(w, err.Error(), http.StatusBadRequest)
			return
		}
		proposal := strings.TrimSpace(body.ProposalMarkdown)
		comment := strings.TrimSpace(body.Comment)
		if comment == "" {
			comment = strings.TrimSpace(body.Feedback)
		}
		if proposal == "" {
			var err error
			proposal, err = s.generateWorkflowMemoryProposal(r.Context(), workflowID, comment, "")
			if errors.Is(err, db.ErrNotFound) {
				jsonErr(w, "not found", http.StatusNotFound)
				return
			}
			if err != nil {
				jsonErr(w, err.Error(), http.StatusInternalServerError)
				return
			}
		}
		job, err := db.CreateMemoryUpdateJob(r.Context(), s.pool, workflowID, proposal, comment)
		if err != nil {
			jsonErr(w, err.Error(), http.StatusInternalServerError)
			return
		}
		jsonOK(w, job, http.StatusCreated)
	case action == "accept" && r.Method == http.MethodPost:
		job, err := db.GetLatestMemoryUpdateJobByWorkflow(r.Context(), s.pool, workflowID)
		if errors.Is(err, db.ErrNotFound) {
			jsonErr(w, "not found", http.StatusNotFound)
			return
		}
		if err != nil {
			jsonErr(w, err.Error(), http.StatusInternalServerError)
			return
		}
		_, _, proj, err := s.workflowProject(r.Context(), workflowID)
		if errors.Is(err, db.ErrNotFound) {
			jsonErr(w, "not found", http.StatusNotFound)
			return
		}
		if err != nil {
			jsonErr(w, err.Error(), http.StatusInternalServerError)
			return
		}
		path, err := memory.WriteApprovedUpdate(s.memoryRepoPath, proj.MemoryNamespace, workflowID, job.ProposalMarkdown)
		if err != nil {
			jsonErr(w, err.Error(), http.StatusInternalServerError)
			return
		}
		job, err = db.UpdateMemoryUpdateJob(r.Context(), s.pool, job.ID, acceptMemoryUpdateParams(path))
		if err != nil {
			jsonErr(w, err.Error(), http.StatusInternalServerError)
			return
		}
		jsonOK(w, job, http.StatusOK)
	case action == "reject" && r.Method == http.MethodPost:
		var body struct {
			Comment string `json:"comment"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		job, err := db.GetLatestMemoryUpdateJobByWorkflow(r.Context(), s.pool, workflowID)
		if errors.Is(err, db.ErrNotFound) {
			jsonErr(w, "not found", http.StatusNotFound)
			return
		}
		if err != nil {
			jsonErr(w, err.Error(), http.StatusInternalServerError)
			return
		}
		job, err = db.UpdateMemoryUpdateJob(r.Context(), s.pool, job.ID, rejectMemoryUpdateParams(body.Comment))
		if err != nil {
			jsonErr(w, err.Error(), http.StatusInternalServerError)
			return
		}
		jsonOK(w, job, http.StatusOK)
	case action == "revise" && r.Method == http.MethodPost:
		var body struct {
			Comment          string `json:"comment"`
			ProposalMarkdown string `json:"proposal_markdown"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			jsonErr(w, err.Error(), http.StatusBadRequest)
			return
		}
		job, err := db.GetLatestMemoryUpdateJobByWorkflow(r.Context(), s.pool, workflowID)
		if errors.Is(err, db.ErrNotFound) {
			jsonErr(w, "not found", http.StatusNotFound)
			return
		}
		if err != nil {
			jsonErr(w, err.Error(), http.StatusInternalServerError)
			return
		}
		proposal := strings.TrimSpace(body.ProposalMarkdown)
		if proposal == "" {
			proposal, err = s.generateWorkflowMemoryProposal(r.Context(), workflowID, body.Comment, job.ProposalMarkdown)
			if errors.Is(err, db.ErrNotFound) {
				jsonErr(w, "not found", http.StatusNotFound)
				return
			}
			if err != nil {
				jsonErr(w, err.Error(), http.StatusInternalServerError)
				return
			}
		}
		job, err = db.UpdateMemoryUpdateJob(r.Context(), s.pool, job.ID, reviseMemoryUpdateParams(body.Comment, proposal))
		if err != nil {
			jsonErr(w, err.Error(), http.StatusInternalServerError)
			return
		}
		jsonOK(w, job, http.StatusOK)
	default:
		jsonErr(w, "not found", http.StatusNotFound)
	}
}

func (s *Server) handleMemoryUpdate(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/memory-updates/")
	parts := strings.SplitN(path, "/", 2)
	id, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		jsonErr(w, "invalid id", http.StatusBadRequest)
		return
	}
	action := ""
	if len(parts) == 2 {
		action = parts[1]
	}
	job, err := db.GetMemoryUpdateJob(r.Context(), s.pool, id)
	if errors.Is(err, db.ErrNotFound) {
		jsonErr(w, "not found", http.StatusNotFound)
		return
	}
	if err != nil {
		jsonErr(w, err.Error(), http.StatusInternalServerError)
		return
	}
	switch {
	case action == "" && r.Method == http.MethodGet:
		jsonOK(w, job, http.StatusOK)
	case action == "accept" && r.Method == http.MethodPost:
		_, _, proj, err := s.workflowProject(r.Context(), job.WorkflowID)
		if errors.Is(err, db.ErrNotFound) {
			jsonErr(w, "not found", http.StatusNotFound)
			return
		}
		if err != nil {
			jsonErr(w, err.Error(), http.StatusInternalServerError)
			return
		}
		path, err := memory.WriteApprovedUpdate(s.memoryRepoPath, proj.MemoryNamespace, job.WorkflowID, job.ProposalMarkdown)
		if err != nil {
			jsonErr(w, err.Error(), http.StatusInternalServerError)
			return
		}
		job, err = db.UpdateMemoryUpdateJob(r.Context(), s.pool, job.ID, acceptMemoryUpdateParams(path))
		if err != nil {
			jsonErr(w, err.Error(), http.StatusInternalServerError)
			return
		}
		jsonOK(w, job, http.StatusOK)
	case action == "reject" && r.Method == http.MethodPost:
		var body struct {
			Comment string `json:"comment"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		job, err = db.UpdateMemoryUpdateJob(r.Context(), s.pool, job.ID, rejectMemoryUpdateParams(body.Comment))
		if err != nil {
			jsonErr(w, err.Error(), http.StatusInternalServerError)
			return
		}
		jsonOK(w, job, http.StatusOK)
	case action == "revise" && r.Method == http.MethodPost:
		var body struct {
			Comment          string `json:"comment"`
			ProposalMarkdown string `json:"proposal_markdown"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			jsonErr(w, err.Error(), http.StatusBadRequest)
			return
		}
		proposal := strings.TrimSpace(body.ProposalMarkdown)
		if proposal == "" {
			proposal, err = s.generateWorkflowMemoryProposal(r.Context(), job.WorkflowID, body.Comment, job.ProposalMarkdown)
			if errors.Is(err, db.ErrNotFound) {
				jsonErr(w, "not found", http.StatusNotFound)
				return
			}
			if err != nil {
				jsonErr(w, err.Error(), http.StatusInternalServerError)
				return
			}
		}
		job, err = db.UpdateMemoryUpdateJob(r.Context(), s.pool, job.ID, reviseMemoryUpdateParams(body.Comment, proposal))
		if err != nil {
			jsonErr(w, err.Error(), http.StatusInternalServerError)
			return
		}
		jsonOK(w, job, http.StatusOK)
	default:
		jsonErr(w, "not found", http.StatusNotFound)
	}
}

func acceptMemoryUpdateParams(path string) db.UpdateMemoryUpdateJobParams {
	accepted := "accepted"
	path = strings.TrimSpace(path)
	return db.UpdateMemoryUpdateJobParams{Status: &accepted, MemoryPath: &path}
}

func rejectMemoryUpdateParams(comment string) db.UpdateMemoryUpdateJobParams {
	rejected := "rejected"
	comment = strings.TrimSpace(comment)
	return db.UpdateMemoryUpdateJobParams{Status: &rejected, ReviewerComment: &comment}
}

func reviseMemoryUpdateParams(comment, proposal string) db.UpdateMemoryUpdateJobParams {
	pending := "pending"
	comment = strings.TrimSpace(comment)
	proposal = strings.TrimSpace(proposal)
	params := db.UpdateMemoryUpdateJobParams{Status: &pending, ReviewerComment: &comment}
	if proposal != "" {
		params.ProposalMarkdown = &proposal
	}
	return params
}

func (s *Server) generateWorkflowMemoryProposal(ctx context.Context, workflowID int64, comment, previousProposal string) (string, error) {
	contextMarkdown, err := s.buildWorkflowMemoryProposal(ctx, workflowID, comment)
	if err != nil {
		return "", err
	}
	return s.generateMemoryProposalMarkdown(ctx, workflowID, contextMarkdown, comment, previousProposal)
}

func (s *Server) generateMemoryProposalMarkdown(ctx context.Context, workflowID int64, contextMarkdown, comment, previousProposal string) (string, error) {
	if strings.TrimSpace(s.memoryRepoPath) == "" {
		return "", fmt.Errorf("memory repo path not configured")
	}
	if s.cerb == nil {
		return "", fmt.Errorf("cerberus client not configured")
	}
	session := fmt.Sprintf("foundry-memory-update-%d-%d", workflowID, time.Now().UnixNano())
	profilePath, profileErr := s.writeProfileFile(ctx, session)
	if profileErr != nil {
		log.Printf("memory update proposal: write profile file: %v (proceeding without profile)", profileErr)
	}
	defer removeProfileFile(session)
	s.cerb.SetProfile(profilePath)
	s.cerb.SetRepoPath(s.memoryRepoPath)
	out, err := s.cerb.Generate(ctx, session, memoryProposalPrompt(contextMarkdown, comment, previousProposal))
	if cleanErr := s.cerb.Clean(ctx, session); cleanErr != nil {
		log.Printf("memory update proposal: clean session %s: %v", session, cleanErr)
	}
	if err != nil {
		return "", err
	}
	proposal := strings.TrimSpace(out)
	if proposal == "" {
		return "", fmt.Errorf("cerberus returned empty memory proposal")
	}
	return proposal, nil
}

func memoryProposalPrompt(contextMarkdown, comment, previousProposal string) string {
	var b strings.Builder
	b.WriteString("You are updating a private project memory repository. Read the existing memory files in /workspace as needed, but do not create, edit, delete, or commit files.\n\n")
	b.WriteString("Return only the proposed durable memory update as markdown. Include concise facts that should help future work on this project. Exclude transient logs, prompt bodies, and implementation noise.\n")
	if strings.TrimSpace(comment) != "" {
		b.WriteString("\nReviewer instruction:\n")
		b.WriteString(strings.TrimSpace(comment))
		b.WriteString("\n")
	}
	if strings.TrimSpace(previousProposal) != "" {
		b.WriteString("\nCurrent proposal to revise:\n")
		b.WriteString(strings.TrimSpace(previousProposal))
		b.WriteString("\n")
	}
	b.WriteString("\nWorkflow context:\n")
	b.WriteString(strings.TrimSpace(contextMarkdown))
	b.WriteString("\n")
	return b.String()
}

func (s *Server) workflowProject(ctx context.Context, workflowID int64) (db.Workflow, db.Spec, db.Project, error) {
	wf, err := db.GetWorkflow(ctx, s.pool, workflowID)
	if err != nil {
		return wf, db.Spec{}, db.Project{}, err
	}
	sp, err := db.GetSpec(ctx, s.pool, wf.SpecID)
	if err != nil {
		return wf, sp, db.Project{}, err
	}
	proj, err := db.GetProject(ctx, s.pool, sp.ProjectID)
	return wf, sp, proj, err
}

func (s *Server) buildWorkflowMemoryProposal(ctx context.Context, workflowID int64, feedback string) (string, error) {
	wf, sp, proj, err := s.workflowProject(ctx, workflowID)
	if err != nil {
		return "", err
	}
	phases, err := db.ListPhasesByWorkflow(ctx, s.pool, workflowID)
	if err != nil {
		return "", err
	}
	return formatWorkflowMemoryProposal(wf, sp, proj, phases, feedback), nil
}

func formatWorkflowMemoryProposal(wf db.Workflow, sp db.Spec, proj db.Project, phases []db.Phase, feedback string) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("# Workflow %d memory update\n\n", wf.ID))
	b.WriteString(fmt.Sprintf("Project: %s\nSpec: %s\nTrack: %s\nStatus: %s\n", proj.Name, sp.Title, wf.Track, wf.Status))
	if feedback = strings.TrimSpace(feedback); feedback != "" {
		b.WriteString("\n## Reviewer feedback\n\n")
		b.WriteString(feedback)
		b.WriteString("\n")
	}
	b.WriteString("\n## Phase decisions\n")
	for _, ph := range phases {
		b.WriteString(fmt.Sprintf("\n### Phase %d: %s\n\n", ph.Position, ph.Name))
		if ph.DecisionSummary != nil && strings.TrimSpace(*ph.DecisionSummary) != "" {
			b.WriteString("Summary: ")
			b.WriteString(strings.TrimSpace(*ph.DecisionSummary))
			b.WriteString("\n")
		}
		if ph.DecisionRationale != nil && strings.TrimSpace(*ph.DecisionRationale) != "" {
			b.WriteString("Rationale: ")
			b.WriteString(strings.TrimSpace(*ph.DecisionRationale))
			b.WriteString("\n")
		}
		if len(ph.FilesTouched) > 0 && string(ph.FilesTouched) != "[]" {
			b.WriteString("Files touched: `")
			b.WriteString(string(ph.FilesTouched))
			b.WriteString("`\n")
		}
		if feedback := formatPhaseFeedback(ph.PhaseFeedback); feedback != "" {
			b.WriteString("Structured feedback:\n")
			b.WriteString(feedback)
			b.WriteString("\n")
		}
	}
	return strings.TrimSpace(b.String())
}

func formatPhaseFeedback(raw []byte) string {
	if len(raw) == 0 || string(raw) == "{}" {
		return ""
	}
	var fb struct {
		Result          string   `json:"result"`
		UsefulContext   []string `json:"useful_context"`
		Problems        []string `json:"problems"`
		SuggestedMemory string   `json:"suggested_memory"`
		Confidence      float64  `json:"confidence"`
	}
	if err := json.Unmarshal(raw, &fb); err != nil {
		return ""
	}
	var lines []string
	if s := strings.TrimSpace(fb.Result); s != "" {
		lines = append(lines, "- Result: "+s)
	}
	for _, s := range fb.UsefulContext {
		if s = strings.TrimSpace(s); s != "" {
			lines = append(lines, "- Useful context: "+s)
		}
	}
	for _, s := range fb.Problems {
		if s = strings.TrimSpace(s); s != "" {
			lines = append(lines, "- Problem: "+s)
		}
	}
	if s := strings.TrimSpace(fb.SuggestedMemory); s != "" {
		lines = append(lines, "- Suggested memory: "+s)
	}
	if fb.Confidence != 0 {
		lines = append(lines, fmt.Sprintf("- Confidence: %.2f", fb.Confidence))
	}
	return strings.Join(lines, "\n")
}

// ---- phases ----

func resumeFailedPhaseUpdate() db.UpdatePhaseParams {
	pending := "pending"
	zero := 0
	return db.UpdatePhaseParams{Status: &pending, RetryCount: &zero}
}

func approvePhaseUpdate(now time.Time) db.UpdatePhaseParams {
	done := "done"
	pass := "pass"
	return db.UpdatePhaseParams{Status: &done, ReviewVerdict: &pass, FinishedAt: &now}
}

func rejectPhaseUpdate(now time.Time) db.UpdatePhaseParams {
	failed := "failed"
	fail := "fail"
	return db.UpdatePhaseParams{Status: &failed, ReviewVerdict: &fail, FinishedAt: &now}
}

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
		_, err := db.UpdatePhase(r.Context(), s.pool, id, approvePhaseUpdate(time.Now()))
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
		_, err := db.UpdatePhase(r.Context(), s.pool, id, rejectPhaseUpdate(time.Now()))
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
			if _, _, proj, err := s.workflowProject(r.Context(), ph.WorkflowID); err == nil {
				s.cerb.SetRepoPath(proj.RepoPath)
			}
			if err := s.cerb.Clean(r.Context(), *ph.CerberusSession); err != nil {
				jsonErr(w, err.Error(), http.StatusInternalServerError)
				return
			}
			db.DeleteCerberusEvents(r.Context(), s.pool, *ph.CerberusSession)
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
	ph, err := db.GetPhase(r.Context(), s.pool, phaseID)
	if errors.Is(err, db.ErrNotFound) {
		jsonErr(w, "not found", http.StatusNotFound)
		return
	}
	if err != nil {
		jsonErr(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	var lastID int64
	if raw := r.URL.Query().Get("after_id"); raw != "" {
		if parsed, err := strconv.ParseInt(raw, 10, 64); err == nil && parsed > 0 {
			lastID = parsed
		}
	}

	sendCatchup := func() bool {
		logs, err := db.StreamPhaseLogs(r.Context(), s.pool, phaseID, lastID)
		if err != nil {
			return false
		}
		for _, l := range logs {
			data, _ := json.Marshal(l)
			fmt.Fprintf(w, "id: %d\ndata: %s\n\n", l.ID, data)
			lastID = l.ID
		}
		flusher.Flush()
		return true
	}
	isTerminal := func() bool {
		ph, err := db.GetPhase(r.Context(), s.pool, phaseID)
		if err != nil {
			return true
		}
		return ph.Status == "done" || ph.Status == "failed"
	}

	if !sendCatchup() {
		return
	}
	if isTerminal() {
		fmt.Fprintf(w, "event: done\ndata: {}\n\n")
		flusher.Flush()
		return
	}

	key := fmt.Sprintf("wf:%d", ph.WorkflowID)
	ch := s.eventHub.Subscribe(key)
	defer s.eventHub.Unsubscribe(key, ch)
	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-heartbeat.C:
			if !sendCatchup() {
				return
			}
			fmt.Fprintf(w, "event: heartbeat\ndata: {}\n\n")
			flusher.Flush()
			if isTerminal() {
				fmt.Fprintf(w, "event: done\ndata: {}\n\n")
				flusher.Flush()
				return
			}
		case data, ok := <-ch:
			if !ok {
				return
			}
			var evt struct {
				Event   string `json:"event"`
				PhaseID int64  `json:"phase_id"`
			}
			if json.Unmarshal(data, &evt) != nil {
				continue
			}
			if evt.Event == "log" && evt.PhaseID == phaseID {
				if !sendCatchup() {
					return
				}
			} else if evt.Event == "phase_update" && evt.PhaseID == phaseID && (isTerminal()) {
				if !sendCatchup() {
					return
				}
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

Before drafting or materially updating a spec, read the key intent files in the project's memory namespace when they exist. Use file path references only; do not inline wiki contents into the prompt or generated spec.

Default intent files to inspect under the configured project memory namespace:
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
		if strings.TrimSpace(s.memoryRepoPath) == "" {
			jsonErr(w, "memory repo path is not configured", http.StatusUnprocessableEntity)
			return
		}
		var body struct {
			ProjectID   *int64 `json:"project_id"`
			Description string `json:"description"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			jsonErr(w, err.Error(), http.StatusBadRequest)
			return
		}
		if body.ProjectID == nil {
			jsonErr(w, "project_id is required", http.StatusUnprocessableEntity)
			return
		}
		proj, err := db.GetProject(r.Context(), s.pool, *body.ProjectID)
		if errors.Is(err, db.ErrNotFound) {
			jsonErr(w, "project not found", http.StatusNotFound)
			return
		}
		if err != nil {
			jsonErr(w, err.Error(), http.StatusInternalServerError)
			return
		}
		projectRepoPath := strings.TrimSpace(proj.RepoPath)
		if projectRepoPath == "" {
			jsonErr(w, "project repo path is not configured", http.StatusUnprocessableEntity)
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
		initialPrompt += "\n\nProject name: " + proj.Name + "\nThe selected project's repository is mounted at /workspace inside your container. Use project memory namespace " + proj.MemoryNamespace + "."
		if mem, err := memory.LoadApproved(s.memoryRepoPath, proj.MemoryNamespace); err == nil {
			initialPrompt = memory.Prepend(mem.Markdown, initialPrompt)
		} else {
			log.Printf("spec-builder draft %d: load memory: %v", draft.ID, err)
		}

		pool := s.pool
		cerb := s.cerb
		draftID := draft.ID
		cbURL := s.callbackURL()
		cerberusRepoPath := projectRepoPath
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
			cerb.SetRepoPath(cerberusRepoPath)
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
		if draft.ProjectID == nil {
			jsonErr(w, "draft has no project", http.StatusUnprocessableEntity)
			return
		}
		proj, err := db.GetProject(r.Context(), s.pool, *draft.ProjectID)
		if errors.Is(err, db.ErrNotFound) {
			jsonErr(w, "project not found", http.StatusNotFound)
			return
		}
		if err != nil {
			jsonErr(w, err.Error(), http.StatusInternalServerError)
			return
		}
		projectRepoPath := strings.TrimSpace(proj.RepoPath)
		if projectRepoPath == "" {
			jsonErr(w, "project repo path is not configured", http.StatusUnprocessableEntity)
			return
		}
		cbURL := s.callbackURL()
		cerb := s.cerb
		session := draft.CerberusSession
		pool := s.pool
		draftID := draft.ID
		cerberusRepoPath := projectRepoPath
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
			defer cancel()
			cerb.SetRepoPath(cerberusRepoPath)
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
		var projID int64
		var proj *db.Project
		if draft.ProjectID != nil {
			projID = *draft.ProjectID
			p, err := db.GetProject(r.Context(), s.pool, projID)
			if err != nil {
				jsonErr(w, err.Error(), http.StatusInternalServerError)
				return
			}
			proj = &p
			s.cerb.SetRepoPath(p.RepoPath)
		}
		if err := s.cerb.Close(r.Context(), draft.CerberusSession); err != nil {
			log.Printf("spec-builder close error: %v", err)
		}
		if err := s.cerb.Clean(r.Context(), draft.CerberusSession); err != nil {
			log.Printf("spec-builder clean error: %v", err)
		}
		db.DeleteCerberusEvents(r.Context(), s.pool, draft.CerberusSession)
		removeProfileFile(draft.CerberusSession)
		title := saveBody.Title
		if title == "" {
			title = extractSpecTitle(specContent)
		}
		if title == "" {
			title = draft.Title
		}
		if proj != nil {
			if _, err := writeSpecMarkdownToMemory(s.memoryRepoPath, proj.MemoryNamespace, draft.ID, title, specContent); err != nil {
				jsonErr(w, err.Error(), http.StatusInternalServerError)
				return
			}
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
			if draft.ProjectID != nil {
				if proj, err := db.GetProject(r.Context(), s.pool, *draft.ProjectID); err == nil {
					s.cerb.SetRepoPath(proj.RepoPath)
				}
			}
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

func writeSpecMarkdownToMemory(repoPath, namespace string, draftID int64, title, content string) (string, error) {
	repoPath = strings.TrimSpace(repoPath)
	namespace = strings.Trim(strings.TrimSpace(namespace), string(os.PathSeparator)+"/")
	if repoPath == "" {
		return "", fmt.Errorf("memory repo path is not configured")
	}
	if namespace == "" {
		return "", fmt.Errorf("project memory namespace is not configured")
	}

	repoRoot := filepath.Clean(repoPath)
	specDir := filepath.Clean(filepath.Join(repoRoot, filepath.FromSlash(namespace), "specs"))
	if rel, err := filepath.Rel(repoRoot, specDir); err != nil || strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
		return "", fmt.Errorf("invalid memory namespace %q", namespace)
	}
	if err := os.MkdirAll(specDir, 0o755); err != nil {
		return "", fmt.Errorf("create memory specs dir: %w", err)
	}

	base := fmt.Sprintf("draft-%d-%s", draftID, slugifySpecFilename(title))
	path := filepath.Join(specDir, base+".md")
	if err := os.WriteFile(path, []byte(strings.TrimSpace(content)+"\n"), 0o644); err != nil {
		return "", fmt.Errorf("write memory spec: %w", err)
	}
	return path, nil
}

func slugifySpecFilename(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	lastDash := false
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash && b.Len() > 0 {
			b.WriteByte('-')
			lastDash = true
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "spec"
	}
	return out
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
		jsonOK(w, map[string]bool{"success": true}, http.StatusOK)
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
