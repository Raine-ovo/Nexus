/* =============================================================
 * Nexus Web UI — app.js
 * 纯 vanilla JavaScript，零依赖。由 nexus-server 以 go:embed 方式
 * 提供服务，因此所有请求一律使用相对路径（window.location.origin）。
 * ============================================================= */
(function () {
  'use strict';

  /* ---------------- 工具函数 ---------------- */
  var $ = function (sel) { return document.querySelector(sel); };
  var $$ = function (sel) { return Array.prototype.slice.call(document.querySelectorAll(sel)); };

  // HTML 转义：先转义，再套 markdown 标记，防止 XSS
  function escapeHtml(s) {
    return String(s == null ? '' : s)
      .replace(/&/g, '&amp;')
      .replace(/</g, '&lt;')
      .replace(/>/g, '&gt;')
      .replace(/"/g, '&quot;')
      .replace(/'/g, '&#39;');
  }

  function genId(prefix) {
    return (prefix || 'id') + '_' + Date.now().toString(36) + Math.random().toString(36).slice(2, 8);
  }

  function formatTime(iso) {
    if (!iso) return '';
    var d = new Date(iso);
    if (isNaN(d.getTime())) return '';
    var p = function (n) { return (n < 10 ? '0' : '') + n; };
    return (d.getMonth() + 1) + '/' + d.getDate() + ' ' + p(d.getHours()) + ':' + p(d.getMinutes());
  }

  // 会话标题：取首条用户消息前 24 字
  function makeTitle(text) {
    var t = String(text || '').replace(/\s+/g, ' ').trim();
    if (!t) return '新会话';
    return t.length > 24 ? t.slice(0, 24) + '…' : t;
  }

  var toastTimer = null;
  function toast(msg) {
    var el = $('#toast');
    el.textContent = msg;
    el.classList.remove('hidden');
    clearTimeout(toastTimer);
    toastTimer = setTimeout(function () { el.classList.add('hidden'); }, 2400);
  }

  function fallbackCopy(text) {
    var ta = document.createElement('textarea');
    ta.value = text;
    ta.setAttribute('readonly', '');
    ta.style.position = 'fixed';
    ta.style.top = '0';
    ta.style.opacity = '0';
    document.body.appendChild(ta);
    ta.focus();
    ta.select();
    try { document.execCommand('copy'); } catch (e) { /* 忽略 */ }
    document.body.removeChild(ta);
  }
  function copyText(text) {
    if (navigator.clipboard && navigator.clipboard.writeText) {
      navigator.clipboard.writeText(text).then(
        function () { toast('已复制'); },
        function () { fallbackCopy(text); toast('已复制'); }
      );
    } else {
      fallbackCopy(text);
      toast('已复制');
    }
  }

  /* ---------------- Markdown 渲染（轻量、安全、无依赖） ---------------- */
  function renderInline(text) {
    // text 已经做过 HTML 转义
    var codeTokens = [];
    // 1) 行内代码（占位保护）
    text = text.replace(/`([^`]+)`/g, function (m, c) {
      codeTokens.push('<code>' + c + '</code>');
      return '\u0000C' + (codeTokens.length - 1) + '\u0000';
    });
    // 2) 链接（仅 http/https）
    text = text.replace(/\[([^\]]+)\]\((https?:\/\/[^\s)]+)\)/g, function (m, label, url) {
      return '<a href="' + url + '" target="_blank" rel="noopener noreferrer">' + label + '</a>';
    });
    // 3) 粗体 / 斜体
    text = text.replace(/\*\*([^*]+)\*\*/g, '<strong>$1</strong>');
    text = text.replace(/\*([^*]+)\*/g, '<em>$1</em>');
    // 4) 还原行内代码
    text = text.replace(/\u0000C(\d+)\u0000/g, function (m, idx) { return codeTokens[+idx]; });
    return text;
  }

  function renderMarkdown(md) {
    if (md == null) return '';
    var src = String(md).replace(/\r\n/g, '\n').replace(/\r/g, '\n');
    var lines = src.split('\n');
    var html = '';
    var i = 0;
    var list = null;

    function flushList() {
      if (!list) return;
      var items = list.items.map(function (it) { return '<li>' + it + '</li>'; }).join('');
      html += '<' + list.tag + '>' + items + '</' + list.tag + '>';
      list = null;
    }
    function isHeading(l) { return /^#{1,6}\s+/.test(l); }
    function isFence(l) { return /^```/.test(l); }
    function isQuote(l) { return /^>\s?/.test(l); }
    function isUl(l) { return /^\s*[-*+]\s+/.test(l); }
    function isOl(l) { return /^\s*\d+[.)]\s+/.test(l); }

    while (i < lines.length) {
      var line = lines[i];

      // 围栏代码块
      if (isFence(line)) {
        flushList();
        var fm = line.match(/^```(\S*)/);
        var lang = (fm && fm[1]) ? fm[1] : '';
        i++;
        var code = [];
        while (i < lines.length && !/^```\s*$/.test(lines[i])) {
          code.push(lines[i]);
          i++;
        }
        i++; // 跳过结束围栏
        var raw = code.join('\n');
        html += '<div class="code-block">' +
          '<div class="code-block-head"><span class="code-lang">' + escapeHtml(lang || 'code') + '</span>' +
          '<button class="code-copy" data-code="' + encodeURIComponent(raw) + '">复制</button></div>' +
          '<pre><code>' + escapeHtml(raw) + '</code></pre></div>';
        continue;
      }

      // 标题
      if (isHeading(line)) {
        flushList();
        var hm = line.match(/^(#{1,6})\s+(.*)$/);
        var level = hm[1].length;
        html += '<h' + level + '>' + renderInline(escapeHtml(hm[2])) + '</h' + level + '>';
        i++;
        continue;
      }

      // 引用
      if (isQuote(line)) {
        flushList();
        var q = [];
        while (i < lines.length && isQuote(lines[i])) {
          q.push(lines[i].replace(/^>\s?/, ''));
          i++;
        }
        html += '<blockquote>' + renderInline(escapeHtml(q.join(' '))) + '</blockquote>';
        continue;
      }

      // 列表
      var ulM = line.match(/^\s*[-*+]\s+(.*)$/);
      var olM = line.match(/^\s*\d+[.)]\s+(.*)$/);
      if (ulM || olM) {
        var tag = ulM ? 'ul' : 'ol';
        if (!list || list.tag !== tag) {
          flushList();
          list = { tag: tag, items: [] };
        }
        list.items.push(renderInline(escapeHtml((ulM || olM)[1])));
        i++;
        continue;
      }

      // 空行
      if (line.trim() === '') {
        flushList();
        i++;
        continue;
      }

      // 段落
      flushList();
      var para = [];
      while (i < lines.length && lines[i].trim() !== '' &&
             !isHeading(lines[i]) && !isFence(lines[i]) && !isQuote(lines[i]) &&
             !isUl(lines[i]) && !isOl(lines[i])) {
        para.push(lines[i]);
        i++;
      }
      html += '<p>' + renderInline(escapeHtml(para.join(' '))) + '</p>';
    }
    flushList();
    return html;
  }

  /* ---------------- 状态 ---------------- */
  var state = {
    meta: null,
    sessions: [],
    currentSessionId: null,
    streaming: false,
    streamAbort: null,
    elapsed: 0,
    elapsedTimer: null,
    creatingSession: false,
    approvals: [],
    rightPanelOpen: false,
    rightPanelTab: 'approvals',
    debugLoaded: false,
    electronInfo: null,
    traceSpans: [],
    lastRequestId: null,
    tracePolling: null,
    teamData: null
  };
  var renderTimers = {};

  var LS_SESSIONS = 'nexus.sessions';
  var LS_CURRENT = 'nexus.currentSession';

  /* ---------------- DOM 引用 ---------------- */
  var els = {};
  function cacheEls() {
    var ids = [
      'version-badge', 'model-badge', 'status-dot', 'status-text', 'status-pill',
      'approval-badge', 'approval-badge-btn', 'debug-btn', 'trace-btn', 'team-btn', 'settings-btn', 'sidebar-toggle',
      'sidebar', 'new-session-btn', 'session-list', 'session-count',
      'messages', 'empty-state', 'thinking', 'thinking-text', 'thinking-time', 'stop-btn', 'chat-scroll',
      'workstream-toggle', 'workstream-fields', 'scope-input', 'workstream-input',
      'composer', 'send-btn', 'right-panel', 'right-panel-close',
      'tab-approvals', 'tab-debug', 'tab-trace', 'tab-team', 'approvals-list', 'approvals-empty', 'approvals-refresh',
      'debug-body', 'debug-refresh', 'trace-stage', 'trace-body', 'trace-refresh',
      'team-scope', 'team-members', 'team-refresh', 'team-new-name', 'team-new-role', 'team-new-prompt', 'team-spawn-btn',
      'settings-modal', 'set-model', 'set-base-url',
      'set-api-key', 'set-workspace', 'select-workspace-btn', 'settings-mode-hint',
      'electron-info', 'log-toggle', 'log-panel', 'log-output', 'settings-msg',
      'settings-save', 'settings-cancel', 'toast'
    ];
    ids.forEach(function (id) { els[camel(id)] = document.getElementById(id); });
  }
  function camel(id) {
    return id.replace(/-([a-z])/g, function (m, c) { return c.toUpperCase(); });
  }

  /* ---------------- 持久化 ---------------- */
  function loadSessions() {
    try {
      var raw = localStorage.getItem(LS_SESSIONS);
      var arr = raw ? JSON.parse(raw) : [];
      return Array.isArray(arr) ? arr : [];
    } catch (e) { return []; }
  }
  function saveSessions() {
    try { localStorage.setItem(LS_SESSIONS, JSON.stringify(state.sessions)); } catch (e) { /* 忽略 */ }
  }
  function getSession(id) {
    for (var i = 0; i < state.sessions.length; i++) {
      if (state.sessions[i].id === id) return state.sessions[i];
    }
    return null;
  }
  function currentSession() { return getSession(state.currentSessionId); }

  /* ---------------- API 封装（全部相对路径） ---------------- */
  async function apiGet(path) {
    var resp = await fetch(path, { headers: { 'Accept': 'application/json' } });
    if (!resp.ok) throw new Error('HTTP ' + resp.status);
    return resp.json();
  }
  async function apiPost(path, body, extraHeaders) {
    var headers = { 'Content-Type': 'application/json' };
    if (extraHeaders) {
      for (var k in extraHeaders) headers[k] = extraHeaders[k];
    }
    var resp = await fetch(path, {
      method: 'POST',
      headers: headers,
      body: JSON.stringify(body)
    });
    if (!resp.ok) throw new Error('HTTP ' + resp.status);
    return resp.json();
  }
  async function apiDelete(path) {
    var resp = await fetch(path, { method: 'DELETE' });
    if (!resp.ok) throw new Error('HTTP ' + resp.status);
    return resp.json();
  }

  /* ---------------- 连接状态 / meta ---------------- */
  function setStatus(ok) {
    els.statusDot.className = 'dot ' + (ok ? 'dot-on' : 'dot-off');
    els.statusText.textContent = ok ? '已连接' : '未连接';
    els.statusPill.className = 'status-pill ' + (ok ? 'ok' : 'bad');
  }
  async function checkHealth() {
    try {
      var resp = await fetch('/api/health');
      if (resp.ok) {
        var data = await resp.json().catch(function () { return {}; });
        if (data.status === 'ok') { setStatus(true); return; }
      }
      setStatus(false);
    } catch (e) { setStatus(false); }
  }
  async function loadMeta() {
    try {
      var data = await apiGet('/api/meta');
      state.meta = data;
      els.modelBadge.textContent = data.model || '未知模型';
      els.versionBadge.textContent = 'v' + (data.version || '0.0.0');
    } catch (e) {
      els.modelBadge.textContent = '模型不可用';
    }
  }

  /* ---------------- 会话 ---------------- */
  function renderSessionList() {
    els.sessionList.innerHTML = '';
    if (!state.sessions.length) {
      els.sessionList.innerHTML = '<div class="session-empty muted">暂无会话</div>';
      els.sessionCount.textContent = '0 个会话';
      return;
    }
    state.sessions.forEach(function (s) {
      var item = document.createElement('div');
      item.className = 'session-item' + (s.id === state.currentSessionId ? ' active' : '');
      item.dataset.id = s.id;

      var title = document.createElement('div');
      title.className = 'session-title';
      title.textContent = s.title || '新会话';

      var meta = document.createElement('div');
      meta.className = 'session-meta';
      var tags = [];
      if (s.scope) tags.push('scope: ' + s.scope);
      if (s.workstream) tags.push(s.workstream);
      meta.textContent = tags.join(' · ') || (s.created_at ? formatTime(s.created_at) : '');

      var del = document.createElement('button');
      del.className = 'session-del';
      del.title = '删除会话';
      del.innerHTML = '<svg viewBox="0 0 24 24" width="15" height="15" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><path d="M3 6h18M8 6V4h8v2M6 6l1 14h10l1-14"/></svg>';

      item.appendChild(title);
      item.appendChild(meta);
      item.appendChild(del);
      els.sessionList.appendChild(item);
    });
    els.sessionCount.textContent = state.sessions.length + ' 个会话';
  }

  function switchSession(id) {
    var s = getSession(id);
    if (!s) return;
    state.currentSessionId = id;
    try { localStorage.setItem(LS_CURRENT, id); } catch (e) { /* 忽略 */ }
    els.scopeInput.value = s.scope || '';
    els.workstreamInput.value = s.workstream || '';
    renderSessionList();
    renderMessages();
    // 切换会话时刷新团队面板；清空旧会话的运行轨迹
    if (state.rightPanelOpen && state.rightPanelTab === 'team') loadTeam();
    if (state.rightPanelOpen && state.rightPanelTab === 'trace') {
      state.traceSpans = [];
      state.lastRequestId = null;
      renderTrace();
    }
  }

  async function createSession() {
    if (state.streaming || state.creatingSession) return;
    state.creatingSession = true;
    var body = {
      channel: 'desktop',
      user: 'me',
      scope: els.scopeInput.value.trim(),
      workstream: els.workstreamInput.value.trim()
    };
    var session = null;
    try {
      var resp = await apiPost('/api/sessions', body);
      session = {
        id: resp.session_id || genId('sess'),
        channel: 'desktop',
        user: 'me',
        scope: resp.scope || body.scope,
        workstream: resp.workstream || body.workstream,
        created_at: new Date().toISOString(),
        title: '新会话',
        messages: []
      };
    } catch (e) {
      // 服务器不可用时也创建本地会话，保证 UI 可用
      session = {
        id: genId('sess'),
        channel: 'desktop',
        user: 'me',
        scope: body.scope,
        workstream: body.workstream,
        created_at: new Date().toISOString(),
        title: '新会话',
        messages: []
      };
      toast('服务器不可用，已创建本地会话');
    }
    state.sessions.unshift(session);
    saveSessions();
    switchSession(session.id);
    state.creatingSession = false;
  }

  async function deleteSession(id) {
    var removed = false;
    try {
      await apiDelete('/api/sessions/' + encodeURIComponent(id));
      removed = true;
    } catch (e) {
      if (e.message === 'HTTP 404') removed = true;
    }
    if (!removed) { toast('删除失败：' + e.message); return; }
    state.sessions = state.sessions.filter(function (s) { return s.id !== id; });
    if (state.currentSessionId === id) {
      state.currentSessionId = state.sessions.length ? state.sessions[0].id : null;
    }
    if (state.currentSessionId) {
      try { localStorage.setItem(LS_CURRENT, state.currentSessionId); } catch (e) { /* 忽略 */ }
    } else {
      try { localStorage.removeItem(LS_CURRENT); } catch (e) { /* 忽略 */ }
    }
    saveSessions();
    renderSessionList();
    renderMessages();
    if (!state.sessions.length) createSession();
  }

  /* ---------------- 消息 ---------------- */
  function appendMessageDom(m) {
    var div = document.createElement('div');
    div.className = 'msg ' + (m.role === 'user' ? 'msg-user' : 'msg-assistant');
    div.dataset.msgId = m.id;

    var role = document.createElement('div');
    role.className = 'msg-role';
    role.textContent = m.role === 'user' ? '你' : 'Nexus';

    var bubble = document.createElement('div');
    bubble.className = 'msg-bubble';
    if (m.role === 'user') {
      bubble.textContent = m.content;
    } else {
      if (m.error) bubble.classList.add('error');
      bubble.innerHTML = (m.error ? '<span class="error-label">错误</span>' : '') + renderMarkdown(m.content);
    }

    div.appendChild(role);
    div.appendChild(bubble);

    if (m.role === 'assistant') {
      var actions = document.createElement('div');
      actions.className = 'msg-actions';
      var btn = document.createElement('button');
      btn.className = 'msg-copy';
      btn.textContent = '复制';
      actions.appendChild(btn);
      if (m.requestId) {
        var traceBtn = document.createElement('button');
        traceBtn.className = 'msg-trace';
        traceBtn.textContent = '查看轨迹';
        actions.appendChild(traceBtn);
      }
      div.appendChild(actions);
    }

    els.messages.appendChild(div);
  }

  function renderMessages() {
    els.messages.innerHTML = '';
    var s = currentSession();
    if (s && s.messages && s.messages.length) {
      s.messages.forEach(appendMessageDom);
      els.emptyState.classList.add('hidden');
    } else {
      els.emptyState.classList.remove('hidden');
    }
    scrollToBottom();
  }

  function findMessage(id) {
    var s = currentSession();
    if (!s) return null;
    for (var i = 0; i < (s.messages || []).length; i++) {
      if (s.messages[i].id === id) return s.messages[i];
    }
    return null;
  }

  function addMessage(role, content, extra) {
    var s = currentSession();
    var m = { id: genId('msg'), role: role, content: content, error: false };
    if (extra) {
      for (var k in extra) m[k] = extra[k];
    }
    if (s) {
      if (!s.messages) s.messages = [];
      s.messages.push(m);
    }
    appendMessageDom(m);
    els.emptyState.classList.add('hidden');
    scrollToBottom();
    return m;
  }

  function updateBubble(msgId) {
    var el = document.querySelector('[data-msg-id="' + msgId + '"]');
    if (!el) return;
    var m = findMessage(msgId);
    if (!m) return;
    var bubble = el.querySelector('.msg-bubble');
    if (m.error) {
      bubble.classList.add('error');
      bubble.innerHTML = '<span class="error-label">错误</span>' + renderMarkdown(m.content);
    } else {
      bubble.classList.remove('error');
      bubble.innerHTML = renderMarkdown(m.content);
    }
  }

  function scrollToBottom() {
    els.chatScroll.scrollTop = els.chatScroll.scrollHeight;
  }

  function scheduleRender(msgId) {
    clearTimeout(renderTimers[msgId]);
    renderTimers[msgId] = setTimeout(function () {
      updateBubble(msgId);
      scrollToBottom();
    }, 100);
  }

  /* ---------------- 流式 / 思考 ---------------- */
  function startThinking() {
    els.thinking.classList.remove('hidden');
    els.thinkingText.textContent = '正在思考…';
    state.elapsed = 0;
    els.thinkingTime.textContent = '0s';
    clearInterval(state.elapsedTimer);
    state.elapsedTimer = setInterval(function () {
      state.elapsed++;
      els.thinkingTime.textContent = state.elapsed + 's';
    }, 1000);
  }
  function stopThinking() {
    els.thinking.classList.add('hidden');
    clearInterval(state.elapsedTimer);
    state.elapsedTimer = null;
  }

  function setBusyUI(busy) {
    els.sendBtn.disabled = busy;
    els.newSessionBtn.disabled = busy;
    if (busy) {
      els.sidebar.classList.add('busy');
    } else {
      els.sidebar.classList.remove('busy');
    }
  }

  function handleSSEFrame(frame, msgId) {
    var lines = frame.split('\n');
    var data = null;
    for (var i = 0; i < lines.length; i++) {
      var line = lines[i];
      if (line.indexOf('data:') === 0) {
        var payload = line.slice(5).trim();
        if (!payload) continue;
        try { data = JSON.parse(payload); } catch (e) { data = null; }
        if (data) break;
      }
    }
    if (!data) return;
    var m = findMessage(msgId);
    if (!m) return;

    if (data.type === 'delta') {
      m.content += (data.content || '');
      scheduleRender(msgId);
    } else if (data.type === 'done') {
      if (data.error) {
        m.content = data.error;
        m.error = true;
      } else {
        m.content = data.output || m.content;
      }
      updateBubble(msgId);
    } else if (data.type === 'error') {
      m.content = data.message || '发生未知错误';
      m.error = true;
      updateBubble(msgId);
    }
  }

  async function readSSEStream(resp, msgId) {
    if (!resp.body) throw new Error('响应体不可读');
    var reader = resp.body.getReader();
    var decoder = new TextDecoder();
    var buffer = '';
    while (true) {
      var r = await reader.read();
      if (r.done) break;
      buffer += decoder.decode(r.value, { stream: true });
      buffer = buffer.replace(/\r\n/g, '\n');
      var idx;
      while ((idx = buffer.indexOf('\n\n')) !== -1) {
        var frame = buffer.slice(0, idx);
        buffer = buffer.slice(idx + 2);
        handleSSEFrame(frame, msgId);
      }
    }
    if (buffer.trim()) handleSSEFrame(buffer, msgId);
  }

  async function fallbackChat(input, msgId, rid) {
    var s = currentSession();
    var headers = rid ? { 'X-Request-ID': rid } : undefined;
    try {
      var resp = await apiPost('/api/chat', { session_id: s.id, input: input, lane: 'main' }, headers);
      var m = findMessage(msgId);
      if (m) {
        if (resp.error) {
          m.content = resp.error;
          m.error = true;
        } else {
          m.content = resp.output || '';
        }
        updateBubble(msgId);
      }
    } catch (e) {
      var m2 = findMessage(msgId);
      if (m2) {
        m2.content = '请求失败：' + (e.message || e);
        m2.error = true;
        updateBubble(msgId);
      }
    }
  }

  async function sendMessage() {
    var input = els.composer.value.trim();
    if (!input || state.streaming) return;
    var session = currentSession();
    if (!session) { toast('请先创建会话'); return; }

    // 生成请求 ID，用于关联运行轨迹
    var rid = (window.crypto && window.crypto.randomUUID) ? window.crypto.randomUUID() : genId('req');

    // 用户消息
    addMessage('user', input);
    if (!session.title || session.title === '新会话') {
      session.title = makeTitle(input);
      renderSessionList();
    }
    saveSessions();
    els.composer.value = '';
    autoResizeComposer();

    // 助手占位（关联 requestId，便于「查看轨迹」）
    var assistantMsg = addMessage('assistant', '', { requestId: rid });

    state.streaming = true;
    state.lastRequestId = rid;
    state.traceSpans = [];
    setBusyUI(true);
    startThinking();
    startTracePolling(rid);

    var controller = new AbortController();
    state.streamAbort = controller;

    try {
      var resp = await fetch('/api/chat/stream', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json', 'X-Request-ID': rid },
        body: JSON.stringify({ session_id: session.id, input: input, lane: 'main' }),
        signal: controller.signal
      });
      if (!resp.ok) throw new Error('HTTP ' + resp.status);
      var ct = resp.headers.get('content-type') || '';
      if (ct.indexOf('text/event-stream') === -1) throw new Error('非 SSE 响应');
      await readSSEStream(resp, assistantMsg.id);
    } catch (err) {
      if (err && err.name === 'AbortError') {
        // 用户主动停止
        var stopped = findMessage(assistantMsg.id);
        if (stopped) {
          if (!stopped.content) stopped.content = '';
          stopped.content += '\n\n> 已停止生成';
          updateBubble(assistantMsg.id);
        }
      } else {
        // 流式异常：降级到同步接口
        await fallbackChat(input, assistantMsg.id, rid);
      }
    } finally {
      stopTracePolling();
      state.streaming = false;
      state.streamAbort = null;
      setBusyUI(false);
      stopThinking();
      // 最终再拉一次完整轨迹
      if (state.lastRequestId) {
        await loadTrace(state.lastRequestId);
      }
      saveSessions();
      scrollToBottom();
    }
  }

  function autoResizeComposer() {
    els.composer.style.height = 'auto';
    els.composer.style.height = Math.min(els.composer.scrollHeight, 200) + 'px';
  }

  /* ---------------- 审批 ---------------- */
  function updateApprovalBadge(pending) {
    if (pending > 0) {
      els.approvalBadge.textContent = pending;
      els.approvalBadge.classList.remove('hidden');
    } else {
      els.approvalBadge.classList.add('hidden');
    }
  }

  function renderApprovals() {
    var list = (state.approvals || []).filter(function (a) { return a.status === 'pending'; });
    els.approvalsList.innerHTML = '';
    if (!list.length) {
      els.approvalsEmpty.classList.remove('hidden');
      return;
    }
    els.approvalsEmpty.classList.add('hidden');
    list.forEach(function (a) {
      var item = document.createElement('div');
      item.className = 'approval-item';

      var head = document.createElement('div');
      head.className = 'approval-head';
      var tool = document.createElement('span');
      tool.className = 'approval-tool';
      tool.textContent = a.tool_name || '未知工具';
      var time = document.createElement('span');
      time.className = 'approval-time';
      time.textContent = formatTime(a.created_at);
      head.appendChild(tool);
      head.appendChild(time);

      var reason = document.createElement('div');
      reason.className = 'approval-reason';
      reason.textContent = a.reason || '无说明';

      var args = document.createElement('pre');
      args.className = 'approval-args';
      try {
        args.textContent = typeof a.arguments === 'string' ? a.arguments : JSON.stringify(a.arguments, null, 2);
      } catch (e) { args.textContent = String(a.arguments); }

      var actions = document.createElement('div');
      actions.className = 'approval-actions';
      actions.appendChild(approvalBtn('批准一次', 'act-approve', 'approve'));
      actions.appendChild(approvalBtn('始终批准', 'act-approve-all', 'approve-all'));
      actions.appendChild(approvalBtn('拒绝', 'act-deny', 'deny'));

      item.appendChild(head);
      item.appendChild(reason);
      item.appendChild(args);
      item.appendChild(actions);
      item.dataset.id = a.id;
      els.approvalsList.appendChild(item);
    });
  }
  function approvalBtn(text, cls, act) {
    var b = document.createElement('button');
    b.className = cls;
    b.dataset.act = act;
    b.textContent = text;
    return b;
  }

  async function loadApprovals() {
    try {
      var data = await apiGet('/api/approvals');
      state.approvals = data.approvals || [];
      var pending = data.pending != null ? data.pending :
        state.approvals.filter(function (a) { return a.status === 'pending'; }).length;
      updateApprovalBadge(pending);
      if (state.rightPanelOpen && state.rightPanelTab === 'approvals') renderApprovals();
    } catch (e) { /* 静默失败，保留旧数据 */ }
  }

  async function approvalAction(id, action) {
    try {
      if (action === 'deny') {
        await apiPost('/api/approvals/' + encodeURIComponent(id) + '/deny', {});
      } else {
        await apiPost('/api/approvals/' + encodeURIComponent(id) + '/approve', { persist: action === 'approve-all' });
      }
      toast(action === 'deny' ? '已拒绝' : '已批准');
      loadApprovals();
    } catch (e) {
      toast('操作失败：' + e.message);
    }
  }

  /* ---------------- 调试 ---------------- */
  async function loadDebug() {
    els.debugBody.innerHTML = '<div class="muted pad">加载中…</div>';
    try {
      var results = await Promise.all([
        apiGet('/api/debug/traces'),
        apiGet('/api/debug/scopes'),
        apiGet('/api/debug/metrics')
      ]);
      renderDebug(results[0], results[1], results[2]);
      state.debugLoaded = true;
    } catch (e) {
      els.debugBody.innerHTML = '<div class="muted pad">加载调试数据失败：' + escapeHtml(e.message || e) + '</div>';
    }
  }

  function renderDebug(tracesData, scopesData, metricsData) {
    var html = '';

    // Traces
    var traces = (tracesData && tracesData.traces) || [];
    html += collapsibleStart('Traces', traces.length);
    if (traces.length) {
      html += '<div class="collapsible-body"><table class="debug-table"><thead><tr>' +
        '<th>operation</th><th>status</th><th>span_count</th><th>request_id</th><th>scope_decision</th>' +
        '</tr></thead><tbody>';
      traces.forEach(function (t) {
        var st = (t.status || '').toLowerCase();
        var chip = 'status-chip ' + (st === 'ok' ? 'ok' : (st === 'error' ? 'error' : 'pending'));
        html += '<tr data-trace-id="' + escapeHtml(t.trace_id || '') + '">' +
          '<td>' + escapeHtml(t.operation || '') + '</td>' +
          '<td><span class="' + chip + '">' + escapeHtml(t.status || '') + '</span></td>' +
          '<td>' + escapeHtml(t.span_count == null ? '' : t.span_count) + '</td>' +
          '<td>' + escapeHtml(t.request_id || '') + '</td>' +
          '<td>' + escapeHtml(t.scope_decision || '') + '</td>' +
          '</tr>';
      });
      html += '</tbody></table></div>';
    } else {
      html += '<div class="collapsible-body muted pad">暂无 traces</div>';
    }
    html += '</div>';

    // Scopes
    var scopes = (scopesData && scopesData.scopes) || [];
    html += collapsibleStart('Scopes', scopes.length);
    if (scopes.length) {
      html += '<div class="collapsible-body"><table class="debug-table"><thead><tr>' +
        '<th>scope</th><th>kind</th><th>lifecycle</th><th>workstream</th><th>user</th><th>updated</th><th>team_dir</th>' +
        '</tr></thead><tbody>';
      scopes.forEach(function (s) {
        html += '<tr>' +
          '<td>' + escapeHtml(s.scope || '') + '</td>' +
          '<td>' + escapeHtml(s.scope_kind || '') + '</td>' +
          '<td>' + escapeHtml(s.lifecycle || '') + '</td>' +
          '<td>' + escapeHtml(s.workstream || '') + '</td>' +
          '<td>' + escapeHtml(s.user || '') + '</td>' +
          '<td>' + escapeHtml(formatTime(s.updated_at)) + '</td>' +
          '<td>' + escapeHtml(s.team_dir || '') + '</td>' +
          '</tr>';
      });
      html += '</tbody></table></div>';
    } else {
      html += '<div class="collapsible-body muted pad">暂无 scopes</div>';
    }
    html += '</div>';

    // Metrics
    html += collapsibleStart('Metrics', 1);
    var metricsHtml;
    try {
      metricsHtml = escapeHtml(JSON.stringify(metricsData, null, 2));
    } catch (e) { metricsHtml = escapeHtml(String(metricsData)); }
    html += '<div class="collapsible-body"><pre class="json-pre">' + metricsHtml + '</pre></div></div>';

    els.debugBody.innerHTML = html;
  }

  function collapsibleStart(title, count) {
    return '<div class="collapsible"><button class="collapsible-head" data-collapse>' +
      '<span>' + escapeHtml(title) + '</span>' +
      '<span class="count">' + count + '</span>' +
      '<svg class="chev" viewBox="0 0 24 24" width="14" height="14" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><path d="M6 9l6 6 6-6"/></svg>' +
      '</button>';
  }

  async function loadTraceDetail(traceId) {
    try {
      return await apiGet('/api/debug/traces/' + encodeURIComponent(traceId));
    } catch (e) {
      throw e;
    }
  }

  function toggleTraceDetail(row) {
    var traceId = row.dataset.traceId;
    var next = row.nextElementSibling;
    if (next && next.classList.contains('trace-detail')) { next.remove(); return; }
    $$('.trace-detail').forEach(function (d) { d.remove(); });

    var detailTr = document.createElement('tr');
    detailTr.className = 'trace-detail';
    var td = document.createElement('td');
    td.colSpan = 5;
    td.innerHTML = '<div class="muted">加载中…</div>';
    detailTr.appendChild(td);
    row.after(detailTr);

    loadTraceDetail(traceId).then(function (data) {
      td.innerHTML = '<pre class="json-pre">' + escapeHtml(JSON.stringify(data, null, 2)) + '</pre>';
    }).catch(function () {
      td.innerHTML = '<div class="muted">加载详情失败</div>';
    });
  }

  /* ---------------- 运行轨迹 ---------------- */
  function spanNode(span) {
    if (span && span.tags && span.tags.node) return span.tags.node;
    var op = (span && span.operation) || '';
    var i = op.indexOf(':');
    return i >= 0 ? op.slice(i + 1) : op;
  }
  function spanActor(span) {
    if (span && span.tags && span.tags.actor) return span.tags.actor;
    var op = (span && span.operation) || '';
    var i = op.indexOf(':');
    return i >= 0 ? op.slice(0, i) : op;
  }
  function spanEnded(span) {
    if (!span || !span.end_time) return false;
    var d = new Date(span.end_time);
    if (isNaN(d.getTime())) return false;
    return d.getUTCFullYear() > 1; // Go 零值时间 "0001-01-01T00:00:00Z" 视为进行中
  }
  function spanDuration(span) {
    if (!spanEnded(span)) return null;
    var s = new Date(span.start_time).getTime();
    var e = new Date(span.end_time).getTime();
    if (isNaN(s) || isNaN(e) || e < s) return null;
    return e - s;
  }
  function formatDuration(ms) {
    if (ms == null) return '运行中…';
    if (ms < 1000) return Math.round(ms) + 'ms';
    if (ms < 60000) return (ms / 1000).toFixed(1) + 's';
    return (ms / 60000).toFixed(1) + 'min';
  }

  function deriveStage(spans) {
    if (!spans || !spans.length) return '未开始';
    var running = spans.filter(function (s) { return !spanEnded(s); });
    if (!running.length) return '已完成';
    running.sort(function (a, b) { return new Date(b.start_time).getTime() - new Date(a.start_time).getTime(); });
    var node = spanNode(running[0]) || '';
    if (node === 'llm_call') return '正在思考…';
    if (node.indexOf('tool:') === 0) return '正在调用工具 ' + node.slice(5);
    if (node.indexOf('request:') === 0) return '正在处理…';
    return '运行中…';
  }

  function spanLabel(span) {
    var node = spanNode(span);
    var actor = spanActor(span) || 'lead';
    if (!node) return span.operation || '未知';
    switch (node) {
      case 'request:user_turn': return 'Lead 处理用户请求';
      case 'request:work_unit': return actor + ' 工作单元';
      case 'request:delegate': return actor + ' 委派执行';
      case 'llm_call': return actor + ' 调用大模型';
      case 'tool:delegate_task': return actor + ' 委派任务给专家';
      case 'tool:send_message': return actor + ' 发消息给队友';
      case 'tool:spawn_teammate': return actor + ' 创建队友';
      case 'tool:shutdown_teammate': return actor + ' 关闭队友';
    }
    if (node.indexOf('tool:') === 0) return actor + ' 调用工具 ' + node.slice(5);
    return span.operation || '未知';
  }

  function actorBadgeClass(actor) {
    actor = actor || '';
    if (actor === 'lead') return 'actor-lead';
    if (actor.indexOf('delegate') === 0) return 'actor-delegate';
    return 'actor-teammate';
  }

  function buildTraceNode(span, children, depth) {
    var node = document.createElement('div');
    node.className = 'trace-node';
    if (depth > 0) node.style.paddingLeft = (depth * 14) + 'px';

    var row = document.createElement('div');
    row.className = 'trace-row';

    var dot = document.createElement('span');
    dot.className = 'trace-dot ' + (span.status === 'error' ? 'err' : (spanEnded(span) ? 'ok' : 'run'));

    var actor = document.createElement('span');
    actor.className = 'trace-actor ' + actorBadgeClass(spanActor(span));
    actor.textContent = spanActor(span) || '?';

    var label = document.createElement('span');
    label.className = 'trace-label';
    label.textContent = spanLabel(span);

    var dur = document.createElement('span');
    dur.className = 'trace-dur';
    dur.textContent = formatDuration(spanDuration(span));

    var expand = document.createElement('button');
    expand.className = 'trace-expand';
    expand.textContent = '▸';
    expand.title = '展开标签';

    row.appendChild(dot);
    row.appendChild(actor);
    row.appendChild(label);
    row.appendChild(dur);
    row.appendChild(expand);
    node.appendChild(row);

    if (span.tags && Object.keys(span.tags).length) {
      var tags = document.createElement('pre');
      tags.className = 'trace-tags hidden';
      try { tags.textContent = JSON.stringify(span.tags, null, 2); } catch (e) { tags.textContent = String(span.tags); }
      node.appendChild(tags);
      row.addEventListener('click', function () {
        var hidden = tags.classList.toggle('hidden');
        expand.textContent = hidden ? '▸' : '▾';
      });
    }

    var kids = children[span.span_id] || [];
    if (kids.length) {
      var cont = document.createElement('div');
      cont.className = 'trace-children';
      kids.forEach(function (k) { cont.appendChild(buildTraceNode(k, children, depth + 1)); });
      node.appendChild(cont);
    }
    return node;
  }

  function renderTrace() {
    var spans = state.traceSpans || [];
    els.traceStage.textContent = deriveStage(spans);
    if (!spans.length) {
      els.traceBody.innerHTML = '<div class="muted pad">暂无运行轨迹。发送消息后这里会实时显示执行过程。</div>';
      return;
    }
    var byId = {};
    var roots = [];
    var children = {};
    spans.forEach(function (s) { if (s.span_id) byId[s.span_id] = s; });
    spans.forEach(function (s) {
      var pid = s.parent_id;
      if (pid && byId[pid]) (children[pid] = children[pid] || []).push(s);
      else roots.push(s);
    });
    function byStart(a, b) { return new Date(a.start_time).getTime() - new Date(b.start_time).getTime(); }
    roots.sort(byStart);
    Object.keys(children).forEach(function (k) { children[k].sort(byStart); });

    els.traceBody.innerHTML = '';
    roots.forEach(function (r) { els.traceBody.appendChild(buildTraceNode(r, children, 0)); });
  }

  async function loadTrace(rid) {
    if (!rid) return;
    try {
      var data = await apiGet('/api/traces/by-request?request_id=' + encodeURIComponent(rid));
      state.traceSpans = data.spans || [];
      renderTrace();
      if (state.streaming) els.thinkingText.textContent = deriveStage(state.traceSpans);
    } catch (e) { /* 静默失败，保留旧数据 */ }
  }
  function startTracePolling(rid) {
    stopTracePolling();
    state.lastRequestId = rid;
    state.traceSpans = [];
    renderTrace();
    loadTrace(rid);
    state.tracePolling = setInterval(function () { loadTrace(rid); }, 600);
  }
  function stopTracePolling() {
    if (state.tracePolling) {
      clearInterval(state.tracePolling);
      state.tracePolling = null;
    }
  }

  /* ---------------- 团队 ---------------- */
  async function teamPost(path, body) {
    var resp = await fetch(path, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(body)
    });
    var data = null;
    try { data = await resp.json(); } catch (e) { data = null; }
    if (!resp.ok) throw new Error((data && data.error) ? data.error : ('HTTP ' + resp.status));
    return data;
  }

  async function loadTeam() {
    var s = currentSession();
    if (!s) {
      els.teamScope.textContent = '';
      els.teamMembers.innerHTML = '<div class="muted pad">请先创建会话</div>';
      return;
    }
    try {
      var data = await apiGet('/api/team?session_id=' + encodeURIComponent(s.id));
      state.teamData = data;
      renderTeam(data);
    } catch (e) {
      els.teamMembers.innerHTML = '<div class="muted pad">加载团队失败：' + escapeHtml(e.message || e) + '</div>';
    }
  }

  function renderTeam(data) {
    data = data || {};
    var scope = data.scope || '';
    var teamName = data.team_name || '';
    els.teamScope.textContent = (scope ? scope : '') + (scope && teamName ? ' · ' : '') + (teamName ? teamName : '');

    var members = (data.members || []).slice();
    members.sort(function (a, b) {
      var al = a.role === 'lead' ? 0 : 1;
      var bl = b.role === 'lead' ? 0 : 1;
      if (al !== bl) return al - bl;
      return (a.name || '').localeCompare(b.name || '');
    });

    els.teamMembers.innerHTML = '';
    if (!members.length) {
      els.teamMembers.innerHTML = '<div class="muted pad">暂无团队成员</div>';
    } else {
      var nonLead = 0;
      members.forEach(function (m) {
        if (m.role !== 'lead') nonLead++;
        els.teamMembers.appendChild(buildMemberCard(m));
      });
      if (!nonLead) {
        var hint = document.createElement('div');
        hint.className = 'team-hint muted pad';
        hint.textContent = '当前只有 Lead，可创建专业队友。';
        els.teamMembers.appendChild(hint);
      }
    }
    fillRoleSelect(data.roles || []);
  }

  function buildMemberCard(m) {
    var card = document.createElement('div');
    card.className = 'team-member';
    card.dataset.name = m.name || '';

    var head = document.createElement('div');
    head.className = 'team-member-head';

    var name = document.createElement('span');
    name.className = 'team-member-name';
    name.textContent = m.name || '?';
    head.appendChild(name);

    if (m.role === 'lead') {
      var lead = document.createElement('span');
      lead.className = 'badge badge-lead';
      lead.textContent = 'Lead';
      head.appendChild(lead);
    }

    var role = document.createElement('span');
    role.className = 'badge badge-role';
    role.textContent = m.role || '';
    head.appendChild(role);

    var status = document.createElement('span');
    status.className = 'status-chip ' + statusClass(m.status);
    status.textContent = statusText(m.status);
    head.appendChild(status);

    var activity = document.createElement('div');
    activity.className = 'team-member-activity';
    activity.textContent = m.activity || '';

    card.appendChild(head);
    card.appendChild(activity);

    if (m.role !== 'lead') {
      var actions = document.createElement('div');
      actions.className = 'team-member-actions';
      var btn = document.createElement('button');
      btn.className = 'btn-secondary team-shutdown';
      btn.dataset.shutdown = m.name || '';
      btn.textContent = '关闭';
      actions.appendChild(btn);
      card.appendChild(actions);
    }
    return card;
  }

  function statusText(st) {
    if (st === 'working') return '工作中';
    if (st === 'idle') return '空闲';
    if (st === 'shutdown') return '已关闭';
    return st || '未知';
  }
  function statusClass(st) {
    if (st === 'working') return 'ok';
    if (st === 'shutdown') return 'error';
    return 'idle';
  }

  function fillRoleSelect(roles) {
    var current = els.teamNewRole.value;
    els.teamNewRole.innerHTML = '';
    (roles || []).forEach(function (r) {
      var opt = document.createElement('option');
      opt.value = r;
      opt.textContent = r;
      els.teamNewRole.appendChild(opt);
    });
    if (current && roles && roles.indexOf(current) !== -1) els.teamNewRole.value = current;
  }

  async function spawnTeammate() {
    var s = currentSession();
    if (!s) { toast('请先创建会话'); return; }
    var name = els.teamNewName.value.trim();
    var role = els.teamNewRole.value;
    var prompt = els.teamNewPrompt.value.trim();
    if (!name || !role) { toast('请填写名字并选择角色'); return; }
    els.teamSpawnBtn.disabled = true;
    try {
      var resp = await teamPost('/api/team/teammates', { session_id: s.id, name: name, role: role, prompt: prompt });
      if (resp && resp.error) {
        toast('创建失败：' + resp.error);
      } else {
        toast('已创建队友 ' + ((resp && resp.name) || name));
        els.teamNewName.value = '';
        els.teamNewPrompt.value = '';
        loadTeam();
      }
    } catch (e) {
      toast('创建失败：' + (e.message || e));
    } finally {
      els.teamSpawnBtn.disabled = false;
    }
  }

  async function shutdownTeammate(name) {
    var s = currentSession();
    if (!s || !name) return;
    try {
      var resp = await teamPost('/api/team/teammates/' + encodeURIComponent(name) + '/shutdown', { session_id: s.id });
      if (resp && resp.error) {
        toast('关闭失败：' + resp.error);
      } else {
        toast('已请求关闭 ' + ((resp && resp.name) || name));
        loadTeam();
      }
    } catch (e) {
      toast('关闭失败：' + (e.message || e));
    }
  }

  /* ---------------- 右侧栏 ---------------- */
  function setActiveTab(tab) {
    state.rightPanelTab = tab;
    $$('.tab').forEach(function (t) { t.classList.toggle('active', t.dataset.tab === tab); });
    els.tabApprovals.classList.toggle('hidden', tab !== 'approvals');
    els.tabDebug.classList.toggle('hidden', tab !== 'debug');
    els.tabTrace.classList.toggle('hidden', tab !== 'trace');
    els.tabTeam.classList.toggle('hidden', tab !== 'team');
  }
  function openRightPanel(tab) {
    state.rightPanelOpen = true;
    els.rightPanel.classList.remove('hidden');
    setActiveTab(tab);
    if (tab === 'approvals') {
      loadApprovals();
    } else if (tab === 'debug' && !state.debugLoaded) {
      loadDebug();
    } else if (tab === 'trace') {
      if (state.lastRequestId) loadTrace(state.lastRequestId);
      else renderTrace();
    } else if (tab === 'team') {
      loadTeam();
    }
  }
  function closeRightPanel() {
    state.rightPanelOpen = false;
    els.rightPanel.classList.add('hidden');
  }

  /* ---------------- 设置弹窗 ---------------- */
  var logUnsub = null;
  var statusUnsub = null;

  function setSettingsReadonly(ro) {
    ['setModel', 'setBaseUrl', 'setApiKey', 'setWorkspace'].forEach(function (key) {
      els[key].readOnly = ro;
    });
    els.settingsSave.disabled = ro;
    if (ro) els.selectWorkspaceBtn.classList.add('hidden');
  }

  function renderElectronInfo(info) {
    if (!info) { els.electronInfo.innerHTML = ''; return; }
    state.electronInfo = info;
    var parts = [];
    if (info.platform) parts.push('平台：' + escapeHtml(info.platform));
    if (info.version) parts.push('客户端版本：' + escapeHtml(info.version));
    if (info.serverUrl) parts.push('服务地址：' + escapeHtml(info.serverUrl));
    if (info.serverRunning != null) parts.push('服务状态：' + (info.serverRunning ? '运行中' : '未运行'));
    els.electronInfo.innerHTML = parts.join('<span style="opacity:.4">|</span>');
  }

  function setupLogs(bridge) {
    if (typeof bridge.onLog === 'function' && !logUnsub) {
      logUnsub = bridge.onLog(function (line) {
        els.logOutput.textContent += (line + '\n');
        els.logOutput.scrollTop = els.logOutput.scrollHeight;
        if (els.logOutput.textContent.length > 50000) {
          els.logOutput.textContent = els.logOutput.textContent.slice(-50000);
        }
      });
      els.logToggle.classList.remove('hidden');
    } else if (typeof bridge.onLog !== 'function') {
      els.logToggle.classList.add('hidden');
      els.logPanel.classList.add('hidden');
    }
    if (typeof bridge.onStatus === 'function' && !statusUnsub) {
      statusUnsub = bridge.onStatus(function (s) {
        var info = state.electronInfo || {};
        info.serverRunning = s && s.running;
        if (s && s.url) info.serverUrl = s.url;
        renderElectronInfo(info);
      });
    }
  }

  async function openSettings() {
    els.settingsModal.classList.remove('hidden');
    els.settingsMsg.textContent = '';
    var bridge = window.nexusDesktop;

    if (bridge) {
      els.settingsModeHint.textContent = 'Electron 模式：修改后点击「保存」，配置将写入本地并重启服务。';
      setSettingsReadonly(false);
      els.selectWorkspaceBtn.classList.remove('hidden');
      try {
        var s = await bridge.getSettings();
        els.setModel.value = s.model || '';
        els.setBaseUrl.value = s.base_url || '';
        els.setApiKey.value = s.api_key || '';
        els.setWorkspace.value = s.workspace || '';
      } catch (e) {
        els.settingsModeHint.textContent = '读取设置失败：' + e.message;
      }
      try {
        var info = await bridge.getInfo();
        renderElectronInfo(info);
      } catch (e) { /* 忽略 */ }
      setupLogs(bridge);
    } else {
      els.settingsModeHint.textContent = '在浏览器模式下请通过环境变量 NEXUS_API_KEY / NEXUS_BASE_URL / NEXUS_MODEL 配置，然后重启服务。';
      setSettingsReadonly(true);
      if (state.meta) {
        els.setModel.value = state.meta.model || '';
        els.setBaseUrl.value = '';
        els.setApiKey.value = '';
        els.setWorkspace.value = state.meta.workspace_root || '.';
      }
      els.logToggle.classList.add('hidden');
      els.logPanel.classList.add('hidden');
      els.electronInfo.innerHTML = '';
    }
  }

  function closeSettings() {
    els.settingsModal.classList.add('hidden');
  }

  async function saveSettings() {
    var bridge = window.nexusDesktop;
    if (!bridge) { toast('浏览器模式下设置只读'); return; }
    var patch = {
      model: els.setModel.value.trim(),
      base_url: els.setBaseUrl.value.trim(),
      api_key: els.setApiKey.value
    };
    els.settingsSave.disabled = true;
    els.settingsMsg.textContent = '保存中…';
    try {
      var res = await bridge.saveSettings(patch);
      if (res && res.ok) {
        els.settingsMsg.textContent = '已保存，服务重启中…';
        toast('已保存，服务重启中…');
      } else {
        els.settingsMsg.textContent = '保存失败：' + ((res && res.error) || '未知错误');
      }
    } catch (e) {
      els.settingsMsg.textContent = '保存失败：' + e.message;
    } finally {
      els.settingsSave.disabled = false;
    }
  }

  async function selectWorkspace() {
    var bridge = window.nexusDesktop;
    if (!bridge || typeof bridge.selectWorkspace !== 'function') return;
    try {
      var dir = await bridge.selectWorkspace();
      if (dir) els.setWorkspace.value = dir;
    } catch (e) { toast('选择目录失败'); }
  }

  /* ---------------- 事件绑定 ---------------- */
  function bindEvents() {
    els.sidebarToggle.addEventListener('click', function () {
      els.sidebar.classList.toggle('collapsed');
    });

    els.newSessionBtn.addEventListener('click', createSession);

    els.sessionList.addEventListener('click', function (e) {
      if (state.streaming) return;
      var delBtn = e.target.closest('.session-del');
      var item = e.target.closest('.session-item');
      if (!item) return;
      if (delBtn) {
        e.stopPropagation();
        deleteSession(item.dataset.id);
      } else {
        switchSession(item.dataset.id);
      }
    });

    els.sendBtn.addEventListener('click', sendMessage);
    els.composer.addEventListener('keydown', function (e) {
      if (e.key === 'Enter' && !e.shiftKey) {
        e.preventDefault();
        sendMessage();
      }
    });
    els.composer.addEventListener('input', autoResizeComposer);

    els.stopBtn.addEventListener('click', function () {
      if (state.streamAbort) state.streamAbort.abort();
    });

    // 工作线折叠 + scope/workstream 变更
    els.workstreamToggle.addEventListener('click', function () {
      els.workstreamFields.classList.toggle('hidden');
      els.workstreamToggle.classList.toggle('open');
    });
    function onWorkstreamChange() {
      var s = currentSession();
      if (!s) return;
      s.scope = els.scopeInput.value.trim();
      s.workstream = els.workstreamInput.value.trim();
      saveSessions();
      renderSessionList();
    }
    els.scopeInput.addEventListener('input', onWorkstreamChange);
    els.workstreamInput.addEventListener('input', onWorkstreamChange);

    // 消息区复制（委托）
    els.messages.addEventListener('click', function (e) {
      var msgCopy = e.target.closest('.msg-copy');
      if (msgCopy) {
        var msgEl = msgCopy.closest('.msg');
        var m = findMessage(msgEl.dataset.msgId);
        if (m) copyText(m.content);
        return;
      }
      var msgTrace = e.target.closest('.msg-trace');
      if (msgTrace) {
        var traceEl = msgTrace.closest('.msg');
        var tm = findMessage(traceEl.dataset.msgId);
        if (tm && tm.requestId) {
          state.lastRequestId = tm.requestId;
          state.traceSpans = [];
          loadTrace(tm.requestId);
          openRightPanel('trace');
        }
        return;
      }
      var codeCopy = e.target.closest('.code-copy');
      if (codeCopy) {
        try { copyText(decodeURIComponent(codeCopy.dataset.code || '')); } catch (err) { copyText(codeCopy.dataset.code || ''); }
      }
    });

    // 顶栏
    els.approvalBadgeBtn.addEventListener('click', function () {
      if (state.rightPanelOpen && state.rightPanelTab === 'approvals') closeRightPanel();
      else openRightPanel('approvals');
    });
    els.debugBtn.addEventListener('click', function () {
      if (state.rightPanelOpen && state.rightPanelTab === 'debug') closeRightPanel();
      else openRightPanel('debug');
    });
    els.traceBtn.addEventListener('click', function () {
      if (state.rightPanelOpen && state.rightPanelTab === 'trace') closeRightPanel();
      else openRightPanel('trace');
    });
    els.teamBtn.addEventListener('click', function () {
      if (state.rightPanelOpen && state.rightPanelTab === 'team') closeRightPanel();
      else openRightPanel('team');
    });
    els.rightPanelClose.addEventListener('click', closeRightPanel);

    $$('.tab').forEach(function (t) {
      t.addEventListener('click', function () {
        openRightPanel(t.dataset.tab);
      });
    });

    els.approvalsRefresh.addEventListener('click', loadApprovals);
    els.debugRefresh.addEventListener('click', loadDebug);
    els.traceRefresh.addEventListener('click', function () {
      if (state.lastRequestId) loadTrace(state.lastRequestId);
      else renderTrace();
    });
    els.teamRefresh.addEventListener('click', loadTeam);

    els.approvalsList.addEventListener('click', function (e) {
      var btn = e.target.closest('button[data-act]');
      if (!btn) return;
      var item = btn.closest('.approval-item');
      approvalAction(item.dataset.id, btn.dataset.act);
    });

    els.teamMembers.addEventListener('click', function (e) {
      var btn = e.target.closest('button[data-shutdown]');
      if (!btn) return;
      shutdownTeammate(btn.dataset.shutdown);
    });
    els.teamSpawnBtn.addEventListener('click', spawnTeammate);

    els.debugBody.addEventListener('click', function (e) {
      var head = e.target.closest('.collapsible-head');
      if (head) {
        var coll = head.closest('.collapsible');
        var body = coll.querySelector('.collapsible-body');
        if (body) body.classList.toggle('hidden');
        head.classList.toggle('open');
        return;
      }
      var row = e.target.closest('tr[data-trace-id]');
      if (row) toggleTraceDetail(row);
    });

    // 设置弹窗
    els.settingsBtn.addEventListener('click', openSettings);
    els.settingsSave.addEventListener('click', saveSettings);
    els.selectWorkspaceBtn.addEventListener('click', selectWorkspace);
    $$('[data-close-modal]').forEach(function (el) {
      el.addEventListener('click', function (e) {
        if (e.target === el || el.classList.contains('modal-backdrop') || el.classList.contains('icon-btn') || el.id === 'settings-cancel') {
          closeSettings();
        }
      });
    });
    els.logToggle.addEventListener('click', function () {
      els.logPanel.classList.toggle('hidden');
      els.logToggle.classList.toggle('open');
    });

    // Esc 关闭弹窗/右侧栏
    document.addEventListener('keydown', function (e) {
      if (e.key === 'Escape') {
        if (!els.settingsModal.classList.contains('hidden')) closeSettings();
        else if (state.rightPanelOpen) closeRightPanel();
      }
    });
  }

  /* ---------------- 初始化 ---------------- */
  async function init() {
    cacheEls();
    bindEvents();
    renderSessionList();

    // 加载会话（localStorage 优先）
    state.sessions = loadSessions();
    var cur = null;
    try { cur = localStorage.getItem(LS_CURRENT); } catch (e) { /* 忽略 */ }

    if (cur && getSession(cur)) {
      state.currentSessionId = cur;
      renderSessionList();
      renderMessages();
    } else if (state.sessions.length) {
      state.currentSessionId = state.sessions[0].id;
      renderSessionList();
      renderMessages();
    } else {
      await createSession();
    }

    // meta 与健康检查
    loadMeta();
    checkHealth();
    setInterval(checkHealth, 10000);
    setInterval(loadApprovals, 5000);
    loadApprovals();
    // 团队面板打开时每 8s 轮询刷新
    setInterval(function () {
      if (state.rightPanelOpen && state.rightPanelTab === 'team') loadTeam();
    }, 8000);

    // 空输入框初始状态
    autoResizeComposer();
  }

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', init);
  } else {
    init();
  }
})();
