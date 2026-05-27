// Small progressive-enhancement hooks for the server-rendered HTMX UI.
'use strict';

function formJSON(form) {
  const data = {};
  const includeEmpty = form.hasAttribute('data-include-empty');
  for (const [key, value] of new FormData(form).entries()) {
    if (value === '' && !includeEmpty) continue;
    if (key === 'extra_env') data[key] = value.trim() ? JSON.parse(value) : {};
    else if (/^-?\d+(\.\d+)?$/.test(value)) data[key] = Number(value);
    else data[key] = value;
  }
  return data;
}

function refresh(url, target) {
  if (window.htmx && url) htmx.ajax('GET', url, target || '#app');
  else if (url) location.href = url.replace('/fragment', '');
}

async function refreshWorkflowPreservingPhase(url) {
  if (!url) return;
  const app = document.getElementById('app');
  const open = currentPhaseDetail && app?.querySelector(`#phase-detail-${currentPhaseDetail.id}:not(:empty)`);
  if (!app || !open) { refresh(url, '#app'); return; }
  try {
    const res = await fetch(url);
    if (!res.ok) throw new Error(await res.text());
    const doc = new DOMParser().parseFromString(await res.text(), 'text/html');
    const next = doc.body.firstElementChild;
    const nextPanel = next?.querySelector(`#phase-detail-${currentPhaseDetail.id}`);
    if (next && nextPanel) {
      nextPanel.innerHTML = open.innerHTML;
      app.innerHTML = '';
      app.appendChild(next);
      if (window.htmx) htmx.process(app);
      initStreams(app);
    } else {
      refresh(url, '#app');
    }
  } catch (_) {
    refresh(url, '#app');
  }
}

function fragmentURL(url) {
  if (url.endsWith('/fragment')) return url;
  return url.replace(/\/$/, '') + '/fragment';
}

function go(url) {
  if (window.htmx) htmx.ajax('GET', fragmentURL(url), '#app').then(() => history.pushState({}, '', url));
  else location.href = url;
}

async function sendJSON(method, url, body) {
  const init = { method, headers: { 'Content-Type': 'application/json' } };
  if (body !== undefined) init.body = JSON.stringify(body);
  const res = await fetch(url, init);
  if (!res.ok) {
    let message = await res.text();
    try { message = JSON.parse(message).error || message; } catch (_) {}
    throw new Error(message);
  }
  if (res.status === 204) return null;
  return res.json();
}

function ensureToastHost() {
  let host = document.getElementById('toast-host');
  if (!host) {
    host = document.createElement('div');
    host.id = 'toast-host';
    host.className = 'toast-host';
    host.setAttribute('aria-live', 'polite');
    document.body.appendChild(host);
  }
  return host;
}

function toast(message, kind = 'info') {
  const host = ensureToastHost();
  const item = document.createElement('div');
  item.className = `toast toast-${kind}`;
  item.textContent = message;
  host.appendChild(item);
  setTimeout(() => item.remove(), 4500);
}

function showError(message) {
  const region = document.getElementById('action-errors');
  if (!region) return;
  region.textContent = message || 'Request failed';
  region.hidden = false;
}

function clearError() {
  const region = document.getElementById('action-errors');
  if (!region) return;
  region.textContent = '';
  region.hidden = true;
}

function pendingLabel(el) {
  return el?.dataset.pendingLabel || 'Working…';
}

function setPending(scope, pending) {
  if (!scope) return;
  const control = scope.matches?.('button, .btn') ? scope : scope.querySelector?.('button, .btn');
  const controls = scope.matches?.('form') ? scope.querySelectorAll('button, input, select, textarea') : [scope];
  controls.forEach?.((el) => {
    if (!el) return;
    if (pending) {
      el.dataset.wasDisabled = el.disabled ? '1' : '0';
      el.disabled = true;
      el.setAttribute('aria-busy', 'true');
    } else {
      if (el.dataset.wasDisabled !== '1') el.disabled = false;
      el.removeAttribute('aria-busy');
      delete el.dataset.wasDisabled;
    }
  });
  if (control) {
    if (pending) {
      if (!control.dataset.originalText) control.dataset.originalText = control.textContent;
      control.classList.add('is-pending');
      control.textContent = pendingLabel(control);
    } else {
      control.classList.remove('is-pending');
      if (control.dataset.originalText) control.textContent = control.dataset.originalText;
      delete control.dataset.originalText;
    }
  }
}

function redirectFrom(el, data) {
  if (el.dataset.redirect) { go(el.dataset.redirect); return true; }
  if (!el.dataset.redirectTemplate) return false;
  let url = el.dataset.redirectTemplate;
  for (const [key, value] of Object.entries(data || {})) url = url.replace(`{${key}}`, value);
  if (url.includes('{')) return false;
  go(url);
  return true;
}

function appendChatMessage(role, content, extraClass) {
  const box = document.getElementById('draft-messages');
  if (!box) return null;
  const msg = document.createElement('div');
  msg.className = `chat-msg chat-msg-${role}${extraClass ? ' ' + extraClass : ''}`;
  const label = document.createElement('div');
  label.className = 'chat-msg-label';
  label.textContent = role;
  const body = document.createElement('div');
  body.className = 'chat-msg-body';
  body.textContent = content;
  msg.append(label, body);
  box.appendChild(msg);
  box.scrollTop = box.scrollHeight;
  return body;
}

function setDraftInputDisabled(disabled) {
  const form = document.querySelector('form[data-draft-message]');
  if (!form) return;
  form.querySelectorAll('textarea, button').forEach((el) => { el.disabled = disabled; });
}

async function submitDraftMessage(form) {
  const textarea = form.querySelector('textarea[name="content"]');
  const content = textarea ? textarea.value.trim() : '';
  if (!content) return;
  appendChatMessage('user', content);
  textarea.value = '';
  liveAssistantBody = appendChatMessage('assistant', 'Thinking…', 'chat-typing');
  setDraftInputDisabled(true);
  try {
    await sendJSON((form.method || 'POST').toUpperCase(), form.action, { content });
  } catch (err) {
    setDraftInputDisabled(false);
    if (liveAssistantBody) liveAssistantBody.textContent = 'Error: ' + (err.message || String(err));
    showError(err.message || String(err));
    toast(err.message || String(err), 'error');
  }
}

document.addEventListener('submit', async (event) => {
  const form = event.target;
  if (!(form instanceof HTMLFormElement)) return;
  if (form.matches('[data-draft-message]')) {
    event.preventDefault();
    submitDraftMessage(form);
    return;
  }
  if (!form.matches('[data-json], [data-settings]')) return;
  event.preventDefault();
  try {
    clearError();
    const method = form.dataset.method || (form.matches('[data-settings]') ? 'PATCH' : (form.method || 'POST').toUpperCase());
    const body = formJSON(form);
    setPending(form, true);
    const data = await sendJSON(method, form.action, body);
    toast('Saved', 'success');
    if (!redirectFrom(form, data)) refresh(form.dataset.refresh, form.dataset.target);
  } catch (err) {
    showError(err.message || String(err));
    toast(err.message || String(err), 'error');
  } finally {
    setPending(form, false);
  }
});

document.addEventListener('click', async (event) => {
  const phaseButton = event.target.closest('[data-phase-detail]');
  if (phaseButton) {
    currentPhaseDetail = { id: phaseButton.dataset.phaseId, kind: phaseButton.dataset.phaseDetail };
    document.querySelectorAll('.phase-detail').forEach((panel) => {
      if (panel.id !== `phase-detail-${currentPhaseDetail.id}`) panel.innerHTML = '';
    });
  }

  const button = event.target.closest('[data-json-post], [data-json-patch], [data-json-delete]');
  if (!button) return;
  event.preventDefault();
  if (button.disabled) return;
  if (button.hasAttribute('data-confirm') && !window.confirm(button.dataset.confirm)) return;
  setPending(button, true);
  try {
    clearError();
    const body = button.dataset.body ? JSON.parse(button.dataset.body) : {};
    const method = button.dataset.jsonDelete ? 'DELETE' : (button.dataset.jsonPatch ? 'PATCH' : 'POST');
    const url = button.dataset.jsonDelete || button.dataset.jsonPatch || button.dataset.jsonPost;
    const data = await sendJSON(method, url, method === 'DELETE' ? undefined : body);
    toast('Done', 'success');
    if (!redirectFrom(button, data)) refresh(button.dataset.refresh, button.dataset.target);
  } catch (err) {
    showError(err.message || String(err));
    toast(err.message || String(err), 'error');
  } finally {
    setPending(button, false);
  }
});

let workflowSource;
let draftSource;
let logSource;
let refreshTimer;
let liveAssistantBody;
let currentPhaseDetail;

const STATUS_CLASSES = ['pending', 'queued', 'paused', 'idle', 'running', 'progress', 'streaming', 'awaiting_review', 'awaiting', 'review', 'warning', 'done', 'pass', 'accepted', 'failed', 'fail', 'error', 'blocked', 'rejected', 'stopping'];

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
  workflowSource = new EventSource(el.dataset.workflowStream);
  const handle = (ev) => {
    if (!applyWorkflowEvent(el, ev)) refreshWorkflowPreservingPhase(el.dataset.refresh);
  };
  ['snapshot', 'workflow_update', 'phase_update', 'done', 'failed'].forEach((name) => workflowSource.addEventListener(name, handle));
}

function updateDraftPreviewFromTool(ev) {
  try {
    const data = JSON.parse(ev.data);
    if (data.tool_name !== 'update_spec') return;
    const input = JSON.parse(data.tool_input || '{}');
    const content = input.content || input.markdown || input.spec || '';
    const preview = document.getElementById('draft-preview');
    if (preview && content) preview.textContent = content;
  } catch (_) {}
}

function initDraftStream(root) {
  const el = root.querySelector?.('[data-draft-stream]');
  if (!el) return;
  if (draftSource) { draftSource.close(); draftSource = null; }
  const out = document.getElementById('draft-stream');
  const finish = () => {
    setDraftInputDisabled(false);
    if (out) out.textContent = '';
    liveAssistantBody = null;
    clearTimeout(refreshTimer);
    refreshTimer = setTimeout(() => refresh(`/spec-builder/${el.dataset.draftId}/fragment`, '#app'), 250);
  };
  draftSource = new EventSource(el.dataset.draftStream);
  draftSource.addEventListener('text_delta', (ev) => {
    try {
      const text = JSON.parse(ev.data).content || '';
      if (!liveAssistantBody) liveAssistantBody = appendChatMessage('assistant', '');
      if (liveAssistantBody) {
        if (liveAssistantBody.textContent === 'Thinking…') liveAssistantBody.textContent = '';
        liveAssistantBody.parentElement?.classList.remove('chat-typing');
        liveAssistantBody.textContent += text;
        const box = liveAssistantBody.closest('.chat-messages');
        if (box) box.scrollTop = box.scrollHeight;
      } else if (out) out.textContent += text;
    } catch (_) {}
  });
  draftSource.addEventListener('tool_use', updateDraftPreviewFromTool);
  ['message_end', 'turn_complete', 'error'].forEach((name) => draftSource.addEventListener(name, finish));
  draftSource.onerror = finish;
}

function appendLogRow(box, log) {
  const id = Number(log.id || 0);
  const last = Number(box.dataset.logLastId || 0);
  if (id && id <= last) return;
  const row = document.createElement('div');
  row.className = 'log-line';
  if (id) row.dataset.logId = String(id);
  const ts = document.createElement('span');
  ts.className = 'log-ts';
  ts.textContent = formatSSETime(log.ts);
  row.appendChild(ts);
  row.append(document.createTextNode(log.line || ''));
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

function initStreams(root) {
  initWorkflowStream(root);
  initDraftStream(root);
  initLogStream(root);
}

document.body.addEventListener('htmx:beforeRequest', (event) => {
  const el = event.detail.elt;
  if (el?.matches?.('form, button, .btn')) setPending(el, true);
});

document.body.addEventListener('htmx:afterRequest', (event) => {
  const el = event.detail.elt;
  if (el?.matches?.('form, button, .btn')) setPending(el, false);
  if (event.detail.failed) {
    const message = event.detail.xhr?.responseText || 'Request failed';
    showError(message);
    toast(message, 'error');
  } else {
    clearError();
  }
});

document.body.addEventListener('htmx:afterSwap', (event) => {
  if (event.detail.target.id === 'app') {
    const page = event.detail.target.firstElementChild?.dataset.page || 'backlog';
    document.querySelectorAll('nav a').forEach((a) => a.classList.toggle('active', a.dataset.nav === page));
    if (!event.detail.target.querySelector('[data-workflow-stream]') && workflowSource) { workflowSource.close(); workflowSource = null; }
    if (!event.detail.target.querySelector('[data-draft-stream]') && draftSource) { draftSource.close(); draftSource = null; }
    if (logSource) { logSource.close(); logSource = null; }
  }
  initStreams(event.detail.target);
});

document.addEventListener('DOMContentLoaded', () => initStreams(document));
