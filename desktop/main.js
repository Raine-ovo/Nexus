'use strict';

/**
 * Nexus 桌面壳主进程。
 *
 * 职责：
 *   1. 解析打包的 nexus-server 二进制（按平台/架构）；
 *   2. 读取/持久化用户设置（模型、Base URL、API Key、工作目录、端口）；
 *   3. 生成 config.yaml 并拉起 nexus-server 子进程（cwd=工作目录）；
 *   4. 轮询 /api/health，就绪后打开 BrowserWindow 指向本地服务；
 *   5. 通过 IPC 向渲染层暴露设置、日志、状态、工作目录选择等能力。
 */

const { app, BrowserWindow, ipcMain, dialog, shell, nativeTheme } = require('electron');
const { spawn } = require('child_process');
const fs = require('fs');
const path = require('path');
const net = require('net');
const os = require('os');

const APP_VERSION = app.getVersion() || '0.1.0';

const DEFAULT_SETTINGS = {
  model: 'glm-5.1',
  base_url: 'https://open.bigmodel.cn/api/paas/v4/',
  api_key: '',
  workspace: '',
  port: 17800,
};

let mainWindow = null;
let serverProc = null;
let serverUrl = '';
let serverRunning = false;
let settings = { ...DEFAULT_SETTINGS };

// ---------------------------------------------------------------------------
// 工具函数
// ---------------------------------------------------------------------------

function settingsPath() {
  return path.join(app.getPath('userData'), 'settings.json');
}

function configPath() {
  return path.join(app.getPath('userData'), 'nexus-config.yaml');
}

function loadSettings() {
  try {
    const raw = fs.readFileSync(settingsPath(), 'utf8');
    const parsed = JSON.parse(raw);
    settings = { ...DEFAULT_SETTINGS, ...parsed };
  } catch (_) {
    settings = { ...DEFAULT_SETTINGS };
  }
  if (!settings.workspace || !settings.workspace.trim()) {
    settings.workspace = path.join(app.getPath('home'), 'Nexus');
  }
  try {
    fs.mkdirSync(settings.workspace, { recursive: true });
  } catch (_) {
    // 目录创建失败不阻塞启动；服务端会因 cwd 无效而报错并显示在日志里。
  }
  return settings;
}

function saveSettings(patch) {
  settings = { ...settings, ...(patch || {}) };
  if (!settings.workspace || !settings.workspace.trim()) {
    settings.workspace = path.join(app.getPath('home'), 'Nexus');
  }
  fs.mkdirSync(settings.workspace, { recursive: true });
  fs.writeFileSync(settingsPath(), JSON.stringify(settings, null, 2), 'utf8');
  return settings;
}

function serverBinaryName() {
  const plat = process.platform === 'win32' ? 'win'
    : process.platform === 'darwin' ? 'darwin' : 'linux';
  const arch = process.arch === 'arm64' ? 'arm64' : 'x64';
  const name = `nexus-server-${plat}-${arch}`;
  return process.platform === 'win32' ? `${name}.exe` : name;
}

function resolveServerBinary() {
  const name = serverBinaryName();
  const candidates = [];
  if (app.isPackaged) {
    candidates.push(path.join(process.resourcesPath, 'server', name));
  }
  candidates.push(path.join(__dirname, 'bin', name));
  candidates.push(path.join(__dirname, '..', 'bin', name));
  for (const c of candidates) {
    if (c && fs.existsSync(c)) return c;
  }
  // 最后兜底：依赖 PATH 上的 nexus-server。
  return 'nexus-server';
}

function yamlString(value) {
  // JSON 双引号字符串是合法的 YAML 标量，可安全承载任意字符。
  return JSON.stringify(String(value ?? ''));
}

function buildConfigYaml(s) {
  const addr = `127.0.0.1:${s.port}`;
  return [
    `# 由 Nexus Desktop 自动生成，请勿手改；改动在应用「设置」里进行。`,
    `server:`,
    `  http_addr: ${yamlString(addr)}`,
    `  ws_addr: ${yamlString(addr)}   # 与 http 相同 → 关闭独立 WS 监听`,
    `  read_timeout: 10m`,
    `  write_timeout: 10m`,
    ``,
    `model:`,
    `  provider: "openai"`,
    `  model_name: ${yamlString(s.model || 'glm-5.1')}`,
    `  api_key: ${yamlString(s.api_key || '')}`,
    `  base_url: ${yamlString(s.base_url || '')}`,
    `  max_tokens: 8192`,
    `  temperature: 0.7`,
    `  max_concurrency: 1`,
    `  min_request_interval_ms: 2500`,
    ``,
    `agent:`,
    `  max_iterations: 20`,
    `  token_threshold: 100000`,
    `  compact_target_ratio: 0.6`,
    `  micro_compact_size: 51200`,
    `  output_persist_dir: ".outputs"`,
    ``,
    `rag:`,
    `  chunk_size: 512`,
    `  chunk_overlap: 64`,
    `  embedding_dim: 1536`,
    `  top_k: 5`,
    `  rerank_top_k: 3`,
    `  knowledge_dir: ".knowledge"`,
    `  embedding:`,
    `    provider: ""   # 桌面端默认哈希嵌入，离线可用`,
    `  vector_backend: "memory"`,
    `  keyword_backend: "memory"`,
    ``,
    `memory:`,
    `  conversation_window: 20`,
    `  max_semantic_entries: 500`,
    `  semantic_file: ".memory/semantic.yaml"`,
    `  compaction_threshold: 80000`,
    ``,
    `planning:`,
    `  task_dir: ".tasks"`,
    `  max_background_slots: 3`,
    ``,
    `team:`,
    `  dir: ".team"`,
    ``,
    `run:`,
    `  sandbox_dir: ""`,
    ``,
    `gateway:`,
    `  lanes:`,
    `    main:`,
    `      max_concurrency: 1`,
    `    cron:`,
    `      max_concurrency: 1`,
    `    background:`,
    `      max_concurrency: 3`,
    `  auth:`,
    `    api_keys: []`,
    `    readonly_keys: []`,
    `    jwt_secret: ""`,
    `  rate_limit:`,
    `    enabled: true`,
    `    rps: 20`,
    `    burst: 40`,
    `    trusted_proxies: []`,
    `  jobs:`,
    `    dir: ".jobs"`,
    `    max_jobs: 1000`,
    `    ttl: 24h`,
    ``,
    `mcp:`,
    `  server_enabled: true`,
    `  rpc_path: "/mcp/rpc"`,
    `  sse_path: "/mcp/sse"`,
    `  clients: []`,
    ``,
    `permission:`,
    `  mode: "semi_auto"`,
    `  workspace_root: "."`,
    `  dangerous_patterns:`,
    `    - "rm -rf /"`,
    `    - "sudo"`,
    `    - "chmod 777"`,
    `    - "> /dev/sda"`,
    `    - "mkfs"`,
    ``,
    `reflection:`,
    `  enabled: true`,
    `  max_attempts: 3`,
    `  threshold: 0.7`,
    `  enable_prospect: true`,
    `  memory_file: ".memory/reflections.yaml"`,
    `  max_mem_entries: 200`,
    ``,
    `approval:`,
    `  enabled: true`,
    `  ttl: 10m`,
    ``,
    `observability:`,
    `  trace_enabled: true`,
    `  metrics_enabled: true`,
    `  log_level: "info"`,
    ``,
  ].join('\n');
}

function findFreePort(start) {
  return new Promise((resolve) => {
    const srv = net.createServer();
    srv.once('error', () => resolve(start + 1 + Math.floor(Math.random() * 2000)));
    srv.once('listening', () => {
      const port = srv.address().port;
      srv.close(() => resolve(port));
    });
    srv.listen(start, '127.0.0.1');
  });
}

function pushLog(line) {
  if (mainWindow && !mainWindow.isDestroyed()) {
    mainWindow.webContents.send('server:log', String(line));
  }
}

function pushStatus(running, url) {
  serverRunning = running;
  serverUrl = url;
  if (mainWindow && !mainWindow.isDestroyed()) {
    mainWindow.webContents.send('server:status', { running, url });
  }
}

// ---------------------------------------------------------------------------
// 服务生命周期
// ---------------------------------------------------------------------------

function killServer() {
  if (serverProc) {
    const p = serverProc;
    serverProc = null;
    try {
      if (process.platform === 'win32') {
        // 结束整个进程树，避免子进程残留。
        spawn('taskkill', ['/pid', String(p.pid), '/T', '/F'], { stdio: 'ignore' });
      } else {
        p.kill('SIGTERM');
      }
    } catch (_) {
      /* ignore */
    }
  }
  pushStatus(false, '');
}

function httpGet(url, timeoutMs) {
  return new Promise((resolve) => {
    const lib = url.startsWith('https') ? require('https') : require('http');
    const req = lib.get(url, { timeout: timeoutMs }, (res) => {
      res.resume();
      resolve(res.statusCode);
    });
    req.on('timeout', () => { req.destroy(); resolve(0); });
    req.on('error', () => resolve(0));
  });
}

function waitForHealth(url, timeoutMs) {
  const deadline = Date.now() + timeoutMs;
  return new Promise((resolve) => {
    const tick = async () => {
      if (!serverProc) return resolve(false);
      if (await httpGet(url + '/api/health', 1500) === 200) return resolve(true);
      if (Date.now() > deadline) return resolve(false);
      setTimeout(tick, 400);
    };
    tick();
  });
}

async function startServer() {
  killServer();

  const binary = resolveServerBinary();
  pushLog(`[Nexus] 启动服务: ${binary}`);
  if (binary !== 'nexus-server' && !fs.existsSync(binary)) {
    pushLog('[Nexus] 未找到服务端二进制，请先运行 scripts/build-server.mjs。');
    pushStatus(false, '');
    return false;
  }

  if (process.platform !== 'win32') {
    try { fs.chmodSync(binary, 0o755); } catch (_) { /* ignore */ }
  }

  settings.port = await findFreePort(settings.port || 17800);
  const configYaml = buildConfigYaml(settings);
  fs.writeFileSync(configPath(), configYaml, 'utf8');

  const cwd = settings.workspace || process.cwd();
  let child;
  try {
    child = spawn(binary, ['-config', configPath()], {
      cwd,
      env: { ...process.env },
      stdio: ['ignore', 'pipe', 'pipe'],
      windowsHide: true,
    });
  } catch (err) {
    pushLog(`[Nexus] 启动失败: ${err.message}`);
    pushStatus(false, '');
    return false;
  }

  serverProc = child;
  const url = `http://127.0.0.1:${settings.port}`;

  const forward = (stream) => {
    stream.on('data', (chunk) => {
      String(chunk).split(/\r?\n/).filter((l) => l.trim()).forEach(pushLog);
    });
  };
  if (child.stdout) forward(child.stdout);
  if (child.stderr) forward(child.stderr);

  child.on('exit', (code) => {
    if (serverProc === child) {
      pushLog(`[Nexus] 服务退出 (code=${code})`);
      serverProc = null;
      pushStatus(false, '');
    }
  });
  child.on('error', (err) => {
    pushLog(`[Nexus] 服务错误: ${err.message}`);
  });

  pushStatus(false, url);
  const ok = await waitForHealth(url, 30000);
  if (ok) {
    pushLog(`[Nexus] 服务就绪: ${url}`);
    pushStatus(true, url);
    serverUrl = url;
    return true;
  }

  pushLog('[Nexus] 服务启动超时，请查看上方日志。');
  pushStatus(false, '');
  return false;
}

// ---------------------------------------------------------------------------
// 窗口
// ---------------------------------------------------------------------------

function createWindow(url) {
  nativeTheme.themeSource = 'dark';
  mainWindow = new BrowserWindow({
    width: 1280,
    height: 820,
    minWidth: 900,
    minHeight: 620,
    title: 'Nexus',
    backgroundColor: '#0b0e14',
    show: false,
    webPreferences: {
      preload: path.join(__dirname, 'preload.js'),
      contextIsolation: true,
      nodeIntegration: false,
      sandbox: false,
    },
  });

  mainWindow.once('ready-to-show', () => mainWindow.show());
  mainWindow.on('closed', () => { mainWindow = null; });

  // 拦截新窗口，改为系统浏览器打开（用于 markdown 里的 http/https 链接）。
  mainWindow.webContents.setWindowOpenHandler(({ url }) => {
    if (/^https?:\/\//i.test(url)) shell.openExternal(url);
    return { action: 'deny' };
  });

  mainWindow.loadURL(url);
  return mainWindow;
}

// ---------------------------------------------------------------------------
// IPC
// ---------------------------------------------------------------------------

function registerIpc() {
  ipcMain.handle('settings:get', () => settings);
  ipcMain.handle('settings:save', async (_e, patch) => {
    saveSettings(patch);
    await startServer();
    if (mainWindow && !mainWindow.isDestroyed()) {
      mainWindow.loadURL(serverUrl || `http://127.0.0.1:${settings.port}/`);
    }
    return { ok: serverRunning, error: serverRunning ? undefined : '服务重启失败，请查看日志' };
  });
  ipcMain.handle('settings:selectWorkspace', async () => {
    const result = await dialog.showOpenDialog(mainWindow, {
      title: '选择工作目录',
      properties: ['openDirectory', 'createDirectory'],
      defaultPath: settings.workspace,
    });
    if (result.canceled || !result.filePaths.length) return null;
    const dir = result.filePaths[0];
    saveSettings({ workspace: dir });
    return dir;
  });
  ipcMain.handle('app:info', () => ({
    platform: process.platform,
    version: APP_VERSION,
    serverUrl,
    serverRunning,
  }));
  ipcMain.handle('app:openExternal', (_e, url) => {
    if (/^https?:\/\//i.test(String(url))) shell.openExternal(String(url));
  });
}

// ---------------------------------------------------------------------------
// 启动
// ---------------------------------------------------------------------------

const gotLock = app.requestSingleInstanceLock();
if (!gotLock) {
  app.quit();
} else {
  app.on('second-instance', () => {
    if (mainWindow) {
      if (mainWindow.isMinimized()) mainWindow.restore();
      mainWindow.focus();
    }
  });

  app.whenReady().then(async () => {
    loadSettings();
    registerIpc();

    const ok = await startServer();
    const url = ok ? serverUrl : `http://127.0.0.1:${settings.port}/`;
    createWindow(url);

    app.on('activate', () => {
      if (BrowserWindow.getAllWindows().length === 0) createWindow(serverUrl || url);
    });
  });

  app.on('window-all-closed', () => {
    killServer();
    if (process.platform !== 'darwin') app.quit();
  });

  app.on('before-quit', () => {
    killServer();
  });
}
