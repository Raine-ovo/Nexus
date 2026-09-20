# Nexus Desktop

Nexus 的桌面客户端（Electron 壳 + 内置 `nexus-server` + 内嵌 Web UI）。

## 原理

1. 主进程（`main.js`）按平台/架构找到 `nexus-server` 二进制，读取用户设置，生成 `nexus-config.yaml`，并把服务作为子进程拉起（工作目录 = 用户选择的工作目录）。
2. 轮询 `/api/health` 就绪后，打开窗口指向 `http://127.0.0.1:<port>/`——Web UI 实际由 Go 二进制内置提供。
3. 渲染层通过 `preload.js` 暴露的 `window.nexusDesktop` 读写设置、选择工作目录、订阅服务日志与状态。

## 目录

```
desktop/
├── main.js            # 主进程：服务生命周期 + 窗口 + IPC
├── preload.js         # contextBridge：向 Web UI 暴露桌面能力
├── package.json       # electron + electron-builder 配置
├── scripts/
│   ├── build-server.mjs  # 交叉编译 nexus-server 到 bin/
│   └── build.mjs         # 一键打包当前平台安装包
├── bin/               # 构建产物（服务端二进制，git 忽略）
└── out/               # 安装包产物（git 忽略）
```

## 开发

```bash
cd desktop
npm install
npm run build:server   # 编译当前平台的 nexus-server 到 bin/
npm start              # 启动 Electron（开发模式，不打包）
```

> 本地 Go 若不在 PATH，脚本会自动探测 `C:\tools\go\bin\go.exe`、`/usr/local/go/bin/go` 等常见位置；也可用 `GOROOT` 指定。

## 打包

```bash
cd desktop
npm install
npm run dist           # 当前平台（Windows→NSIS、macOS→DMG/ZIP、Linux→AppImage/deb）
```

或显式指定：

```bash
npm run dist:win
npm run dist:mac
npm run dist:linux
```

产物在 `desktop/out/`。

## 首次使用

首次启动后在「设置」里填入模型 API Key（与 Base URL、模型名），保存后服务自动重启生效。默认示例为智谱 GLM（`glm-5.1` + `https://open.bigmodel.cn/api/paas/v4/`），可换成任意 OpenAI 兼容接口。

## 注意

- 服务默认监听 loopback（`127.0.0.1`），无鉴权，仅供本机使用。
- 工作目录（`workspace`）是 agent 读写文件的根目录；`.team/`、`.tasks/`、`.memory/`、`.outputs/` 等运行时产物都会落在该目录内。
