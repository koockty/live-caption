const {contextBridge, ipcRenderer} = require('electron');
contextBridge.exposeInMainWorld('captions', {
  init: () => ipcRenderer.invoke('captions:init'),
  setSettings: (value) => ipcRenderer.invoke('captions:settings', value),
  openCapture: () => ipcRenderer.invoke('captions:open'),
  close: () => ipcRenderer.invoke('captions:close'),
  onState: (callback) => {const handler = (_event, value) => callback(value); ipcRenderer.on('captions:state', handler); return () => ipcRenderer.removeListener('captions:state', handler);},
});
