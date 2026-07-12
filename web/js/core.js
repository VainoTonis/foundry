// Foundry UI: local, dependency-light progressive enhancement.
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

