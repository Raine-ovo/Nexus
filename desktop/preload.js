'use strict';

const { contextBridge, ipcRenderer } = require('electron');

/**
 * 向渲染层暴露的桌面能力。Web UI 通过 window.nexusDesktop 特性检测：
 * 存在则启用设置编辑与工作目录选择，否则退化为纯浏览器模式。
 */
contextBridge.exposeInMainWorld('nexusDesktop', {
  getSettings: () => ipcRenderer.invoke('settings:get'),
  saveSettings: (patch) => ipcRenderer.invoke('settings:save', patch),
  selectWorkspace: () => ipcRenderer.invoke('settings:selectWorkspace'),
  getInfo: () => ipcRenderer.invoke('app:info'),
  openExternal: (url) => ipcRenderer.invoke('app:openExternal', url),

  onLog: (cb) => {
    const handler = (_event, line) => cb(line);
    ipcRenderer.on('server:log', handler);
    return () => ipcRenderer.removeListener('server:log', handler);
  },
  onStatus: (cb) => {
    const handler = (_event, status) => cb(status);
    ipcRenderer.on('server:status', handler);
    return () => ipcRenderer.removeListener('server:status', handler);
  },
});
