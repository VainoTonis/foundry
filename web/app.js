// Foundry UI — vanilla JS, no framework
'use strict';

const api = {
  async get(path) {
    const r = await fetch(path);
    if (!r.ok) throw new Error((await r.json()).error || r.statusText);
    return r.json();
  },
  async post(path, body) {
    const r = await fetch(path, { method: 'POST', headers: {'Content-Type':'application/json'}, body: JSON.stringify(body) });
    if (!r.ok) throw new Error((await r.json()).error || r.statusText);
    return r.json();
  },
  async patch(path, body) {
    const r = await fetch(path, { method: 'PATCH', headers: {'Content-Type':'application/json'}, body: JSON.stringify(body) });
    if (!r.ok) throw new Error((await r.json()).error || r.statusText);
    return r.json();
  },
};

// ---- router ----
const app = document.getElementById('app');
let _view = null;

function navigate(view, params = {}) {
  _view = { view, params };
  render();
}

function render() {
  const { view, params } = _view;
  app.innerHTML = '';
  document.querySelectorAll('nav a').forEach(a => a.classList.remove('active'));
  if (view === 'backlog') { document.getElementById('nav-backlog').classList.add('active'); renderBacklog(); }
  else if (view === 'projects') { document.getElementById('nav-projects').classList.add('active'); renderProjects(); }
  else if (view === 'settings') { document.getElementById('nav-settings').classList.add('active'); renderSettings(); }
  else if (view === 'spec') renderSpec(params.id);
  else if (view === 'spec_builder') renderSpecBuilder(params.id);
  else if (view === 'workflow') renderWorkflow(params.id);
}

// ---- Backlog ----
async function renderBacklog() {
  const groups = ['running', 'queued', 'paused', 'done', 'failed', 'dumpster'];
  const specs = await api.get('/api/specs').catch(() => []);
  const projects = await api.get('/api/projects').catch(() => []);
  const projectMap = Object.fromEntries((projects || []).map(p => [p.id, p]));

  const header = el('div', { style: 'display:flex;align-items:center;justify-content:space-between;margin-bottom:1.25rem' });
  const title = el('h2', {}, 'Backlog');
  const actions = el('div', { style: 'display:flex;gap:.5rem' });
  const btnProj = btn('+ Project', () => showCreateProject());
  const btnSpec = btn('+ Spec', 'btn-primary', () => showCreateSpec(projects));
  const btnBuild = btn('Build with AI', '', () => navigate('spec_builder', {}));
  actions.append(btnProj, btnSpec, btnBuild);
  header.append(title, actions);
  app.append(header);

  const byStatus = {};
  for (const s of (specs || [])) {
    (byStatus[s.status] = byStatus[s.status] || []).push(s);
  }

  let any = false;
  for (const status of groups) {
    const items = byStatus[status] || [];
    if (!items.length) continue;
    any = true;
    app.append(el('div', { className: 'group-label' }, status));
    for (const s of items) {
      const proj = projectMap[s.project_id];
      const card = el('div', { className: 'card' });
      const hdr = el('div', { className: 'card-header' });
      const title = el('span', { className: 'card-title' }, s.title);
      title.onclick = () => navigate('spec', { id: s.id });
      hdr.append(title, chip(s.track), chip(s.status));
      const meta = el('div', { className: 'card-meta' },
        (proj ? proj.name + ' · ' : '') + fmtDate(s.created_at));
      const actions = el('div', { className: 'card-actions' });
      if (s.status === 'dumpster' || s.status === 'queued') {
        const run = btn('Run PoC', 'btn-primary', async () => {
          try {
            await api.post('/api/workflows', { spec_id: s.id });
            navigate('backlog');
          } catch(e) { alert(e.message); }
        });
        actions.append(run);
      }
      card.append(hdr, meta, actions);
      app.append(card);
    }
  }
  if (!any) app.append(el('div', { className: 'empty' }, 'No specs yet. Create one to get started.'));

  // active spec drafts
  const drafts = await api.get('/api/spec-drafts').catch(() => []);
  const activeDrafts = (drafts || []).filter(d => d.status === 'active');
  if (activeDrafts.length > 0) {
    app.append(el('div', { className: 'group-label', style: 'margin-top:1.5rem' }, 'spec builder drafts'));
    for (const d of activeDrafts) {
      const card = el('div', { className: 'card' });
      const hd = el('div', { className: 'card-header' });
      const title = el('span', { className: 'card-title' }, d.title || '(untitled draft)');
      title.onclick = () => navigate('spec_builder', { id: d.id });
      hd.append(title, chip('active'));
      const meta = el('div', { className: 'card-meta' }, fmtDate(d.created_at));
      const actions = el('div', { className: 'card-actions' });
      actions.append(btn('Resume', 'btn-primary', () => navigate('spec_builder', { id: d.id })));
      card.append(hd, meta, actions);
      app.append(card);
    }
  }
}

// ---- Spec detail ----
async function renderSpec(id) {
  let spec = await api.get(`/api/specs/${id}`).catch(e => { app.append(el('div', {className:'empty'}, e.message)); return null; });
  if (!spec) return;

  app.append(el('span', { className: 'back', onclick: () => navigate('backlog') }, '← Backlog'));

  const hdr = el('div', { style: 'display:flex;align-items:center;gap:.6rem;margin-bottom:1rem' });
  hdr.append(el('h2', { style: 'flex:1' }, spec.title), chip(spec.track), chip(spec.status));
  app.append(hdr);

  // edit form
  const section = el('div', { className: 'section' });
  section.append(el('h3', {}, 'Spec content'));
  const titleInput = input('text', spec.title);
  const contentTA = textarea(spec.content);
  const saveBtn = btn('Save', 'btn-primary', async () => {
    try {
      spec = await api.patch(`/api/specs/${id}`, { title: titleInput.value, content: contentTA.value });
      alert('Saved');
    } catch(e) { alert(e.message); }
  });
  const promoteBtn = btn('Promote to Polish', '', async () => {
    try {
      spec = await api.post(`/api/specs/${id}/promote`);
      navigate('spec', { id });
    } catch(e) { alert(e.message); }
  });
  const runBtn = btn('Run Workflow', 'btn-primary', async () => {
    try {
      const wf = await api.post('/api/workflows', { spec_id: spec.id });
      navigate('workflow', { id: wf.id });
    } catch(e) { alert(e.message); }
  });
  const fieldTitle = el('div', { className: 'field' });
  fieldTitle.append(el('label', {}, 'Title'), titleInput);
  const fieldContent = el('div', { className: 'field' });
  const hint = el('div', { style: 'font-size:.75rem;color:var(--muted);margin-bottom:.3rem' },
    'Needs at least one phase: ## Phase 1: Name');
  fieldContent.append(el('label', {}, 'Content (markdown)'), hint, contentTA);
  section.append(fieldTitle, fieldContent,
    el('div', { style: 'display:flex;gap:.5rem' }, saveBtn, promoteBtn, runBtn));
  app.append(section);

  // workflows
  const wfSection = el('div', { className: 'section' });
  wfSection.append(el('h3', {}, 'Workflow runs'));
  const wfList = el('div', { id: 'wf-list' });
  wfList.append(el('div', { className: 'empty' }, 'Loading...'));
  wfSection.append(wfList);
  app.append(wfSection);

  api.get(`/api/specs/${id}/workflows`).then(wfs => {
    wfList.innerHTML = '';
    if (!wfs || !wfs.length) {
      wfList.append(el('div', { className: 'empty' }, 'No runs yet.'));
      return;
    }
    for (const wf of wfs) {
      const row = el('div', { className: 'card', style: 'cursor:pointer', onclick: () => navigate('workflow', { id: wf.id }) });
      const hd = el('div', { className: 'card-header' });
      hd.append(el('span', { className: 'card-title' }, `Workflow #${wf.id}`), chip(wf.track), chip(wf.status));
      const meta = el('div', { className: 'card-meta' }, fmtDate(wf.created_at) + (wf.finished_at ? ' → ' + fmtDate(wf.finished_at) : ' (running)'));
      row.append(hd, meta);
      wfList.append(row);
    }
  }).catch(() => { wfList.innerHTML = '<div class="empty">Could not load workflows.</div>'; });
}

// ---- Workflow detail ----
let _workflowSSE = null;

async function renderWorkflow(id) {
  // close any previous workflow SSE
  if (_workflowSSE) { _workflowSSE.close(); _workflowSSE = null; }

  const wf = await api.get(`/api/workflows/${id}`).catch(e => { app.append(el('div',{className:'empty'},e.message)); return null; });
  if (!wf) return;
  const phases = await api.get(`/api/workflows/${id}/phases`).catch(() => []);

  app.append(el('span', { className: 'back', onclick: () => { if (_workflowSSE) { _workflowSSE.close(); _workflowSSE = null; } navigate('backlog'); } }, '← Backlog'));

  // header with updatable status chip
  const hdr = el('div', { style: 'display:flex;align-items:center;gap:.6rem;margin-bottom:1rem' });
  const statusChip = chip(wf.status);
  hdr.append(el('h2', { style: 'flex:1' }, `Workflow #${wf.id}`), chip(wf.track), statusChip);
  app.append(hdr);

  // action bar — stop or resume
  const actionBar = el('div', { style: 'display:flex;gap:.5rem;margin-bottom:1rem' });
  app.append(actionBar);

  function updateActionBar(status) {
    actionBar.innerHTML = '';
    if (status === 'running') {
      actionBar.append(btn('Stop', 'btn-danger', async () => {
        if (!confirm('Stop this workflow?')) return;
        try { await api.post(`/api/workflows/${id}/stop`); } catch(e) { alert(e.message); }
      }));
    } else if (status === 'paused' || status === 'failed') {
      actionBar.append(btn('Resume', 'btn-primary', async () => {
        try { await api.post(`/api/workflows/${id}/resume`); navigate('workflow', { id }); }
        catch(e) { alert(e.message); }
      }));
    }
  }
  updateActionBar(wf.status);

  // phase tracking for live updates
  const phaseElements = new Map(); // phaseID -> { row, chipEl, ph }
  const expandedPhases = new Map(); // phaseID -> { logBox, detailEl }

  for (const ph of (phases || [])) {
    const row = el('div', { className: 'phase-row', onclick: () => togglePhaseDetail(ph, row, id, expandedPhases) });
    const pos = el('div', { className: 'phase-pos' }, `P${ph.position}`);
    const body = el('div', { className: 'phase-body' });
    const nameRow = el('div', { style: 'display:flex;align-items:center;gap:.5rem' });
    const phaseChip = chip(ph.status);
    nameRow.append(el('span', { className: 'phase-name' }, ph.name), phaseChip);
    if (ph.review_verdict) nameRow.append(chip(ph.review_verdict));
    body.append(nameRow);
    row.append(pos, body);
    row._expanded = false;
    row._ph = ph;
    app.append(row);
    phaseElements.set(ph.id, { row, chipEl: phaseChip, nameRow, ph });
  }

  if (!phases || !phases.length) {
    const msg = wf.status === 'paused'
      ? 'No phases found — spec content needs ## Phase 1: sections. Edit the spec and resume.'
      : 'No phases yet.';
    app.append(el('div', { className: 'empty' }, msg));
  }

  // SSE for live updates
  if (wf.status === 'running') {
    const es = new EventSource(`/api/workflows/${id}/stream`);
    _workflowSSE = es;

    es.addEventListener('log', e => {
      const d = JSON.parse(e.data);
      const ep = expandedPhases.get(d.phase_id);
      if (ep) {
        appendLog(ep.logBox, { line: d.line, ts: d.ts });
        ep.logBox.scrollTop = ep.logBox.scrollHeight;
      }
    });

    es.addEventListener('phase_update', e => {
      const d = JSON.parse(e.data);
      const pe = phaseElements.get(d.phase_id);
      if (pe) {
        const newChip = chip(d.status);
        pe.chipEl.replaceWith(newChip);
        pe.chipEl = newChip;
        pe.ph.status = d.status;
        // auto-expand running phase
        if (d.status === 'running' && !pe.row._expanded) {
          pe.row.click();
        }
      }
    });

    es.addEventListener('workflow_update', e => {
      const d = JSON.parse(e.data);
      const newChip = chip(d.status);
      statusChip.replaceWith(newChip);
      updateActionBar(d.status);
      if (d.status === 'done' || d.status === 'paused' || d.status === 'failed') {
        es.close();
        _workflowSSE = null;
        expandedPhases.clear();
        phaseElements.clear();
      }
    });

    es.onerror = () => { es.close(); _workflowSSE = null; expandedPhases.clear(); phaseElements.clear(); };
  }
}

function togglePhaseDetail(ph, row, workflowId, expandedPhases) {
  if (row._expanded) {
    const det = row.nextSibling;
    if (det && det._isDetail) det.remove();
    row._expanded = false;
    expandedPhases.delete(ph.id);
    return;
  }
  row._expanded = true;
  const det = el('div', { style: 'padding:.75rem 1rem 1rem 3rem;margin-bottom:.5rem;background:var(--surface);border:1px solid var(--border);border-radius:0 0 6px 6px' });
  det._isDetail = true;

  if (ph.goal) {
    const gs = el('div', { className: 'section' });
    gs.append(el('h3', {}, 'Goal'), el('pre', { style: 'white-space:pre-wrap;font-size:.82rem;color:var(--muted)' }, ph.goal));
    det.append(gs);
  }

  // actions
  const acts = el('div', { style: 'display:flex;gap:.5rem;margin-bottom:1rem;flex-wrap:wrap' });
  const approveBtn = btn('Approve', 'btn-primary', async () => {
    await api.post(`/api/phases/${ph.id}/approve`).catch(e => alert(e.message));
    navigate('workflow', { id: ph.workflow_id });
  });
  const rejectBtn = btn('Reject', 'btn-danger', async () => {
    await api.post(`/api/phases/${ph.id}/reject`).catch(e => alert(e.message));
    navigate('workflow', { id: ph.workflow_id });
  });
  const diffBtn = btn('Show Diff', '', async () => {
    const text = await fetch(`/api/phases/${ph.id}/diff`).then(r => r.text()).catch(e => e.message);
    renderDiff(det, text);
  });
  const cleanBtn = btn('Clean session', 'btn-danger', async () => {
    if (!confirm(`Clean cerberus session for phase ${ph.position}?`)) return;
    await api.post(`/api/phases/${ph.id}/clean`).catch(e => alert(e.message));
    navigate('workflow', { id: ph.workflow_id });
  });
  acts.append(approveBtn, rejectBtn, diffBtn);
  if (ph.cerberus_session) acts.append(cleanBtn);
  det.append(acts);

  // session name
  if (ph.cerberus_session) {
    det.append(el('div', { style: 'font-size:.75rem;color:var(--muted);margin-bottom:.75rem' },
      'Session: ' + ph.cerberus_session));
  }

  // decision record
  if (ph.decision_summary || ph.review_notes) {
    const ds = el('div', { className: 'section' });
    ds.append(el('h3', {}, 'Review'));
    if (ph.review_verdict) ds.append(el('div', { style: 'margin-bottom:.4rem' }, chip(ph.review_verdict)));
    if (ph.review_notes) ds.append(el('pre', { style: 'white-space:pre-wrap;font-size:.82rem;color:var(--muted)' }, ph.review_notes));
    if (ph.decision_summary) {
      ds.append(el('h3', { style: 'margin-top:.75rem' }, 'Decision summary'));
      ds.append(el('pre', { style: 'white-space:pre-wrap;font-size:.82rem;color:var(--muted)' }, ph.decision_summary));
    }
    if (ph.decision_rationale) {
      ds.append(el('h3', { style: 'margin-top:.75rem' }, 'Rationale'));
      ds.append(el('pre', { style: 'white-space:pre-wrap;font-size:.82rem;color:var(--muted)' }, ph.decision_rationale));
    }
    det.append(ds);
  }

  // logs
  const logSec = el('div', { className: 'section' });
  logSec.append(el('h3', {}, 'Logs'));
  const logBox = el('div', { className: 'log-box', id: `log-${ph.id}` });
  logSec.append(logBox);
  det.append(logSec);

  // register for SSE log routing
  expandedPhases.set(ph.id, { logBox, detailEl: det });

  // load existing logs
  loadLogs(ph, logBox);

  row.after(det);
}

async function loadLogs(ph, box) {
  const logs = await api.get(`/api/phases/${ph.id}/logs`).catch(() => []);
  box.innerHTML = '';
  for (const l of (logs || [])) {
    appendLog(box, l);
  }
  box.scrollTop = box.scrollHeight;
}

function appendLog(box, l) {
  const line = el('div', { className: 'log-line' });
  const ts = el('span', { className: 'log-ts' }, fmtTime(l.ts));
  line.append(ts, document.createTextNode(l.line));
  box.append(line);
  if (box.children.length > 1000) box.firstChild.remove();
}

function renderDiff(container, text) {
  let box = container.querySelector('.diff-box');
  if (box) { box.remove(); return; }
  box = el('div', { className: 'diff-box' });
  for (const line of text.split('\n')) {
    const row = el('div');
    if (line.startsWith('+') && !line.startsWith('+++')) row.className = 'diff-add';
    else if (line.startsWith('-') && !line.startsWith('---')) row.className = 'diff-del';
    else if (line.startsWith('@@')) row.className = 'diff-hunk';
    row.textContent = line;
    box.append(row);
  }
  container.append(box);
}

// ---- modals ----
function showCreateProject() {
  const nameI = input('text', '');
  nameI.placeholder = 'My project';
  const pathI = input('text', '');
  pathI.placeholder = '/home/user/repos/myproject';
  modal('New Project', [
    field('Name', nameI),
    field('Repo path', pathI),
  ], async () => {
    await api.post('/api/projects', { name: nameI.value, repo_path: pathI.value });
    navigate('backlog');
  });
}

function showCreateSpec(projects) {
  const titleI = input('text', '');
  titleI.placeholder = 'SPEC-001 my feature';
  const contentTA = textarea('# Feature title\n\nGlobal context here.\n\n## Phase 1: Bootstrap\n\nWhat this phase does.');
  const projSel = el('select');
  projSel.append(el('option', { value: '' }, '— select project —'));
  for (const p of (projects || [])) {
    projSel.append(el('option', { value: p.id }, p.name));
  }
  modal('New Spec', [
    field('Title', titleI),
    field('Project', projSel),
    field('Content', contentTA),
  ], async () => {
    const projectID = parseInt(projSel.value);
    if (!projectID) { alert('Select a project'); return false; }
    const sp = await api.post('/api/specs', { title: titleI.value, content: contentTA.value, project_id: projectID });
    navigate('spec', { id: sp.id });
  });
}

function modal(title, fields, onSubmit) {
  const overlay = el('div', { className: 'modal-overlay' });
  const box = el('div', { className: 'modal' });
  box.append(el('h2', {}, title));
  for (const f of fields) box.append(f);
  const acts = el('div', { className: 'modal-actions' });
  const cancel = btn('Cancel', '', () => overlay.remove());
  const submit = btn('Create', 'btn-primary', async () => {
    try {
      const res = await onSubmit();
      if (res !== false) overlay.remove();
    } catch(e) { alert(e.message); }
  });
  acts.append(cancel, submit);
  box.append(acts);
  overlay.append(box);
  overlay.onclick = e => { if (e.target === overlay) overlay.remove(); };
  document.body.append(overlay);
}

// ---- helpers ----
function el(tag, props = {}, ...children) {
  const e = document.createElement(tag);
  for (const [k, v] of Object.entries(props)) {
    if (k === 'className') e.className = v;
    else if (k === 'style') e.style.cssText = v;
    else if (k.startsWith('on')) e[k] = v;
    else e.setAttribute(k, v);
  }
  for (const c of children) {
    if (typeof c === 'string') e.append(document.createTextNode(c));
    else if (c) e.append(c);
  }
  return e;
}

function btn(label, cls, handler) {
  if (typeof cls === 'function') { handler = cls; cls = ''; }
  const b = el('button', { className: 'btn ' + (cls || '') }, label);
  if (handler) b.onclick = handler;
  return b;
}

function chip(val) {
  return el('span', { className: `chip chip-${val}` }, val);
}

function input(type, value) {
  const i = el('input');
  i.type = type;
  i.value = value || '';
  return i;
}

function textarea(value) {
  const t = el('textarea');
  t.value = value || '';
  return t;
}

function field(label, control) {
  const d = el('div', { className: 'field' });
  d.append(el('label', {}, label), control);
  return d;
}

function fmtDate(s) {
  if (!s) return '';
  return new Date(s).toLocaleDateString();
}

function fmtTime(s) {
  if (!s) return '';
  return new Date(s).toLocaleTimeString();
}

// ---- Projects view ----
async function renderProjects() {
  const hdr = el('div', { style: 'display:flex;align-items:center;justify-content:space-between;margin-bottom:1.25rem' });
  hdr.append(el('h2', {}, 'Projects'));
  app.append(hdr);

  const discoverBtn = btn('Discover repos', 'btn-primary', async () => {
    discoverBtn.textContent = 'Scanning...';
    discoverBtn.disabled = true;
    try {
      const repos = await api.get('/api/projects/discover');
      renderRepoList(repos);
    } catch(e) {
      alert(e.message);
    } finally {
      discoverBtn.textContent = 'Discover repos';
      discoverBtn.disabled = false;
    }
  });
  hdr.append(discoverBtn);

  // existing projects
  const projects = await api.get('/api/projects').catch(() => []);
  if (!projects || !projects.length) {
    app.append(el('div', { className: 'empty' }, 'No projects yet. Click "Discover repos" to scan ~/git.'));
  } else {
    app.append(el('div', { className: 'group-label' }, 'Registered projects'));
    for (const p of projects) {
      const card = el('div', { className: 'card' });
      const hd = el('div', { className: 'card-header' });
      hd.append(el('span', { className: 'card-title' }, p.name));
      const meta = el('div', { className: 'card-meta' }, p.repo_path);
      card.append(hd, meta);
      app.append(card);
    }
  }

  const repoListContainer = el('div', { id: 'repo-list' });
  app.append(repoListContainer);

  function renderRepoList(repos) {
    repoListContainer.innerHTML = '';
    if (!repos || !repos.length) {
      repoListContainer.append(el('div', { className: 'empty' }, 'No git repos found in ~/git.'));
      return;
    }
    repoListContainer.append(el('div', { className: 'group-label', style: 'margin-top:1.5rem' }, 'Discovered repos'));
    for (const r of repos) {
      const card = el('div', { className: 'card' });
      const hd = el('div', { className: 'card-header' });
      hd.append(el('span', { className: 'card-title' }, r.name));
      if (r.imported) hd.append(chip('done')); // reuse done chip as "imported" indicator
      const meta = el('div', { className: 'card-meta' }, r.path);
      const acts = el('div', { className: 'card-actions' });
      if (!r.imported) {
        const importBtn = btn('Import', 'btn-primary', async () => {
          try {
            await api.post('/api/projects', { name: r.name, repo_path: r.path });
            navigate('projects');
          } catch(e) { alert(e.message); }
        });
        acts.append(importBtn);
      }
      card.append(hd, meta, acts);
      repoListContainer.append(card);
    }
  }
}

// ---- Settings view ----
async function renderSettings() {
  app.append(el('h2', { style: 'margin-bottom:1.25rem' }, 'Settings'));

  const fields = [
    { key: 'db_url',                       label: 'Database URL',                    type: 'text' },
    { key: 'cerberus_bin',                  label: 'Cerberus binary',                 type: 'text' },
    { key: 'cerberus_image',                label: 'Cerberus image (blank = default)', type: 'text' },
    { key: 'cerberus_model',                label: 'Cerberus model (blank = default)', type: 'text' },
    { key: 'server_port',                   label: 'Server port',                     type: 'text' },
    { key: 'git_root',                      label: 'Git root path',                   type: 'text' },
    { key: 'max_concurrent_workflows',      label: 'Max concurrent workflows',        type: 'text' },
    { key: 'default_workflow_budget_usd',   label: 'Default workflow budget (USD)',   type: 'text' },
    { key: 'default_phase_timeout_seconds', label: 'Default phase timeout (sec)',     type: 'text' },
  ];

  // load current yaml
  let yamlText = '';
  try {
    const r = await fetch('/api/settings');
    yamlText = await r.text();
  } catch(e) {
    app.append(el('div', { className: 'empty' }, 'Could not load settings: ' + e.message));
    return;
  }

  // parse key: "value" or key: value into a map
  function parseYAML(text) {
    const map = {};
    for (const line of text.split('\n')) {
      const m = line.match(/^(\w+):\s*"?([^"]*)"?\s*$/);
      if (m) map[m[1]] = m[2];
    }
    return map;
  }

  const current = parseYAML(yamlText);
  const inputs = {};

  const form = el('div');
  for (const f of fields) {
    const inp = input(f.type, current[f.key] || '');
    inputs[f.key] = inp;
    form.append(field(f.label, inp));
  }
  app.append(form);

  const note = el('div', { style: 'font-size:.75rem;color:var(--muted);margin-bottom:1rem' },
    'Changes are written to config.yaml immediately. Restart the server for most changes to take effect.');
  app.append(note);

  const saveBtn = btn('Save', 'btn-primary', async () => {
    const patch = {};
    for (const f of fields) {
      const val = inputs[f.key].value;
      if (val !== (current[f.key] || '')) patch[f.key] = val;
    }
    if (!Object.keys(patch).length) { alert('No changes.'); return; }
    try {
      await fetch('/api/settings', {
        method: 'PATCH',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(patch),
      });
      alert('Saved. Restart server to apply.');
      navigate('settings');
    } catch(e) { alert(e.message); }
  });
  app.append(saveBtn);

  // ---- Profiles ----
  app.append(el('h3', { style: 'margin-top:2rem;margin-bottom:1rem' }, 'Profiles'));

  let profiles = [];
  try { profiles = await api.get('/api/profiles'); } catch(_) {}

  const activeProfileName = current['cerberus_profile'] || '';

  // profile list with delete buttons
  const profileList = el('div', { style: 'margin-bottom:1rem' });
  function renderProfileList() {
    profileList.innerHTML = '';
    if (!profiles.length) {
      profileList.append(el('div', { style: 'color:var(--muted);font-size:.875rem' }, 'No profiles saved.'));
      return;
    }
    for (const p of profiles) {
      const row = el('div', { style: 'display:flex;align-items:center;gap:.5rem;margin-bottom:.35rem' });
      const isActive = p.name === activeProfileName;
      const label = el('span', { style: 'flex:1;font-size:.875rem' + (isActive ? ';font-weight:600' : '') },
        p.name + (isActive ? ' (active)' : ''));
      const delBtn = btn('Delete', 'btn-danger', async () => {
        if (!confirm(`Delete profile "${p.name}"?`)) return;
        try {
          await fetch(`/api/profiles/${p.id}`, { method: 'DELETE' });
          profiles = profiles.filter(x => x.id !== p.id);
          // if deleted profile was active, clear it
          if (profileSelect.value === p.name) {
            profileSelect.value = '';
          }
          renderProfileList();
        } catch(e) { alert(e.message); }
      });
      delBtn.style.cssText = 'font-size:.75rem;padding:.2rem .5rem';
      row.append(label, delBtn);
      profileList.append(row);
    }
  }
  renderProfileList();
  app.append(profileList);

  const profileSelect = document.createElement('select');
  profileSelect.style.cssText = 'width:100%;padding:.4rem .5rem;background:var(--surface);color:var(--text);border:1px solid var(--border);border-radius:4px;font-size:.875rem';
  const noneOpt = document.createElement('option');
  noneOpt.value = '';
  noneOpt.textContent = '— none —';
  profileSelect.append(noneOpt);
  for (const p of profiles) {
    const o = document.createElement('option');
    o.value = p.name;
    o.textContent = p.name;
    if (p.name === activeProfileName) o.selected = true;
    profileSelect.append(o);
  }
  app.append(field('Active profile', profileSelect));

  const applyProfileBtn = btn('Apply profile', 'btn-primary', async () => {
    try {
      await fetch('/api/settings', {
        method: 'PATCH',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ cerberus_profile: profileSelect.value }),
      });
      alert('Profile applied. Restart server to take effect.');
      navigate('settings');
    } catch(e) { alert(e.message); }
  });
  app.append(applyProfileBtn);

  app.append(el('h4', { style: 'margin-top:1.5rem;margin-bottom:.75rem' }, 'Create new profile'));

  const pName    = input('text', '');
  const pModel   = input('text', '');
  const pImage   = input('text', '');
  const pAWSProf = input('text', '');
  const pAWSReg  = input('text', '');
  const pEnvTA   = textarea('');
  pEnvTA.placeholder = 'KEY=VALUE (one per line)';
  pEnvTA.style.cssText = 'font-family:monospace;font-size:.8rem;height:5rem';

  const pForm = el('div');
  pForm.append(
    field('Name', pName),
    field('Default model', pModel),
    field('Default image', pImage),
    field('AWS profile', pAWSProf),
    field('AWS region', pAWSReg),
    field('Extra env (KEY=VALUE, one per line)', pEnvTA),
  );
  app.append(pForm);

  const createProfileBtn = btn('Create profile', 'btn-primary', async () => {
    if (!pName.value.trim()) { alert('Name is required.'); return; }
    const extraEnv = {};
    for (const line of pEnvTA.value.split('\n')) {
      const eq = line.indexOf('=');
      if (eq > 0) extraEnv[line.slice(0, eq).trim()] = line.slice(eq + 1).trim();
    }
    const body = { name: pName.value.trim() };
    if (pModel.value.trim())   body.default_model = pModel.value.trim();
    if (pImage.value.trim())   body.default_image = pImage.value.trim();
    if (pAWSProf.value.trim()) body.aws_profile   = pAWSProf.value.trim();
    if (pAWSReg.value.trim())  body.aws_region    = pAWSReg.value.trim();
    if (Object.keys(extraEnv).length) body.extra_env = extraEnv;
    try {
      const created = await api.post('/api/profiles', body);
      profiles.push(created);
      const o = document.createElement('option');
      o.value = created.name;
      o.textContent = created.name;
      profileSelect.append(o);
      renderProfileList();
      pName.value = pModel.value = pImage.value = pAWSProf.value = pAWSReg.value = pEnvTA.value = '';
      alert(`Profile "${created.name}" created.`);
    } catch(e) { alert(e.message); }
  });
  app.append(createProfileBtn);
}

// ---- Spec Builder ----
async function renderSpecBuilder(draftId) {
  // if we have a draftId, resume existing session
  if (draftId) {
    const draft = await api.get(`/api/spec-drafts/${draftId}`).catch(e => {
      app.append(el('div', { className: 'empty' }, e.message));
      return null;
    });
    if (!draft) return;
    renderDraftChat(draft);
    return;
  }

  // start form
  app.append(el('span', { className: 'back', onclick: () => navigate('backlog') }, '← Backlog'));
  app.append(el('h2', { style: 'margin-bottom:1.25rem' }, 'Build a Spec with AI'));

  const projects = await api.get('/api/projects').catch(() => []);

  const descTA = textarea('');
  descTA.placeholder = 'Describe what you want to build or the problem you want to solve…';
  descTA.style.cssText = 'min-height:5rem';

  const projSelect = el('select');
  const optNone = el('option', { value: '' }, '— no project —');
  projSelect.append(optNone);
  for (const p of (projects || [])) {
    projSelect.append(el('option', { value: p.id }, p.name));
  }

  const form = el('div', { className: 'section' });
  form.append(
    field('What do you want to build?', descTA),
    field('Project (optional)', projSelect),
  );
  app.append(form);

  const startBtn = btn('Start', 'btn-primary', async () => {
    const description = descTA.value.trim();
    if (!description) { alert('Describe what you want to build.'); return; }
    startBtn.disabled = true;
    startBtn.textContent = 'Starting…';
    try {
      const body = { description };
      const pid = projSelect.value ? parseInt(projSelect.value) : null;
      if (pid) body.project_id = pid;
      const draft = await api.post('/api/spec-drafts', body);
      app.innerHTML = '';
      renderDraftChat(draft);
    } catch (e) {
      startBtn.disabled = false;
      startBtn.textContent = 'Start';
      alert(e.message);
    }
  });
  app.append(startBtn);
}

function renderDraftChat(draft) {
  app.innerHTML = '';
  app.append(el('span', { className: 'back', onclick: () => { if (es) { es.close(); es = null; } navigate('backlog'); } }, '← Backlog'));

  const hdr = el('div', { style: 'display:flex;align-items:center;justify-content:space-between;margin-bottom:1rem' });
  hdr.append(
    el('h2', {}, 'Spec Builder'),
    el('span', { style: 'font-size:.75rem;color:var(--muted)' }, 'session: ' + draft.cerberus_session),
  );
  app.append(hdr);

  // main layout container — two columns
  const layoutContainer = el('div', { className: 'spec-builder-layout' });
  app.append(layoutContainer);

  // left column — chat
  const leftCol = el('div', { className: 'spec-builder-chat' });
  layoutContainer.append(leftCol);

  const chatBox = el('div', { className: 'chat-messages' });
  leftCol.append(chatBox);

  let msgs = Array.isArray(draft.messages) ? draft.messages : [];
  let es = null;       // EventSource
  let specContent = '';
  let streaming = null; // { wrap, body, text } — the bubble currently receiving deltas
  const debugEvents = [];
  let debugFilter = 'all';
  let debugPanel = null;
  let debugLog = null;

  if (draft.status === 'error') {
    chatBox.append(el('div', { className: 'empty' }, 'Session error — the AI session failed to start. Abandon and try again.'));
    leftCol.append(el('div', { style: 'display:flex;gap:.5rem;margin-top:.75rem' }, btn('Abandon', 'btn-danger', abandonDraft)));
    return;
  }

  // render existing completed messages with markdown
  for (const m of msgs) renderChatMsg(chatBox, m.role, m.content);
  chatBox.scrollTop = chatBox.scrollHeight;

  // input row
  const inputRow = el('div', { className: 'chat-input-row' });
  const msgTA = el('textarea', { className: 'chat-textarea', placeholder: 'Type a message…', rows: 3 });
  msgTA.onkeydown = e => {
    if (e.key === 'Enter' && !e.shiftKey) { e.preventDefault(); sendMsg(); }
  };
  const sendB = btn('Send', 'btn-primary', sendMsg);
  inputRow.append(msgTA, sendB);
  leftCol.append(inputRow);

  // action row
  const actionRow = el('div', { style: 'display:flex;gap:.5rem;margin-top:.75rem;flex-wrap:wrap' });
  const saveB = btn('Save as Draft Spec', 'btn-primary', saveSpec);
  const abandonB = btn('Abandon', 'btn-danger', abandonDraft);
  const debugB = btn('Debug', '', () => {
    debugPanel.hidden = !debugPanel.hidden;
    debugB.textContent = debugPanel.hidden ? 'Debug' : 'Hide Debug';
  });
  actionRow.append(saveB, abandonB, debugB);
  leftCol.append(actionRow);

  debugPanel = el('div', { className: 'sse-debug-panel' });
  debugPanel.hidden = true;
  const debugControls = el('div', { className: 'sse-debug-controls' });
  const debugFilterSelect = el('select', { onchange: e => { debugFilter = e.target.value; renderDebugLog(); } });
  for (const type of ['all', 'foundry', 'text_delta', 'tool_use', 'tool_result', 'message_end', 'turn_complete']) {
    debugFilterSelect.append(el('option', { value: type }, type));
  }
  const clearDebugB = btn('Clear', '', () => { debugEvents.length = 0; renderDebugLog(); });
  debugControls.append(el('span', {}, 'SSE events'), debugFilterSelect, clearDebugB);
  debugLog = el('div', { className: 'sse-debug-log' });
  debugPanel.append(debugControls, debugLog);
  leftCol.append(debugPanel);

  // right column — spec preview
  const rightCol = el('div', { className: 'spec-preview-pane' });
  layoutContainer.append(rightCol);
  rightCol.append(el('h3', {}, 'Spec Preview'));
  const previewBody = el('div', { className: 'spec-preview-body' });
  rightCol.append(previewBody);

  function disableInput() { msgTA.disabled = true; sendB.disabled = true; }
  function enableInput() { msgTA.disabled = false; sendB.disabled = false; saveB.disabled = false; msgTA.focus(); }

  function updatePreview() {
    previewBody.innerHTML = renderMarkdown(specContent);
  }

  function compactPayload(data) {
    let payload = data;
    try { payload = JSON.parse(data); } catch (_) {}
    if (payload && typeof payload === 'object') {
      payload = { ...payload };
      for (const k of ['content', 'text', 'tool_input', 'result']) {
        if (typeof payload[k] === 'string' && payload[k].length > 500) {
          payload[k] = payload[k].slice(0, 500) + `… (${payload[k].length} chars)`;
        }
      }
    }
    return payload;
  }

  function logDebugEvent(type, e) {
    const payload = e && Object.prototype.hasOwnProperty.call(e, 'data') ? e.data : e;
    debugEvents.push({ type, ts: new Date().toLocaleTimeString(), payload: compactPayload(payload || {}) });
    if (debugEvents.length > 200) debugEvents.shift();
    renderDebugLog();
  }

  function renderDebugLog() {
    if (!debugLog) return;
    debugLog.innerHTML = '';
    for (const item of debugEvents) {
      if (debugFilter !== 'all' && item.type !== debugFilter) continue;
      const row = el('div', { className: `sse-debug-row sse-debug-${item.type}` });
      row.append(
        el('span', { className: 'sse-debug-ts' }, item.ts),
        el('span', { className: 'sse-debug-type' }, item.type),
        el('code', {}, JSON.stringify(item.payload))
      );
      debugLog.append(row);
    }
    debugLog.scrollTop = debugLog.scrollHeight;
  }

  function connectSSE() {
    es = new EventSource(`/api/spec-drafts/${draft.id}/stream`);

    es.addEventListener('text_delta', e => {
      logDebugEvent('text_delta', e);
      const ev = JSON.parse(e.data);
      if (!streaming) {
        streaming = startStreamingBubble(chatBox);
      }
      streaming.text += ev.content;
      streaming.body.textContent = streaming.text;
      chatBox.scrollTop = chatBox.scrollHeight;
    });

    es.addEventListener('message_end', e => {
      logDebugEvent('message_end', e);
      if (streaming) {
        streaming.body.innerHTML = renderMarkdown(streaming.text);
        streaming.body.classList.remove('chat-msg-streaming');
        msgs.push({ role: 'assistant', content: streaming.text });
        streaming = null;
        updatePreview();
      }
    });

    es.addEventListener('turn_complete', e => {
      logDebugEvent('turn_complete', e);
      if (streaming) {
        streaming.body.innerHTML = renderMarkdown(streaming.text);
        streaming.body.classList.remove('chat-msg-streaming');
        msgs.push({ role: 'assistant', content: streaming.text });
        streaming = null;
        updatePreview();
      }
      enableInput();
    });

    es.addEventListener('tool_use', e => {
      logDebugEvent('tool_use', e);
      const ev = JSON.parse(e.data);
      if (ev.tool_name === 'update_spec') {
        try {
          const parsed = JSON.parse(ev.tool_input);
          if (parsed.content) {
            specContent = parsed.content;
            updatePreview();
          }
        } catch (err) {
          console.error('Failed to parse tool_input:', err);
        }
      }
    });

    es.addEventListener('tool_result', e => {
      logDebugEvent('tool_result', e);
    });

    es.onerror = () => {
      logDebugEvent('foundry', { event: 'eventsource_error', draft_id: draft.id });
      if (streaming) {
        streaming.body.innerHTML = renderMarkdown(streaming.text);
        streaming.body.classList.remove('chat-msg-streaming');
        streaming = null;
      }
      enableInput();
    };
  }

  // restore latest spec content from update_spec tool message
  for (let i = msgs.length - 1; i >= 0; i--) {
    if (msgs[i].role === 'assistant' && msgs[i].content && msgs[i].content.startsWith('#')) {
      specContent = msgs[i].content;
      break;
    }
  }

  // start SSE if session is active
  if (draft.status === 'active') {
    connectSSE();
    if (msgs.length === 0) disableInput(); // waiting for first response
    else { enableInput(); updatePreview(); }
  } else {
    updatePreview();
  }

  function startStreamingBubble(box) {
    const wrap = el('div', { className: 'chat-msg chat-msg-assistant' });
    const label = el('div', { className: 'chat-msg-label' }, 'ai');
    const body = el('div', { className: 'chat-msg-body chat-msg-streaming' });
    wrap.append(label, body);
    box.append(wrap);
    return { wrap, body, text: '' };
  }

  async function sendMsg() {
    const content = msgTA.value.trim();
    if (!content) return;
    msgTA.value = '';
    disableInput();

    // optimistically render user message
    renderChatMsg(chatBox, 'user', content);
    chatBox.scrollTop = chatBox.scrollHeight;

    try {
      // POST sends the message to cerberus — response arrives via SSE
      logDebugEvent('foundry', { event: 'message_post_start', draft_id: draft.id, content });
      await api.post(`/api/spec-drafts/${draft.id}/message`, { content });
      logDebugEvent('foundry', { event: 'message_post_success', draft_id: draft.id });
      msgs.push({ role: 'user', content });
    } catch (e) {
      logDebugEvent('foundry', { event: 'message_post_error', draft_id: draft.id, error: e.message });
      renderChatMsg(chatBox, 'assistant', 'Error: ' + e.message);
      enableInput();
    }
  }

  async function saveSpec() {
    logDebugEvent('foundry', { event: 'save_modal_open', draft_id: draft.id });
    const titleInput = input('text', draft.title || '');
    titleInput.placeholder = 'Spec title';
    modal('Save as Spec', [
      field('Title (edit if you like)', titleInput),
    ], async () => {
      const title = titleInput.value.trim();
      if (!title) { alert('Title is required.'); return false; }
      saveB.disabled = true;
      saveB.textContent = 'Saving…';
      try {
        logDebugEvent('foundry', { event: 'save_start', draft_id: draft.id, title });
        const result = await api.post(`/api/spec-drafts/${draft.id}/save`, { title });
        logDebugEvent('foundry', { event: 'save_success', draft_id: draft.id, spec_id: result.spec_id });
        if (es) es.close();
        navigate('spec', { id: result.spec_id });
      } catch (e) {
        logDebugEvent('foundry', { event: 'save_error', draft_id: draft.id, error: e.message });
        saveB.disabled = false;
        saveB.textContent = 'Save as Draft Spec';
        alert(e.message);
        return false;
      }
    });
  }

  async function abandonDraft() {
    if (!confirm('Abandon this spec draft and close the AI session?')) return;
    logDebugEvent('foundry', { event: 'abandon_start', draft_id: draft.id });
    if (es) es.close();
    try {
      const r = await fetch(`/api/spec-drafts/${draft.id}`, { method: 'DELETE' });
      if (!r.ok) throw new Error(r.statusText || `HTTP ${r.status}`);
      logDebugEvent('foundry', { event: 'abandon_success', draft_id: draft.id });
    } catch (e) {
      logDebugEvent('foundry', { event: 'abandon_error', draft_id: draft.id, error: e.message });
    }
    navigate('backlog');
  }
}

// render markdown to HTML, sanitizing script tags
function renderMarkdown(text) {
  if (typeof marked !== 'undefined') {
    return marked.parse(text, { breaks: true });
  }
  // fallback: escape HTML and preserve whitespace
  return text.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/\n/g, '<br>');
}

function renderChatMsg(box, role, content) {
  const wrap = el('div', { className: `chat-msg chat-msg-${role}` });
  const label = el('div', { className: 'chat-msg-label' }, role === 'user' ? 'you' : 'ai');
  const body = el('div', { className: 'chat-msg-body' });
  if (role === 'assistant') {
    body.innerHTML = renderMarkdown(content);
  } else {
    body.textContent = content;
  }
  wrap.append(label, body);
  box.append(wrap);
}

// ---- boot ----
document.getElementById('nav-backlog').onclick = e => { e.preventDefault(); navigate('backlog'); };
document.getElementById('nav-projects').onclick = e => { e.preventDefault(); navigate('projects'); };
document.getElementById('nav-settings').onclick = e => { e.preventDefault(); navigate('settings'); };
navigate('backlog');
