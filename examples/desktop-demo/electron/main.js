// Minimal Electron main process: open one empty window. This is all the app
// does — the point of the demo is the *release* (shiprig versions package.json
// and builds the installer), not the app itself.
const { app, BrowserWindow } = require('electron')

function createWindow() {
  const win = new BrowserWindow({
    width: 480,
    height: 320,
    title: 'Shiprig Electron Demo',
  })
  win.loadFile('index.html')
}

app.whenReady().then(() => {
  createWindow()
  app.on('activate', () => {
    if (BrowserWindow.getAllWindows().length === 0) createWindow()
  })
})

app.on('window-all-closed', () => {
  if (process.platform !== 'darwin') app.quit()
})
