// Foundry UI: local, dependency-light progressive enhancement.
'use strict';

let workflowSource;
let draftSource;
let logSource;
let chatSource;
let refreshTimer;
let liveAssistantBody;
let liveChatAssistantBody;
let currentPhaseDetail;

const STATUS_CLASSES = ['active', 'pending', 'queued', 'paused', 'idle', 'running', 'progress', 'streaming', 'suspended', 'awaiting_review', 'awaiting', 'review', 'warning', 'done', 'pass', 'accepted', 'failed', 'fail', 'error', 'blocked', 'rejected', 'stopping'];

function displayStatus(status) {
  return String(status || '').replace(/_/g, ' ');
}

function setStatusChip(chip, status) {
  if (!chip || !status) return false;
  STATUS_CLASSES.forEach((s) => chip.classList.remove(`chip-${s}`));
  chip.classList.add(`chip-${status}`);
  chip.dataset.status = status;
  chip.textContent = displayStatus(status);
  return true;
}

function formatSSETime(value) {
  if (!value) return '—';
  const d = new Date(value);
  if (Number.isNaN(d.getTime())) return String(value);
  const pad = (n) => String(n).padStart(2, '0');
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`;
}

function applyWorkflowStatus(root, status) {
  return setStatusChip(root.querySelector('[data-workflow-status]'), status);
}

function phaseProgressPercent(status) {
  if (status === 'done' || status === 'failed') return 100;
  if (status === 'running') return 40;
  return 0;
}

function setWorkWindowPhase(root, phaseId, tab) {
  const desk = root.closest?.('[data-workflow-id]') || document.querySelector('[data-workflow-id]');
  const body = document.getElementById('workflow-work-body');
  if (!desk || !body || !phaseId) return;
  const nextTab = tab || desk.dataset.selectedTab || 'diff';
  desk.dataset.selectedPhase = String(phaseId);
  desk.dataset.selectedTab = nextTab;
  document.querySelectorAll('[data-workflow-phase-select]').forEach((el) => el.classList.toggle('is-selected', el.dataset.phaseId === String(phaseId)));
  document.querySelectorAll('[data-work-window-tab]').forEach((el) => el.classList.toggle('is-selected', el.dataset.workWindowTab === nextTab));
  const tile = document.querySelector(`[data-workflow-phase-select][data-phase-id="${phaseId}"]`);
  const name = document.querySelector('[data-selected-phase-name]');
  if (name && tile?.dataset.phaseName) name.textContent = tile.dataset.phaseName;
  document.querySelectorAll('[data-phase-action]').forEach((button) => {
    const action = button.dataset.phaseAction;
    button.dataset.jsonPost = `/api/phases/${phaseId}/${action}`;
  });
  body.dataset.phaseDetailPanel = String(phaseId);
  const kind = nextTab === 'logs' ? 'logs' : 'diff';
  if (window.htmx) htmx.ajax('GET', `/phases/${phaseId}/${kind}/fragment`, { target: '#workflow-work-body', swap: 'innerHTML' });
  else location.href = `/phases/${phaseId}/${kind}/fragment`;
}

function applyPhaseStatus(root, phase) {
  if (!phase) return false;
  const id = phase.phase_id || phase.id;
  const status = phase.status;
  if (!id || !status) return false;
  const row = root.querySelector(`[data-phase-row="${id}"]`);
  const chip = root.querySelector(`[data-phase-status-chip="${id}"]`);
  if (!row || !chip) return false;
  STATUS_CLASSES.forEach((s) => row.classList.remove(`phase-row-${s}`));
  row.classList.add(`phase-row-${status}`);
  row.dataset.phaseStatus = status;
  setStatusChip(chip, status);
  const fill = root.querySelector(`[data-phase-fill="${id}"]`);
  if (fill) fill.style.width = `${phaseProgressPercent(status)}%`;
  const started = row.querySelector('[data-phase-started]');
  if (started && phase.started_at) started.textContent = formatSSETime(phase.started_at);
  const finished = row.querySelector('[data-phase-finished]');
  if (finished && phase.finished_at) finished.textContent = formatSSETime(phase.finished_at);
  return true;
}

function applyWorkflowEvent(root, ev) {
  try {
    const data = JSON.parse(ev.data || '{}');
    if (ev.type === 'snapshot') {
      const okWorkflow = applyWorkflowStatus(root, data.workflow?.status);
      const phases = Array.isArray(data.phases) ? data.phases : [];
      const okPhases = phases.every((phase) => applyPhaseStatus(root, phase));
      return okWorkflow && okPhases;
    }
    if (ev.type === 'workflow_update') return applyWorkflowStatus(root, data.status);
    if (ev.type === 'phase_update') return applyPhaseStatus(root, data);
    if (ev.type === 'done' || ev.type === 'failed') return applyWorkflowStatus(root, data.status || ev.type);
    return true;
  } catch (_) {
    return false;
  }
}

function initWorkflowStream(root) {
  const el = root.querySelector?.('[data-workflow-stream]');
  if (!el) return;
  if (workflowSource) { workflowSource.close(); workflowSource = null; }
  if (currentPhaseDetail?.id) {
    el.dataset.selectedPhase = String(currentPhaseDetail.id);
    el.dataset.selectedTab = currentPhaseDetail.kind || el.dataset.selectedTab || 'diff';
  }
  workflowSource = new EventSource(el.dataset.workflowStream);
  const handle = (ev) => {
    if (!applyWorkflowEvent(el, ev)) refreshWorkflowPreservingPhase(el.dataset.refresh);
  };
  ['snapshot', 'workflow_update', 'phase_update', 'done', 'failed'].forEach((name) => workflowSource.addEventListener(name, handle));
}

function classifyLogState(line) {
  const lower = String(line || '').toLowerCase();
  if (lower.includes('blocked')) return 'blocked';
  if (lower.includes('error') || lower.includes('failed') || lower.includes('fail')) return 'error';
  if (lower.includes('warn')) return 'warning';
  if (lower.includes('done') || lower.includes('complete')) return 'done';
  if (lower.includes('running') || lower.includes('started')) return 'running';
  return 'normal';
}

function splitLogSource(line) {
  const text = String(line || '').trim();
  if (!text) return ['system', '—'];
  if (text.startsWith('[')) {
    const end = text.indexOf(']');
    if (end > 1 && end < 32) return [text.slice(1, end).trim().toLowerCase(), text.slice(end + 1).trim()];
  }
  const idx = text.indexOf(':');
  if (idx > 0 && idx < 24) {
    const prefix = text.slice(0, idx).trim();
    if (!prefix.includes(' ')) return [prefix.toLowerCase(), text.slice(idx + 1).trim()];
  }
  return [text.toLowerCase().includes('system') ? 'system' : 'agent', text];
}

function appendLogRow(box, log) {
  const id = Number(log.id || 0);
  const last = Number(box.dataset.logLastId || 0);
  if (id && id <= last) return;
  const [source, event] = splitLogSource(log.line || '');
  const state = classifyLogState(log.line || '');
  const row = document.createElement('div');
  row.className = `log-row log-state-${state}`;
  if (id) row.dataset.logId = String(id);
  const timeEl = document.createElement('span');
  timeEl.className = 'log-time';
  timeEl.textContent = formatSSETime(log.ts);
  const sourceEl = document.createElement('span');
  sourceEl.className = 'log-source';
  sourceEl.textContent = source;
  const eventEl = document.createElement('span');
  eventEl.className = 'log-event';
  eventEl.textContent = event;
  const stateEl = document.createElement('span');
  stateEl.className = 'log-state';
  const chip = document.createElement('span');
  chip.className = `chip chip-${state}`;
  chip.textContent = state;
  stateEl.appendChild(chip);
  row.append(timeEl, sourceEl, eventEl, stateEl);
  box.appendChild(row);
  if (id) {
    box.dataset.logLastId = String(id);
    box.dataset.logStream = box.dataset.logStream.replace(/([?&]after_id=)\d+/, `$1${id}`);
  }
  box.scrollTop = box.scrollHeight;
}

function initLogStream(root) {
  const box = root.querySelector?.('[data-log-stream]');
  if (logSource) { logSource.close(); logSource = null; }
  if (!box) return;
  logSource = new EventSource(box.dataset.logStream);
  logSource.onmessage = (ev) => {
    try { appendLogRow(box, JSON.parse(ev.data)); } catch (_) {}
  };
  logSource.addEventListener('done', () => { if (logSource) { logSource.close(); logSource = null; } });
}

