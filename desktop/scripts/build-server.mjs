#!/usr/bin/env node
'use strict';

/**
 * 交叉编译 nexus-server 到 desktop/bin/，供 electron-builder 打包进
 * extraResources（resources/server/）。
 *
 * 用法：
 *   node scripts/build-server.mjs                # 当前平台 + 当前架构
 *   node scripts/build-server.mjs --all          # 当前平台 amd64 + arm64
 *   node scripts/build-server.mjs --goos windows --goarch amd64
 *
 * 输出命名：nexus-server-<win|darwin|linux>-<x64|arm64>[.exe]
 */

import { execFileSync } from 'node:child_process';
import { mkdirSync, existsSync } from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const desktopDir = path.join(__dirname, '..');
const binDir = path.join(desktopDir, 'bin');
const repoRoot = path.join(desktopDir, '..');

function nativeGoos() {
  switch (process.platform) {
    case 'win32': return 'windows';
    case 'darwin': return 'darwin';
    default: return 'linux';
  }
}

function nativeGoarch() {
  return process.arch === 'arm64' ? 'arm64' : 'amd64';
}

function findGo() {
  const candidates = [
    'go',
    process.env.GOROOT ? path.join(process.env.GOROOT, 'bin', process.platform === 'win32' ? 'go.exe' : 'go') : null,
    'C:\\tools\\go\\bin\\go.exe',
    '/usr/local/go/bin/go',
    '/usr/bin/go',
    path.join(os.homedir(), 'go', 'bin', process.platform === 'win32' ? 'go.exe' : 'go'),
  ].filter(Boolean);
  for (const c of candidates) {
    try {
      execFileSync(c, ['version'], { stdio: 'ignore' });
      return c;
    } catch (_) { /* 尝试下一个 */ }
  }
  return null;
}

function platformKey(goos) {
  return goos === 'windows' ? 'win' : goos === 'darwin' ? 'darwin' : 'linux';
}

function binaryName(goos, goarch) {
  const arch = goarch === 'amd64' ? 'x64' : (goarch === 'arm64' ? 'arm64' : goarch);
  const name = `nexus-server-${platformKey(goos)}-${arch}`;
  return goos === 'windows' ? `${name}.exe` : name;
}

function build(go, goos, goarch) {
  const out = path.join(binDir, binaryName(goos, goarch));
  mkdirSync(binDir, { recursive: true });
  console.log(`[build-server] ${goos}/${goarch} -> ${path.relative(repoRoot, out)}`);
  // 将 Go 构建缓存固定到仓库内（已 gitignore），避免在受限环境（只读
  // $HOME）或 CI 沙箱里写默认缓存目录失败。
  const goCache = process.env.GOCACHE || path.join(repoRoot, '.gocache');
  execFileSync(go, ['build', '-trimpath', '-o', out, './cmd/nexus'], {
    cwd: repoRoot,
    stdio: 'inherit',
    env: {
      ...process.env,
      GOOS: goos,
      GOARCH: goarch,
      CGO_ENABLED: '0',
      GOCACHE: goCache,
    },
  });
  if (!existsSync(out)) {
    throw new Error(`构建产物缺失: ${out}`);
  }
  console.log(`[build-server] 完成 ${out}`);
}

function main() {
  const go = findGo();
  if (!go) {
    console.error('[build-server] 未找到 Go 工具链，请安装 Go 1.22+ 或设置 GOROOT。');
    process.exit(1);
  }

  const args = process.argv.slice(2);
  const flag = (name) => {
    const i = args.indexOf('--' + name);
    return i >= 0 && args[i + 1] ? args[i + 1] : null;
  };

  const targets = [];
  if (args.includes('--all')) {
    targets.push({ goos: nativeGoos(), goarch: 'amd64' });
    targets.push({ goos: nativeGoos(), goarch: 'arm64' });
  } else if (flag('goos') || flag('goarch')) {
    targets.push({
      goos: flag('goos') || nativeGoos(),
      goarch: flag('goarch') || nativeGoarch(),
    });
  } else {
    targets.push({ goos: nativeGoos(), goarch: nativeGoarch() });
  }

  // 去重（--all 时可能重复）。
  const seen = new Set();
  for (const t of targets) {
    const key = `${t.goos}/${t.goarch}`;
    if (seen.has(key)) continue;
    seen.add(key);
    build(go, t.goos, t.goarch);
  }
  console.log(`[build-server] 全部完成（${seen.size} 个产物）。`);
}

main();
