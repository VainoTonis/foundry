package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/tonis2/foundry/internal/db"
	"github.com/tonis2/foundry/internal/discover"
	"github.com/tonis2/foundry/internal/memory"
	"github.com/tonis2/foundry/internal/specdrafts"
)

// ---- server-rendered UI templates and helpers ----

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
	"diffSummary":      buildDiffSummary,
	"diffRows":         buildDiffRows,
	"logRows":          buildLogRows,
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
      <a href="/spec-builder" data-nav="builder" hx-get="/spec-builder/fragment" hx-target="#app" hx-push-url="/spec-builder">Draft Studio</a>
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
      <a class="btn btn-primary" href="/spec-builder" hx-get="/spec-builder/fragment" hx-target="#app" hx-push-url="/spec-builder">Open Draft Studio</a>
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
  {{else}}<div class="empty empty-action">No specs yet. Create a spec or use Draft Studio to explore intent before saving executable work.</div>{{end}}
  {{if .Drafts}}
    <div class="group-label">draft studio drafts</div>
    {{range .Drafts}}<article class="work-row"><div class="work-main"><a class="work-title" href="/spec-builder/{{.ID}}" hx-get="/spec-builder/{{.ID}}/fragment" hx-target="#app" hx-push-url="/spec-builder/{{.ID}}">{{.Title}}</a><div class="work-meta">draft created {{date .CreatedAt}}</div></div><div class="work-signals"><span class="chip chip-{{.Status}}">{{.Status}}</span></div><div class="work-next"><a class="btn" href="/spec-builder/{{.ID}}" hx-get="/spec-builder/{{.ID}}/fragment" hx-target="#app" hx-push-url="/spec-builder/{{.ID}}">Continue draft</a></div></article>{{end}}
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
  <details class="workflow-actions"><summary>Workflow actions and evidence</summary><div class="card-actions"><button class="btn" data-json-post="/api/workflows/{{.Workflow.ID}}/resume" data-refresh="/workflows/{{.Workflow.ID}}/fragment" data-target="#app">Resume</button>{{if eq .Workflow.Status "failed"}}<button class="btn btn-primary" data-json-post="/api/workflows/{{.Workflow.ID}}/follow-up" data-redirect-template="/workflows/{id}">Follow-up run</button>{{end}}<button class="btn btn-danger" data-json-post="/api/workflows/{{.Workflow.ID}}/stop" data-refresh="/workflows/{{.Workflow.ID}}/fragment" data-target="#app">Stop</button></div><div class="evidence-grid"><section><h3>Approved memory used</h3>{{if .MemoryError}}<div class="empty empty-error">{{.MemoryError}}</div>{{else if .Memory.Markdown}}<div class="card-meta">{{len .Memory.Files}} file(s) from {{.Memory.Root}}</div><pre class="doc-box">{{.Memory.Markdown}}</pre>{{else}}<div class="empty empty-action">No approved markdown memory found for this workflow's project namespace.</div>{{end}}</section><section class="memory-review"><h3>Memory update review</h3>{{if .MemoryUpdateError}}<div class="empty empty-error">{{.MemoryUpdateError}}</div>{{end}}{{if .MemoryUpdate}}<div class="memory-proposal"><div class="memory-proposal-head"><div><span class="eyebrow">Proposal #{{.MemoryUpdate.ID}}</span><h4>Durable project memory</h4></div><span class="chip chip-{{.MemoryUpdate.Status}}">{{.MemoryUpdate.Status}}</span></div><dl class="fact-list"><div><dt>Destination path</dt><dd>{{if .MemoryUpdate.MemoryPath}}{{.MemoryUpdate.MemoryPath}}{{else}}Pending acceptance; no approved file has been written.{{end}}</dd></div><div><dt>Reviewer comment</dt><dd>{{if .MemoryUpdate.ReviewerComment}}{{.MemoryUpdate.ReviewerComment}}{{else}}No reviewer comment yet.{{end}}</dd></div></dl><div class="section-title-row"><h3>Proposal markdown</h3><span class="chip chip-review">review before write</span></div><pre class="doc-box memory-markdown">{{.MemoryUpdate.ProposalMarkdown}}</pre><div class="memory-action-zone"><div class="card-actions"><button class="btn btn-primary" data-json-post="/api/workflows/{{.Workflow.ID}}/memory-update/accept" data-refresh="/workflows/{{.Workflow.ID}}/fragment" data-target="#app">Accept and write memory</button><button class="btn btn-danger" data-json-post="/api/workflows/{{.Workflow.ID}}/memory-update/reject" data-refresh="/workflows/{{.Workflow.ID}}/fragment" data-target="#app">Reject proposal</button></div><form data-json method="post" action="/api/workflows/{{.Workflow.ID}}/memory-update/revise" data-refresh="/workflows/{{.Workflow.ID}}/fragment" data-target="#app"><div class="field"><label>Revision comment</label><textarea name="comment" required placeholder="What should change before this becomes approved memory?"></textarea></div><button class="btn">Revise with comment</button></form></div></div>{{else}}<form class="memory-proposal" data-json method="post" action="/api/workflows/{{.Workflow.ID}}/memory-update" data-refresh="/workflows/{{.Workflow.ID}}/fragment" data-target="#app"><h4>Create a reviewable memory proposal</h4><p class="hint">Nothing is written to approved memory until you accept the generated markdown.</p><div class="field"><label>Feedback for memory</label><textarea name="feedback" placeholder="What durable context should be remembered from this workflow?"></textarea></div><button class="btn btn-primary">Create memory update proposal</button></form>{{end}}</section></div></details>
</div>
{{end}}

{{define "phaseLogs"}}
<div class="evidence-panel"><div class="evidence-head"><div><h3>Logs · Phase #{{.Phase.ID}} {{.Phase.Name}}</h3><p class="hint">Live activity stream. Rows stay inspectable while new events append.</p></div></div><div class="log-table" data-log-stream="/api/phases/{{.Phase.ID}}/logs/stream?after_id={{.LastLogID}}" data-log-last-id="{{.LastLogID}}"><div class="log-row log-row-head"><span>Time</span><span>Source</span><span>Event</span><span>State</span></div>{{range logRows .Logs}}<div class="log-row log-state-{{.State}}" data-log-id="{{.ID}}"><span class="log-time">{{.Time}}</span><span class="log-source">{{.Source}}</span><span class="log-event">{{.Event}}</span><span class="log-state"><span class="chip chip-{{.State}}">{{.State}}</span></span></div>{{else}}<div class="empty empty-action">No logs recorded for this phase yet. When agent activity starts, rows will appear here.</div>{{end}}</div></div>
{{end}}

{{define "phaseDiff"}}
<div class="evidence-panel"><div class="evidence-head"><div><h3>Diff · Phase #{{.Phase.ID}} {{.Phase.Name}}</h3>{{if not .Error}}{{with diffSummary .Diff}}<p class="hint"><strong>{{.Path}}</strong> · {{.Summary}} · <span class="diff-count-add">+{{.Added}}</span> <span class="diff-count-del">-{{.Removed}}</span></p>{{end}}{{end}}</div></div>{{if .Error}}<div class="empty empty-error">{{.Error}}</div>{{else if not .Diff}}<div class="empty empty-action">No diff is available for this phase yet. Run or resume the workflow, then return here to inspect changed code.</div>{{else}}{{with diffSummary .Diff}}<div class="diff-surface" role="table" aria-label="Code diff"><div class="diff-row diff-row-head" role="row"><span>Line</span><span>Code</span></div>{{range diffRows $.Diff}}<div class="diff-row diff-kind-{{.Kind}}" role="row"><span class="diff-gutter">{{.Marker}}</span><code>{{.Text}}</code></div>{{end}}</div><div class="diff-summary-grid"><div><span>Added lines</span><strong>+{{.Added}}</strong></div><div><span>Removed lines</span><strong>-{{.Removed}}</strong></div><div><span>Conflicts</span><strong>{{.Conflicts}}</strong></div><div><span>DOM hooks touched</span><strong>{{.DOMHooks}}</strong></div></div>{{end}}{{end}}</div>
{{end}}

{{define "builderStart"}}
<div data-page="builder">
  <div class="page-header command-header"><div><p class="eyebrow">Draft Studio</p><h2>Explore intent before it becomes a spec</h2><p class="hint">Choose a project, steer the assistant through goals, constraints, and phase shape, then save only when the markdown matches your intent.</p></div></div>
  <div class="builder-start-grid"><form class="panel-form builder-start-form" data-json method="post" action="/api/spec-drafts" data-redirect-template="/spec-builder/{id}">
    <h3>Start a draft</h3>
    <div class="field"><label>Project</label><select name="project_id" required>{{range .Projects}}<option value="{{.ID}}">{{.Name}}</option>{{end}}</select><p class="hint">Approved memory from this project's namespace is used as context.</p></div>
    <div class="field"><label>What intent should we explore?</label><textarea name="description" required placeholder="Describe the goal, constraints, open questions, evidence needed, and possible phases."></textarea><p class="hint">Use the conversation to steer the draft; for runnable work, ask for visible ## Phase N: headings before saving.</p></div>
    <button class="btn btn-primary">Start draft</button>
  </form><aside class="context-panel"><h3>How Draft Studio helps</h3><dl class="fact-list"><div><dt>Input</dt><dd>Your project, initial intent, and approved memory.</dd></div><div><dt>Exploration</dt><dd>Steer goals, scope, constraints, evidence, and phase boundaries in conversation.</dd></div><div><dt>Safe point</dt><dd>The draft is not executable backlog work until you save it as a spec.</dd></div></dl></aside></div>
  {{if .Drafts}}<div class="group-label">Resume active drafts</div><div class="worklist">{{range .Drafts}}<article class="work-row"><div class="work-main"><a class="work-title" href="/spec-builder/{{.ID}}" hx-get="/spec-builder/{{.ID}}/fragment" hx-target="#app" hx-push-url="/spec-builder/{{.ID}}">{{.Title}}</a><div class="work-meta">updated {{datetime .UpdatedAt}}</div></div><div class="work-signals"><span class="chip chip-{{.Status}}">{{.Status}}</span></div><div class="work-next"><a class="btn btn-primary" href="/spec-builder/{{.ID}}" hx-get="/spec-builder/{{.ID}}/fragment" hx-target="#app" hx-push-url="/spec-builder/{{.ID}}">Continue</a></div></article>{{end}}</div>{{else}}<div class="empty empty-action">No active drafts. Start in Draft Studio to explore and refine intent before it enters the backlog.</div>{{end}}
</div>
{{end}}

{{define "draftMessages"}}{{range .Messages}}<div class="chat-msg chat-msg-{{.Role}}"><div class="chat-msg-label">{{.Role}}</div><div class="chat-msg-body">{{.Content}}</div></div>{{end}}{{end}}

{{define "builderDetail"}}
<div data-page="builder" data-draft-stream="/api/spec-drafts/{{.Draft.ID}}/stream" data-draft-id="{{.Draft.ID}}">
  <div class="context-nav"><a class="btn" href="/spec-builder" hx-get="/spec-builder/fragment" hx-target="#app" hx-push-url="/spec-builder">← Draft Studio</a>{{if .HasProject}}<a class="btn" href="/projects/{{.Project.ID}}" hx-get="/projects/{{.Project.ID}}/fragment" hx-target="#app" hx-push-url="/projects/{{.Project.ID}}">Project: {{.Project.Name}}</a>{{end}}</div>
  <div class="page-header spec-hero"><div><p class="eyebrow">Draft #{{.Draft.ID}}</p><h2>{{.Draft.Title}}</h2><div class="card-meta">{{if .HasProject}}{{.Project.Name}} · {{end}}{{.Draft.Status}} · updated {{datetime .Draft.UpdatedAt}}</div></div><div class="card-actions"><button class="btn btn-primary" data-json-post="/api/spec-drafts/{{.Draft.ID}}/save" data-body='{"title":""}' data-redirect-template="/specs/{spec_id}">Save as spec</button><button class="btn btn-danger" data-json-delete="/api/spec-drafts/{{.Draft.ID}}" data-redirect="/backlog">Abandon</button></div></div>
  <div class="builder-status-slab"><span class="chip chip-{{.Draft.Status}}">{{.Draft.Status}}</span><strong>Steer the assistant until the intent, scope, and phases are ready to save as a spec.</strong><span class="hint">Streaming errors remain in the page error region and chat.</span></div>
  <div class="spec-builder-layout"><section class="spec-builder-chat panel-form"><div class="section-title-row"><h3>Draft Studio conversation</h3><span class="chip chip-streaming">live draft</span></div><div id="draft-messages" class="chat-messages">{{template "draftMessages" .}}</div><div id="draft-stream" class="chat-msg-streaming"></div><form data-json data-draft-message method="post" action="/api/spec-drafts/{{.Draft.ID}}/message"><div class="chat-input-row"><textarea class="chat-textarea" name="content" required placeholder="Steer the draft with goals, constraints, corrections, or clearer phases…"></textarea><button class="btn btn-primary">Send</button></div></form></section><aside class="spec-preview-pane"><div class="section-title-row"><h3>Latest generated spec preview</h3><span class="chip chip-pending">review before save</span></div><pre id="draft-preview" class="doc-box spec-doc">{{if .Preview}}{{.Preview}}{{else}}Steer Draft Studio to produce a full markdown spec preview. Include ## Phase N: headings when the work should become executable.{{end}}</pre></aside></div>
  <details class="section memory-details"><summary>Approved memory used</summary>{{if .MemoryError}}<div class="empty empty-error">{{.MemoryError}}</div>{{else if .Memory.Markdown}}<div class="card-meta">{{len .Memory.Files}} file(s) from {{.Memory.Root}}</div><pre class="doc-box">{{.Memory.Markdown}}</pre>{{else}}<div class="empty empty-action">No project memory loaded for this draft.</div>{{end}}</details>
</div>
{{end}}

{{define "settings"}}
<div data-page="settings">
  <div class="context-nav"><a class="btn" href="/backlog" hx-get="/backlog/fragment" hx-target="#app" hx-push-url="/backlog">← Backlog</a><a class="btn" href="/projects" hx-get="/projects/fragment" hx-target="#app" hx-push-url="/projects">Projects</a><a class="btn" href="/spec-builder" hx-get="/spec-builder/fragment" hx-target="#app" hx-push-url="/spec-builder">Draft Studio</a></div>
  <div class="page-header command-header"><div><p class="eyebrow">Foundry / settings</p><h2>Runtime controls</h2><p class="hint">Edit config, inspect Cerberus sessions, and manage execution profiles. Errors remain in the page alert region above.</p></div></div>
  <section class="settings-grid" aria-label="Settings workbench">
    <form class="panel-form settings-config" data-settings action="/api/settings" data-refresh="/settings/fragment" data-target="#app">
      <div class="section-title-row"><h3>Settings</h3><span class="chip chip-warning">non-runtime changes may require restart</span></div>
      {{range .Settings}}{{if and (not .IsVerbosity) (not .IsCerberusProfile)}}<div class="field"><label>{{.Key}}</label><input name="{{.Key}}" value="{{.Value}}"><p class="hint">{{if .IsRuntime}}DB runtime setting{{else}}config.yaml key{{end}}: {{.Key}}</p></div>{{end}}{{end}}
      <div class="field"><label for="cerberus-profile">Profiles</label><select id="cerberus-profile" name="cerberus_profile" data-include-empty><option value="" {{if eq .CerberusProfile ""}}selected{{end}}>No profile</option>{{if and .CerberusProfile (not .CerberusProfileExists)}}<option value="{{.CerberusProfile}}" selected>{{.CerberusProfile}} (configured)</option>{{end}}{{range .Profiles}}<option value="{{.Name}}" {{if eq .Name $.CerberusProfile}}selected{{end}}>{{.Name}}</option>{{end}}</select><p class="hint">DB runtime setting: cerberus_profile. Select a saved execution profile or choose no profile.</p></div>
      <div class="field"><label for="verbosity-level">Verbosity level</label>{{if .HasVerbosity}}<select id="verbosity-level" name="{{.VerbosityKey}}"><option value="quiet" {{if eq .VerbosityValue "quiet"}}selected{{end}}>Quiet</option><option value="normal" {{if eq .VerbosityValue "normal"}}selected{{end}}>Normal</option><option value="verbose" {{if eq .VerbosityValue "verbose"}}selected{{end}}>Verbose</option></select>{{else}}<select id="verbosity-level" disabled><option>Normal</option></select><p class="hint">Static UI placeholder: this install has no verbosity key in config.yaml yet.</p>{{end}}</div>
      <p class="hint">Runtime settings are saved in the database and take effect immediately. Only non-runtime keys are written to config.yaml and may require a restart.</p>
      <button class="btn btn-primary">Save settings</button>
    </form>
    <aside class="context-panel settings-help"><h3>Safe operation</h3><dl class="fact-list"><div><dt>Primary action</dt><dd>Save runtime DB settings, non-runtime config, or one profile at a time.</dd></div><div><dt>Danger action</dt><dd>Deleting profiles is explicit and red. Protected sessions cannot be cleaned.</dd></div><div><dt>Failure handling</dt><dd>Request failures stay visible in the alert region and also appear as a toast.</dd></div></dl></aside>
  </section>
  <section class="settings-section"><div class="section-title-row"><h3>Cerberus sessions</h3><span class="chip chip-pending">cleanup audit</span></div>{{if .SessionError}}<div class="empty empty-error">{{.SessionError}}</div>{{end}}
  {{if .Sessions}}<div class="worklist">{{range .Sessions}}<article class="work-row"><div class="work-main"><span class="work-title">{{.Session}}</span><div class="work-meta">{{.Type}}{{if .ProjectName}} · {{.ProjectName}}{{end}}{{if .SpecTitle}} · spec: {{.SpecTitle}}{{end}}{{if .PhaseName}} · phase: {{.PhaseName}}{{end}}{{if .DraftTitle}} · draft: {{.DraftTitle}}{{end}} · updated {{datetime .LastUpdatedAt}}</div><div class="work-meta">Cerberus: {{if .CerberusStatus}}{{.CerberusStatus}}{{else}}unknown{{end}}{{if .CerberusError}} · {{.CerberusError}}{{end}}{{if .UnsafeReason}} · {{.UnsafeReason}}{{end}}</div></div><div class="work-signals"><span class="chip chip-{{.FoundryStatus}}">{{.FoundryStatus}}</span>{{if .SafeToClean}}<span class="chip chip-done">safe cleanup</span>{{else}}<span class="chip chip-running">active / protected</span>{{end}}</div><div class="work-next"><button class="btn" data-json-post="{{cleanSessionURL .Session}}" data-refresh="/settings/fragment" data-target="#app" {{if not .SafeToClean}}disabled title="{{.UnsafeReason}}"{{end}}>Clean session</button></div></article>{{end}}</div>{{else}}<div class="empty empty-action">No Foundry-known Cerberus sessions. Active work will appear here when workflows or drafts use Cerberus.</div>{{end}}</section>
  <section class="settings-section"><div class="section-title-row"><h3>Profiles</h3><span class="chip chip-pending">execution defaults</span></div>
  <form class="panel-form profile-create" data-json method="post" action="/api/profiles" data-refresh="/settings/fragment" data-target="#app">
    <h3>Create profile</h3>
    <div class="form-grid"><div class="field"><label>Name</label><input name="name" required></div><div class="field"><label>Default model</label><input name="default_model"></div><div class="field"><label>Default image</label><input name="default_image"></div><div class="field"><label>AWS profile</label><input name="aws_profile"></div><div class="field"><label>AWS region</label><input name="aws_region"></div></div>
    <div class="field"><label>Extra env (JSON)</label><textarea name="extra_env" placeholder='{"KEY":"value"}'></textarea><p class="hint">Invalid JSON is rejected before sending and remains visible in the error region.</p></div>
    <button class="btn btn-primary">Create profile</button>
  </form>
  {{if .Profiles}}<div class="profile-list">{{range .Profiles}}<article class="project-slab"><form data-json data-include-empty data-method="PATCH" action="/api/profiles/{{.ID}}" data-refresh="/settings/fragment" data-target="#app"><div class="project-head"><span class="project-title">{{.Name}}</span><span class="chip chip-pending">profile</span></div><div class="form-grid"><div class="field"><label>Name</label><input name="name" value="{{.Name}}" required></div><div class="field"><label>Default model</label><input name="default_model" value="{{.DefaultModel}}"></div><div class="field"><label>Default image</label><input name="default_image" value="{{.DefaultImage}}"></div><div class="field"><label>AWS profile</label><input name="aws_profile" value="{{.AWSProfile}}"></div><div class="field"><label>AWS region</label><input name="aws_region" value="{{.AWSRegion}}"></div></div><div class="field"><label>Extra env (JSON)</label><textarea name="extra_env">{{json .ExtraEnv}}</textarea></div><div class="card-actions"><button class="btn btn-primary">Save profile</button><button class="btn btn-danger" type="button" data-json-delete="/api/profiles/{{.ID}}" data-refresh="/settings/fragment" data-target="#app">Delete profile</button></div></form></article>{{end}}</div>{{else}}<div class="empty empty-action">No profiles saved. Create one to make model/image/env choices explicit for future agent runs.</div>{{end}}</section>
</div>
{{end}}
`))

// Template helper functions
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

// Diff display helpers
type uiDiffSummary struct {
	Path      string
	Summary   string
	Added     int
	Removed   int
	Conflicts int
	DOMHooks  int
}

type uiDiffRow struct{ Kind, Marker, Text string }

type uiLogRow struct {
	ID                  int64
	Time, Source, Event string
	State               string
}

func buildDiffSummary(raw string) uiDiffSummary {
	s := uiDiffSummary{Path: "unknown file", Summary: "No changed lines", DOMHooks: 0}
	for _, line := range strings.Split(raw, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(line, "+++ b/") && s.Path == "unknown file" {
			s.Path = strings.TrimPrefix(strings.TrimSpace(line), "+++ b/")
		} else if strings.HasPrefix(line, "diff --git ") && s.Path == "unknown file" {
			parts := strings.Fields(line)
			if len(parts) >= 4 {
				s.Path = strings.TrimPrefix(parts[3], "b/")
			}
		}
		if strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++") {
			s.Added++
			if diffLineTouchesDOMHook(line) {
				s.DOMHooks++
			}
		} else if strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "---") {
			s.Removed++
			if diffLineTouchesDOMHook(line) {
				s.DOMHooks++
			}
		}
		if strings.HasPrefix(trimmed, "<<<<<<<") || strings.HasPrefix(trimmed, "=======") || strings.HasPrefix(trimmed, ">>>>>>>") {
			s.Conflicts++
		}
	}
	if s.Added > 0 || s.Removed > 0 {
		s.Summary = fmt.Sprintf("%d changed lines", s.Added+s.Removed)
	}
	return s
}

func buildDiffRows(raw string) []uiDiffRow {
	lines := strings.Split(raw, "\n")
	rows := make([]uiDiffRow, 0, len(lines))
	for _, line := range lines {
		row := uiDiffRow{Kind: "context", Marker: " ", Text: line}
		switch {
		case strings.HasPrefix(line, "@@"):
			row.Kind, row.Marker = "hunk", "@"
		case strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++"):
			row.Kind, row.Marker, row.Text = "add", "+", strings.TrimPrefix(line, "+")
		case strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "---"):
			row.Kind, row.Marker, row.Text = "del", "-", strings.TrimPrefix(line, "-")
		case strings.HasPrefix(line, "diff --git") || strings.HasPrefix(line, "+++") || strings.HasPrefix(line, "---"):
			row.Kind, row.Marker = "meta", "•"
		}
		rows = append(rows, row)
	}
	return rows
}

func diffLineTouchesDOMHook(line string) bool {
	lower := strings.ToLower(line)
	for _, token := range []string{"data-", "id=", "hx-", "aria-", "class="} {
		if strings.Contains(lower, token) {
			return true
		}
	}
	return false
}

func buildLogRows(logs []db.PhaseLog) []uiLogRow {
	rows := make([]uiLogRow, 0, len(logs))
	for _, l := range logs {
		source, event := splitLogSource(l.Line)
		rows = append(rows, uiLogRow{ID: l.ID, Time: l.Ts.Format("2006-01-02 15:04:05"), Source: source, Event: event, State: classifyLogState(l.Line)})
	}
	return rows
}

func splitLogSource(line string) (string, string) {
	text := strings.TrimSpace(line)
	if text == "" {
		return "system", "—"
	}
	if strings.HasPrefix(text, "[") {
		if end := strings.Index(text, "]"); end > 1 && end < 32 {
			return strings.ToLower(strings.TrimSpace(text[1:end])), strings.TrimSpace(text[end+1:])
		}
	}
	if idx := strings.Index(text, ":"); idx > 0 && idx < 24 {
		prefix := strings.TrimSpace(text[:idx])
		if !strings.Contains(prefix, " ") {
			return strings.ToLower(prefix), strings.TrimSpace(text[idx+1:])
		}
	}
	if strings.Contains(strings.ToLower(text), "system") {
		return "system", text
	}
	return "agent", text
}

func classifyLogState(line string) string {
	lower := strings.ToLower(line)
	switch {
	case strings.Contains(lower, "blocked"):
		return "blocked"
	case strings.Contains(lower, "error") || strings.Contains(lower, "failed") || strings.Contains(lower, "fail"):
		return "error"
	case strings.Contains(lower, "warn") || strings.Contains(lower, "warning"):
		return "warning"
	case strings.Contains(lower, "done") || strings.Contains(lower, "complete"):
		return "done"
	case strings.Contains(lower, "running") || strings.Contains(lower, "started"):
		return "running"
	default:
		return "normal"
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

// ---- UI route handlers ----

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
	mem, err := memory.LoadApproved(repoPath, p.MemoryNamespace, nil)
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
	gitRoot, memoryRepoPath, _ := s.runtimeSettings()
	projectRows := make([]uiProjectRow, 0, len(projects))
	for _, p := range projects {
		state, class := projectMemoryState(memoryRepoPath, p)
		projectRows = append(projectRows, uiProjectRow{Project: p, MemoryState: state, MemoryClass: class})
	}
	var repos []uiRepoItem
	var discoverErr string
	if r.URL.Query().Get("discover") == "1" {
		if gitRoot == "" {
			discoverErr = "git_root not configured"
		} else if found, err := discover.FindRepos(gitRoot); err != nil {
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
	}{projectRows, repos, discoverErr, memoryRepoPath}
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
	_, memoryRepoPath, _ := s.runtimeSettings()
	mem, memErr := memory.LoadApproved(memoryRepoPath, p.MemoryNamespace, nil)
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
	_, memoryRepoPath, _ := s.runtimeSettings()
	mem, memErr := memory.LoadApproved(memoryRepoPath, proj.MemoryNamespace, nil)
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
	preview := specdrafts.ExtractFinalSpec(draft.Messages)
	var mem memory.Slice
	var memErrMsg string
	var proj db.Project
	hasProject := false
	if draft.ProjectID != nil {
		if p, err := db.GetProject(r.Context(), s.pool, *draft.ProjectID); err == nil {
			proj = p
			hasProject = true
			_, memoryRepoPath, _ := s.runtimeSettings()
			if loaded, err := memory.LoadApproved(memoryRepoPath, proj.MemoryNamespace, nil); err == nil {
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
	runtimeValues, err := s.loadRuntimeSettings(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	cerberusProfile := runtimeValues["cerberus_profile"]
	mergedConfig := mergeYAMLRuntimeSettings(string(data), runtimeValues)
	profiles, _ := db.ListProfiles(r.Context(), s.pool)
	sessions, sessionErr := s.knownCerberusSessionViews(r.Context(), true)
	sessionErrMsg := ""
	if sessionErr != nil {
		sessionErrMsg = sessionErr.Error()
	}
	type setting struct {
		Key, Value        string
		IsVerbosity       bool
		IsCerberusProfile bool
		IsRuntime         bool
	}
	var settings []setting
	var verbosityKey, verbosityValue string
	foundCerberusProfile := false
	for _, line := range strings.Split(mergedConfig, "\n") {
		parts := strings.SplitN(line, ":", 2)
		if len(parts) == 2 && strings.TrimSpace(parts[0]) != "" {
			key := strings.TrimSpace(parts[0])
			value := strings.Trim(strings.TrimSpace(parts[1]), "\"")
			isVerbosity := key == "verbosity" || key == "ui_verbosity" || key == "log_verbosity"
			isCerberusProfile := key == "cerberus_profile"
			if isVerbosity && verbosityKey == "" {
				verbosityKey, verbosityValue = key, value
			}
			if isCerberusProfile {
				cerberusProfile = value
				foundCerberusProfile = true
			}
			settings = append(settings, setting{Key: key, Value: value, IsVerbosity: isVerbosity, IsCerberusProfile: isCerberusProfile, IsRuntime: isRuntimeSetting(key)})
		}
	}
	if !foundCerberusProfile && cerberusProfile == "" {
		cerberusProfile = ""
	}
	cerberusProfileExists := cerberusProfile == ""
	for _, p := range profiles {
		if p.Name == cerberusProfile {
			cerberusProfileExists = true
			break
		}
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := uiTemplates.ExecuteTemplate(w, "settings", struct {
		Settings              []setting
		Profiles              []db.Profile
		Sessions              []cerberusSessionView
		SessionError          string
		HasVerbosity          bool
		VerbosityKey          string
		VerbosityValue        string
		CerberusProfile       string
		CerberusProfileExists bool
	}{settings, profiles, sessions, sessionErrMsg, verbosityKey != "", verbosityKey, verbosityValue, cerberusProfile, cerberusProfileExists}); err != nil {
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
