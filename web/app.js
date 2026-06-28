// Small progressive-enhancement hooks for the server-rendered HTMX UI.
'use strict';

function formJSON(form) {
  const data = {};
  const includeEmpty = form.hasAttribute('data-include-empty');
  for (const [key, value] of new FormData(form).entries()) {
    const fieldIncludesEmpty = Array.from(form.elements).some((el) => el.name === key && el.hasAttribute('data-include-empty'));
    if (value === '' && !includeEmpty && !fieldIncludesEmpty) continue;
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
  const desk = app?.querySelector('[data-workflow-id]');
  const selectedPhase = currentPhaseDetail?.id || desk?.dataset.selectedPhase;
  const selectedTab = currentPhaseDetail?.kind || desk?.dataset.selectedTab || 'diff';
  if (!app || !desk || !selectedPhase) { refresh(url, '#app'); return; }
  try {
    const res = await fetch(url);
    if (!res.ok) throw new Error(await res.text());
    const doc = new DOMParser().parseFromString(await res.text(), 'text/html');
    const next = doc.body.firstElementChild;
    const nextDesk = next?.matches?.('[data-workflow-id]') ? next : next?.querySelector?.('[data-workflow-id]');
    if (!next || !nextDesk) { refresh(url, '#app'); return; }
    nextDesk.dataset.selectedPhase = String(selectedPhase);
    nextDesk.dataset.selectedTab = selectedTab;
    app.innerHTML = '';
    app.appendChild(next);
    if (window.htmx) htmx.process(app);
    initStreams(app);
    const phaseControl = app.querySelector(`[data-workflow-phase-select][data-phase-id="${selectedPhase}"]`) || nextDesk;
    setWorkWindowPhase(phaseControl, selectedPhase, selectedTab);
  } catch (_) {
    refresh(url, '#app');
  }
}

function fragmentURL(url) {
  const next = new URL(url, location.origin);
  let path = next.pathname === '/' ? '/chat' : next.pathname.replace(/\/$/, '');
  if (!path.endsWith('/fragment')) path += '/fragment';
  return path + next.search;
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
  return appendChatMessageToBox('draft-messages', role, content, extraClass);
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
  if (form.matches('[data-chat-message]')) {
    event.preventDefault();
    submitChatMessage(form);
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

document.addEventListener('keydown', (event) => {
  if (event.key !== 'Enter' || event.shiftKey || event.isComposing) return;
  const textarea = event.target;
  if (!(textarea instanceof HTMLTextAreaElement)) return;
  const form = textarea.closest('form[data-chat-message]');
  if (!form) return;
  event.preventDefault();
  form.requestSubmit();
});

document.addEventListener('click', async (event) => {
  const workPhase = event.target.closest('[data-workflow-phase-select]');
  if (workPhase) {
    event.preventDefault();
    currentPhaseDetail = { id: workPhase.dataset.phaseId, kind: 'diff' };
    setWorkWindowPhase(workPhase, workPhase.dataset.phaseId, 'diff');
    return;
  }
  const workTab = event.target.closest('[data-work-window-tab]');
  if (workTab) {
    event.preventDefault();
    const desk = workTab.closest('[data-workflow-id]');
    const phaseId = desk?.dataset.selectedPhase;
    currentPhaseDetail = { id: phaseId, kind: workTab.dataset.workWindowTab };
    setWorkWindowPhase(workTab, phaseId, workTab.dataset.workWindowTab);
    return;
  }

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
    if (!redirectFrom(button, data)) {
      if (button.closest('[data-workflow-id]') && button.dataset.refresh) refreshWorkflowPreservingPhase(button.dataset.refresh);
      else refresh(button.dataset.refresh, button.dataset.target);
    }
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
        appendAssistantMarkdown(liveAssistantBody, text);
        const box = liveAssistantBody.closest('.chat-messages');
        if (box) box.scrollTop = box.scrollHeight;
      } else if (out) out.textContent += text;
    } catch (_) {}
  });
  draftSource.addEventListener('tool_use', updateDraftPreviewFromTool);
  ['message_end', 'turn_complete', 'error'].forEach((name) => draftSource.addEventListener(name, finish));
  draftSource.onerror = finish;
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

function setChatInputDisabled(disabled) {
  const form = document.querySelector('form[data-chat-message]');
  if (!form) return;
  form.querySelectorAll('textarea, button').forEach((el) => { el.disabled = disabled; });
}

function setChatDebug(message, eventName) {
  const line = document.querySelector('[data-chat-debug-line]');
  const conn = document.querySelector('[data-chat-connection]');
  const last = document.querySelector('[data-chat-last-event]');
  const count = document.querySelector('[data-chat-event-count]');
  if (line && message) line.textContent = message;
  if (conn && message) conn.textContent = message;
  if (last && eventName) last.textContent = eventName;
  if (count && eventName) count.textContent = String(Number(count.textContent || 0) + 1);
}

function escapeHTML(value) {
  return String(value || '').replace(/[&<>"']/g, (ch) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[ch]));
}

function renderMarkdownInline(value) {
  let rest = String(value || '');
  let out = '';
  while (rest) {
    const code = rest.indexOf('`');
    const bold = rest.indexOf('**');
    if (code < 0 && bold < 0) return out + escapeHTML(rest);
    if (code >= 0 && (bold < 0 || code < bold)) {
      const end = rest.indexOf('`', code + 1);
      if (end < 0) return out + escapeHTML(rest);
      out += escapeHTML(rest.slice(0, code)) + `<code>${escapeHTML(rest.slice(code + 1, end))}</code>`;
      rest = rest.slice(end + 1);
      continue;
    }
    const end = rest.indexOf('**', bold + 2);
    if (end < 0) return out + escapeHTML(rest);
    out += escapeHTML(rest.slice(0, bold)) + `<strong>${escapeHTML(rest.slice(bold + 2, end))}</strong>`;
    rest = rest.slice(end + 2);
  }
  return out;
}

function renderChatMarkdown(value) {
  const lines = String(value || '').replace(/\r\n/g, '\n').split('\n');
  const out = [];
  let para = [];
  let listKind = '';
  let codeOpen = false;
  const flushPara = () => {
    if (!para.length) return;
    out.push(`<p>${renderMarkdownInline(para.join(' '))}</p>`);
    para = [];
  };
  const flushList = () => {
    if (!listKind) return;
    out.push(`</${listKind}>`);
    listKind = '';
  };
  const openList = (kind) => {
    if (listKind === kind) return;
    flushList();
    out.push(`<${kind}>`);
    listKind = kind;
  };
  for (const line of lines) {
    const trimmed = line.trim();
    if (trimmed.startsWith('```')) {
      if (codeOpen) { out.push('</code></pre>'); codeOpen = false; }
      else { flushPara(); flushList(); out.push('<pre><code>'); codeOpen = true; }
      continue;
    }
    if (codeOpen) { out.push(escapeHTML(line) + '\n'); continue; }
    if (!trimmed) { flushPara(); flushList(); continue; }
    const heading = trimmed.match(/^(#{1,4})\s+(.+)$/);
    if (heading) {
      flushPara(); flushList();
      out.push(`<h${heading[1].length}>${renderMarkdownInline(heading[2])}</h${heading[1].length}>`);
      continue;
    }
    if (trimmed.startsWith('- ') || trimmed.startsWith('* ')) {
      flushPara();
      openList('ul');
      out.push(`<li>${renderMarkdownInline(trimmed.slice(2).trim())}</li>`);
      continue;
    }
    const ordered = trimmed.match(/^\d+\.\s+(.+)$/);
    if (ordered) {
      flushPara();
      openList('ol');
      out.push(`<li>${renderMarkdownInline(ordered[1])}</li>`);
      continue;
    }
    if (/^\*\*[^*]+:\*\*/.test(trimmed)) {
      flushPara();
      openList('ul');
      out.push(`<li>${renderMarkdownInline(trimmed)}</li>`);
      continue;
    }
    flushList();
    para.push(trimmed);
  }
  flushPara();
  flushList();
  if (codeOpen) out.push('</code></pre>');
  return out.join('');
}

function focusChatInput(root) {
  const textarea = root.querySelector?.('[data-chat-input]');
  if (textarea && !textarea.disabled) textarea.focus({ preventScroll: true });
}

function appendAssistantMarkdown(body, text) {
  if (!body) return;
  if (body.textContent === 'Thinking…') body.textContent = '';
  body.parentElement?.classList.remove('chat-typing');
  body.dataset.markdown = (body.dataset.markdown || '') + text;
  body.innerHTML = renderChatMarkdown(body.dataset.markdown);
}

function isChatAtBottom(box) {
  return !box || (box.scrollHeight - box.scrollTop - box.clientHeight) < 48;
}

function chatBottomSentinel(box) {
  if (!box) return null;
  let sentinel = box.querySelector(':scope > [data-chat-bottom]');
  if (!sentinel) {
    sentinel = document.createElement('div');
    sentinel.dataset.chatBottom = '1';
    sentinel.className = 'chat-bottom-sentinel';
    box.appendChild(sentinel);
  }
  return sentinel;
}

function setChatAutoScroll(box) {
  if (box) box.dataset.autoScroll = isChatAtBottom(box) ? '1' : '0';
}

function bindChatAutoScroll(box) {
  if (!box || box.dataset.autoScrollBound === '1') return;
  box.dataset.autoScrollBound = '1';
  if (!box.dataset.autoScroll) box.dataset.autoScroll = '1';
  chatBottomSentinel(box);
  box.addEventListener('scroll', () => setChatAutoScroll(box), { passive: true });
  box.addEventListener('wheel', (event) => {
    if (event.deltaY < 0) box.dataset.autoScroll = '0';
  }, { passive: true });
  box.addEventListener('touchmove', () => { box.dataset.autoScroll = '0'; }, { passive: true });
}

function scrollChatIfFollowing(box) {
  if (!box || box.dataset.autoScroll === '0') return;
  const sentinel = chatBottomSentinel(box);
  requestAnimationFrame(() => {
    if (box.dataset.autoScroll === '0') return;
    sentinel?.scrollIntoView({ block: 'end' });
    box.scrollTop = box.scrollHeight;
    requestAnimationFrame(() => {
      if (box.dataset.autoScroll === '0') return;
      sentinel?.scrollIntoView({ block: 'end' });
      box.scrollTop = box.scrollHeight;
    });
  });
}

function appendChatMessageToBox(boxId, role, content, extraClass) {
  const box = document.getElementById(boxId);
  if (!box) return null;
  box.querySelector(':scope > .chat-empty')?.remove();
  const msg = document.createElement('div');
  msg.className = `chat-msg chat-msg-${role}${extraClass ? ' ' + extraClass : ''}`;
  const label = document.createElement('div');
  label.className = 'chat-msg-label';
  label.textContent = role;
  const body = document.createElement('div');
  body.className = 'chat-msg-body';
  body.textContent = content;
  msg.append(label, body);
  box.insertBefore(msg, chatBottomSentinel(box));
  scrollChatIfFollowing(box);
  return body;
}

async function submitChatMessage(form) {
  const textarea = form.querySelector('textarea[name="content"]');
  const content = textarea ? textarea.value.trim() : '';
  if (!content) return;
  const box = document.getElementById('chat-messages');
  if (box) box.dataset.autoScroll = '1';
  appendChatMessageToBox('chat-messages', 'user', content);
  textarea.value = '';
  liveChatAssistantBody = appendChatMessageToBox('chat-messages', 'assistant', 'Thinking…', 'chat-typing');
  setChatDebug('Sending prompt to Cerberus...', 'submit');
  setChatInputDisabled(true);
  try {
    await sendJSON((form.method || 'POST').toUpperCase(), form.action, { content });
  } catch (err) {
    setChatInputDisabled(false);
    setChatDebug('Request failed: ' + (err.message || String(err)), 'request_error');
    if (liveChatAssistantBody) liveChatAssistantBody.textContent = 'Error: ' + (err.message || String(err));
    showError(err.message || String(err));
    toast(err.message || String(err), 'error');
  }
}

function initChatStream(root) {
  const el = root.querySelector?.('[data-chat-stream]');
  if (!el) return;
  bindChatAutoScroll(root.querySelector('#chat-messages'));
  if (chatSource) { chatSource.close(); chatSource = null; }
  const finish = () => {
    setChatDebug('Turn complete. Refreshing transcript...', 'finish');
    setChatInputDisabled(false);
    liveChatAssistantBody = null;
    clearTimeout(refreshTimer);
    refreshTimer = setTimeout(() => refresh(`/chat/${el.dataset.chatId}/fragment`, '#app'), 250);
  };
  chatSource = new EventSource(el.dataset.chatStream);
  setChatDebug('Stream connected. Ready for prompt.', 'open');
  focusChatInput(root);
  chatSource.addEventListener('text_delta', (ev) => {
    try {
      setChatDebug('Streaming response...', 'text_delta');
      const text = JSON.parse(ev.data).content || '';
      if (!liveChatAssistantBody) liveChatAssistantBody = appendChatMessageToBox('chat-messages', 'assistant', '');
      if (liveChatAssistantBody) {
        appendAssistantMarkdown(liveChatAssistantBody, text);
        scrollChatIfFollowing(liveChatAssistantBody.closest('.chat-messages'));
      }
    } catch (_) {}
  });
  ['message_end', 'turn_complete', 'error'].forEach((name) => chatSource.addEventListener(name, finish));
  chatSource.onerror = () => {
    setChatDebug('Stream closed. Refreshing transcript...', 'error');
    finish();
  };
}

function initStreams(root) {
  initWorkflowStream(root);
  initDraftStream(root);
  initLogStream(root);
  initChatStream(root);
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
    if (!event.detail.target.querySelector('[data-chat-stream]') && chatSource) { chatSource.close(); chatSource = null; }
    if (logSource) { logSource.close(); logSource = null; }
  }
  initStreams(event.detail.target);
});

window.addEventListener('popstate', () => {
  setTimeout(() => refresh(fragmentURL(location.href), '#app'), 50);
});

document.body.addEventListener('htmx:historyRestore', () => {
  setTimeout(() => refresh(fragmentURL(location.href), '#app'), 0);
});

document.addEventListener('DOMContentLoaded', () => initStreams(document));
