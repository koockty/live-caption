import {app, BrowserWindow, ipcMain, screen, shell, Menu} from 'electron';
import fs from 'node:fs';
import path from 'node:path';
import {fileURLToPath} from 'node:url';
import {Backend, backendURL} from './backend.js';
import {normalizeSettings, fitBounds} from './settings.js';

const here = path.dirname(fileURLToPath(import.meta.url));
const base = backendURL(process.env.CAPTION_BACKEND_URL);
if (process.env.CAPTION_DESKTOP_DATA_DIR) app.setPath('userData', path.resolve(process.env.CAPTION_DESKTOP_DATA_DIR));
app.setName('Live Captions');
if (!app.requestSingleInstanceLock()) app.quit();
else {
  let win, backend, store, timer, latest = {type: 'connection', message: '正在连接 Go 后端…'};
  const settingsPath = () => path.join(app.getPath('userData'), 'captions-settings.json');
  const persist = () => {clearTimeout(timer); timer = setTimeout(() => {
    try {fs.mkdirSync(app.getPath('userData'), {recursive: true}); const target = settingsPath(); fs.writeFileSync(target + '.tmp', JSON.stringify(store), {mode: 0o600}); fs.renameSync(target + '.tmp', target);} catch {console.error('无法保存字幕显示设置');}
  }, 250);};
  const trusted = (event) => win && event.sender === win.webContents && event.senderFrame === win.webContents.mainFrame;
  app.on('second-instance', () => {if (win) {win.show(); win.focus();}});
  app.whenReady().then(async () => {
    let saved; try {saved = JSON.parse(fs.readFileSync(settingsPath(), 'utf8'));} catch {saved = {};}
    store = {...normalizeSettings(saved), bounds: saved.bounds};
    const display = saved.bounds && Number.isFinite(saved.bounds.x) && Number.isFinite(saved.bounds.y) ? screen.getDisplayNearestPoint({x: Math.round(saved.bounds.x), y: Math.round(saved.bounds.y)}) : screen.getPrimaryDisplay();
    win = new BrowserWindow({...fitBounds(saved.bounds, display.workArea), minWidth: 480, minHeight: 180, title: 'Live Captions 悬浮字幕', frame: false, transparent: true, backgroundColor: '#00000000', alwaysOnTop: true, show: false, resizable: true, acceptFirstMouse: true, webPreferences: {preload: path.join(here, 'preload.cjs'), contextIsolation: true, nodeIntegration: false, sandbox: true}});
    win.setAlwaysOnTop(true, 'floating');
    if (process.platform === 'darwin') win.setVisibleOnAllWorkspaces(true, {visibleOnFullScreen: true});
    win.webContents.setWindowOpenHandler(() => ({action: 'deny'}));
    win.webContents.on('will-navigate', (event) => event.preventDefault());
    win.on('move', () => {store.bounds = win.getBounds(); persist();});
    win.on('resize', () => {store.bounds = win.getBounds(); persist();});
    ipcMain.handle('captions:init', (event) => trusted(event) ? {settings: normalizeSettings(store), state: latest} : null);
    ipcMain.handle('captions:settings', (event, patch) => {
      if (!trusted(event) || !patch || typeof patch !== 'object') return null;
      store = {...store, ...normalizeSettings({...normalizeSettings(store), ...patch})}; persist(); return normalizeSettings(store);
    });
    ipcMain.handle('captions:open', (event) => {if (trusted(event)) void shell.openExternal(base);});
    ipcMain.handle('captions:close', (event) => {if (trusted(event)) win.close();});
    Menu.setApplicationMenu(Menu.buildFromTemplate([{label: 'Live Captions', submenu: [{label: '显示字幕窗', click: () => win.show()}, {label: '打开采集页', click: () => shell.openExternal(base)}, {type: 'separator'}, {role: 'quit', label: '退出'}]}]));
    backend = new Backend(base);
    backend.on('update', (message) => {latest = message; if (!win.isDestroyed()) win.webContents.send('captions:state', message);});
    await win.loadFile(path.join(here, 'overlay.html'));
    win.show(); backend.poll();
    console.log('悬浮字幕窗已启动；置顶：' + win.isAlwaysOnTop());
  }).catch(() => {console.error('桌面字幕窗启动失败'); app.quit();});
  app.on('window-all-closed', () => {backend?.stop(); app.quit();});
  app.on('before-quit', () => {
    backend?.stop(); clearTimeout(timer);
    if (store) {try {fs.mkdirSync(app.getPath('userData'), {recursive: true}); fs.writeFileSync(settingsPath(), JSON.stringify(store), {mode: 0o600});} catch {}}
  });
}
