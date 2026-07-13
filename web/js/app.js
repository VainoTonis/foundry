// Foundry UI: local, dependency-light progressive enhancement.
'use strict';

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
    const page = event.detail.target.firstElementChild?.dataset.page || 'plans';
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

// Shared console interactions keep list pages and chat history behavior identical.
document.addEventListener('input', (event) => {
  const input = event.target.closest('[data-list-search]');
  if (!input) return;
  const list = document.getElementById(input.dataset.listSearch);
  if (!list) return;
  const query = input.value.trim().toLowerCase();
  Array.from(list.children).forEach((row) => {
    row.hidden = query !== '' && !row.textContent.toLowerCase().includes(query);
  });
});

document.addEventListener('click', (event) => {
  const toggle = event.target.closest('[data-chat-history-toggle]');
  if (toggle) {
    event.preventDefault();
    document.querySelector('.console-chat')?.classList.toggle('history-collapsed');
    return;
  }

  const row = event.target.closest('[data-row-href]');
  if (!row || event.target.closest('button, a, form, input, select, textarea')) return;
  htmx.ajax('GET', row.dataset.rowFragment, {target: '#app', swap: 'innerHTML'});
  history.pushState({}, '', row.dataset.rowHref);
});

document.addEventListener('keydown', (event) => {
  const row = event.target.closest('[data-row-href]');
  if (row && (event.key === 'Enter' || event.key === ' ')) {
    event.preventDefault();
    htmx.ajax('GET', row.dataset.rowFragment, {target: '#app', swap: 'innerHTML'});
    history.pushState({}, '', row.dataset.rowHref);
  }
});

// --- chat settings dialog ---

function openChatSettings() {
  const dialog = document.getElementById('chat-settings-dialog');
  if (dialog) dialog.showModal();
}

function closeChatSettings() {
  const dialog = document.getElementById('chat-settings-dialog');
  if (dialog) dialog.close();
}

document.addEventListener('click', (event) => {
  if (event.target.closest('#chat-settings-btn')) {
    event.preventDefault();
    openChatSettings();
    return;
  }

  if (event.target.closest('#chat-settings-close')) {
    closeChatSettings();
    return;
  }

  // "Add" button in the settings dialog project section.
  const addProjectBtn = event.target.closest('#chat-add-project-btn');
  if (addProjectBtn) {
    event.preventDefault();
    const sessionId = addProjectBtn.dataset.sessionId;
    const select = document.getElementById('chat-add-project-select');
    const projectId = select?.value;
    if (!projectId) return;
    sendJSON('POST', `/api/chat/sessions/${sessionId}/projects`, { project_id: Number(projectId) })
      .then(() => { closeChatSettings(); refresh(`/chat/${sessionId}/fragment`, '#app'); })
      .catch((err) => toast(err.message || String(err), 'error'));
    return;
  }

  // Remove project chip inside dialog.
  const detachBtn = event.target.closest('[data-detach-project]');
  if (detachBtn) {
    event.preventDefault();
    const sessionId = detachBtn.dataset.sessionId;
    const projectId = detachBtn.dataset.projectId;
    sendJSON('DELETE', `/api/chat/sessions/${sessionId}/projects/${projectId}`)
      .then(() => { closeChatSettings(); refresh(`/chat/${sessionId}/fragment`, '#app'); })
      .catch((err) => toast(err.message || String(err), 'error'));
    return;
  }
});

// Profile select change → PATCH immediately.
document.addEventListener('change', (event) => {
  const select = event.target.closest('#chat-profile-select');
  if (!select) return;
  const sessionId = select.dataset.sessionId;
  sendJSON('PATCH', `/api/chat/sessions/${sessionId}/profile`, { profile_name: select.value })
    .then(() => toast('Profile updated', 'success'))
    .catch((err) => { toast(err.message || String(err), 'error'); });
});

// Close dialog on backdrop click.
// When the backdrop is clicked, event.target is the <dialog> element itself.
// Clicking children inside the dialog gives a different target, so this is safe.
document.addEventListener('click', (event) => {
  const dialog = document.getElementById('chat-settings-dialog');
  if (event.target === dialog) dialog.close();
});
