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
  // update nav active state
  document.querySelectorAll('nav a').forEach(a => a.classList.remove('active'));
  if (view === 'backlog') { document.getElementById('nav-backlog').classList.add('active'); renderBacklog(); }
  else if (view === 'projects') { document.getElementById('nav-projects').classList.add('active'); renderProjects(); }
  else if (view === 'spec') renderSpec(params.id);
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
  actions.append(btnProj, btnSpec);
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
}

// ---- Spec detail ----
async function renderSpec(id) {
  let spec = await api.get(`/api/specs/${id}`).catch(e => { app.append(el('div', {className:'empty'}, e.message)); return null; });
  if (!spec) return;
  const workflows = await api.get(`/api/specs`).catch(() => null); // we'll fetch workflows separately

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
  fieldContent.append(el('label', {}, 'Content (markdown)'), contentTA);
  section.append(fieldTitle, fieldContent,
    el('div', { style: 'display:flex;gap:.5rem' }, saveBtn, promoteBtn, runBtn));
  app.append(section);

  // workflows
  const wfSection = el('div', { className: 'section' });
  wfSection.append(el('h3', {}, 'Workflow runs'));
  // fetch by listing phases won't work; we'd need a workflow list endpoint by spec.
  // For now show a message — we'll link from nav.
  const wfList = el('div', { id: 'wf-list' });
  wfList.append(el('div', { className: 'empty' }, 'Loading...'));
  wfSection.append(wfList);
  app.append(wfSection);

  // fetch workflows for this spec via all workflows (no dedicated endpoint — build from known IDs)
  // We store locally or just give user the run button above. For richer history we'd need GET /api/workflows?spec_id=
  wfList.innerHTML = '<div class="empty">Start a workflow from the button above.</div>';
}

// ---- Workflow detail ----
async function renderWorkflow(id) {
  const wf = await api.get(`/api/workflows/${id}`).catch(e => { app.append(el('div',{className:'empty'},e.message)); return null; });
  if (!wf) return;
  const phases = await api.get(`/api/workflows/${id}/phases`).catch(() => []);

  app.append(el('span', { className: 'back', onclick: () => navigate('backlog') }, '← Backlog'));

  const hdr = el('div', { style: 'display:flex;align-items:center;gap:.6rem;margin-bottom:1rem' });
  hdr.append(el('h2', { style: 'flex:1' }, `Workflow #${wf.id}`), chip(wf.track), chip(wf.status));
  app.append(hdr);

  if (wf.status === 'paused' || wf.status === 'failed') {
    const resumeBtn = btn('Resume', 'btn-primary', async () => {
      try { await api.post(`/api/workflows/${id}/resume`); navigate('workflow', { id }); }
      catch(e) { alert(e.message); }
    });
    app.append(el('div', { style: 'margin-bottom:1rem' }, resumeBtn));
  }

  for (const ph of (phases || [])) {
    const row = el('div', { className: 'phase-row', onclick: () => togglePhaseDetail(ph, row) });
    const pos = el('div', { className: 'phase-pos' }, `P${ph.position}`);
    const body = el('div', { className: 'phase-body' });
    const nameRow = el('div', { style: 'display:flex;align-items:center;gap:.5rem' });
    nameRow.append(el('span', { className: 'phase-name' }, ph.name), chip(ph.status));
    if (ph.review_verdict) nameRow.append(chip(ph.review_verdict));
    body.append(nameRow);
    row.append(pos, body);
    row._expanded = false;
    row._ph = ph;
    app.append(row);
  }

  if (!phases || !phases.length) app.append(el('div', { className: 'empty' }, 'No phases yet.'));

  // auto-refresh if running
  if (wf.status === 'running') {
    setTimeout(() => navigate('workflow', { id }), 4000);
  }
}

function togglePhaseDetail(ph, row) {
  if (row._expanded) {
    const det = row.nextSibling;
    if (det && det._isDetail) det.remove();
    row._expanded = false;
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
  acts.append(approveBtn, rejectBtn, diffBtn);
  det.append(acts);

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
  loadLogs(ph, logBox);

  row.after(det);
}

async function loadLogs(ph, box) {
  const logs = await api.get(`/api/phases/${ph.id}/logs`).catch(() => []);
  box.innerHTML = '';
  for (const l of (logs || [])) {
    appendLog(box, l);
  }
  if (ph.status === 'running' || ph.status === 'awaiting_review') {
    startSSE(ph.id, box);
  }
}

function startSSE(phaseID, box) {
  const es = new EventSource(`/api/phases/${phaseID}/logs/stream`);
  es.onmessage = e => {
    const l = JSON.parse(e.data);
    appendLog(box, l);
    box.scrollTop = box.scrollHeight;
  };
  es.addEventListener('done', () => es.close());
  es.onerror = () => es.close();
}

function appendLog(box, l) {
  const line = el('div', { className: 'log-line' });
  const ts = el('span', { className: 'log-ts' }, fmtTime(l.ts));
  line.append(ts, document.createTextNode(l.line));
  box.append(line);
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

// ---- boot ----
document.getElementById('nav-backlog').onclick = e => { e.preventDefault(); navigate('backlog'); };
document.getElementById('nav-projects').onclick = e => { e.preventDefault(); navigate('projects'); };
navigate('backlog');
