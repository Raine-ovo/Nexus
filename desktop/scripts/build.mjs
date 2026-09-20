#!/usr/bin/env node
'use strict';

/**
 * 一键打包当前平台的桌面安装包：
 *   1. 交叉编译 nexus-server 到 desktop/bin/
 *   2. 调用 electron-builder 产出安装包到 desktop/out/
 *
 * 用法：
 *   node scripts/build.mjs              # 当前平台
 *   node scripts/build.mjs --win|--mac|--linux  # 显式指定（需对应平台上运行）
 */

import { spawnSync } from 'node:child_process';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const desktopDir = path.join(__dirname, '..');

function run(cmd, args, cwd) {
  console.log(`\n> ${cmd} ${args.join(' ')}`);
  const r = spawnSync(cmd, args, { stdio: 'inherit', cwd });
  if (r.status !== 0) process.exit(r.status ?? 1);
}

function main() {
  const platform = process.platform;
  const electronBuilder = path.join(
    desktopDir, 'node_modules', '.bin',
    platform === 'win32' ? 'electron-builder.cmd' : 'electron-builder',
  );

  // 1) 构建服务端二进制（macOS 同时产出 amd64 + arm64，打包 universal 资源）。
  const serverScript = path.join(__dirname, 'build-server.mjs');
  const serverExtra = platform === 'darwin' ? ['--all'] : [];
  run(process.execPath, [serverScript, ...serverExtra], desktopDir);

  // 2) 打包安装包。
  const targetFlag = platform === 'win32' ? '--win' : platform === 'darwin' ? '--mac' : '--linux';
  run(electronBuilder, [targetFlag, '--publish', 'never'], desktopDir);

  console.log('\n[build] 打包完成，产物位于 desktop/out/。');
}

main();
