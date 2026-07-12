// Foundry UI: local, dependency-light progressive enhancement.
'use strict';

function setChatInputDisabled(disabled) {
  const form = document.querySelector('form[data-chat-message]');
  if (!form) return;
  form.querySelectorAll('textarea, select, button').forEach((el) => { el.disabled = disabled; });
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
  const profile = form.querySelector('select[name="profile_name"]');
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
    await sendJSON((form.method || 'POST').toUpperCase(), form.action, { content, profile_name: profile ? profile.value : undefined });
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

  // Move dialog to body so it isn't clipped by any ancestor overflow/transform.
  // Remove any stale orphaned dialog from a previous fragment swap first.
  document.querySelectorAll('body > .chat-settings-dialog').forEach((stale) => stale.remove());
  const dialog = root.querySelector?.('.chat-settings-dialog');
  if (dialog) document.body.appendChild(dialog);
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

