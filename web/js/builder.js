// Foundry UI: local, dependency-light progressive enhancement.
'use strict';

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

