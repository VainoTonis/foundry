// Sessions page: live tail of cerberus session events.
'use strict';

let sessionSource;
let sessionTextBody;

function appendSessionEvent(box, kind, text) {
  if (!box) return null;
  const row = document.createElement('div');
  row.className = `chat-msg chat-msg-${kind === 'tool_use' ? 'tool' : 'assistant'}`;
  const label = document.createElement('div');
  label.className = 'chat-msg-label';
  label.textContent = kind === 'tool_use' ? 'tool' : 'assistant';
  const body = document.createElement('div');
  body.className = 'chat-msg-body';
  body.textContent = text;
  row.append(label, body);
  box.appendChild(row);
  box.scrollTop = box.scrollHeight;
  return body;
}

function initSessionStream(root) {
  const el = root.querySelector?.('[data-session-stream]');
  if (sessionSource) { sessionSource.close(); sessionSource = null; }
  sessionTextBody = null;
  if (!el) return;
  const box = document.getElementById('session-stream');
  const conn = root.querySelector('[data-session-connection]');
  if (conn) conn.textContent = 'Connecting…';
  sessionSource = new EventSource(el.dataset.sessionStream);
  sessionSource.onopen = () => { if (conn) conn.textContent = 'Live'; };
  sessionSource.addEventListener('text_delta', (ev) => {
    try {
      const text = JSON.parse(ev.data).content || '';
      if (!text) return;
      if (!sessionTextBody) sessionTextBody = appendSessionEvent(box, 'text', '');
      if (sessionTextBody) {
        sessionTextBody.textContent += text;
        if (box) box.scrollTop = box.scrollHeight;
      }
    } catch (_) {}
  });
  sessionSource.addEventListener('tool_use', (ev) => {
    try {
      const data = JSON.parse(ev.data);
      sessionTextBody = null;
      appendSessionEvent(box, 'tool_use', `${data.tool_name || 'tool'}: ${data.tool_input || ''}`);
    } catch (_) {}
  });
  ['message_end', 'turn_complete'].forEach((name) => sessionSource.addEventListener(name, () => { sessionTextBody = null; }));
  sessionSource.onerror = () => { if (conn) conn.textContent = 'Reconnecting…'; };
}
