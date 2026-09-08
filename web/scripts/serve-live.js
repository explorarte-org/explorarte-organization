import { spawn } from 'node:child_process';
import { createConnection } from 'node:net';
import { homedir } from 'node:os';
import { join } from 'node:path';

const uiPort = Number(process.env.PORT || 4185);
const tunnelPort = Number(process.env.DEMO_TUNNEL_PORT || 18080);
const sshHost = process.env.DEMO_SSH_HOST || 'explorarte-nuevo';
const sshConfig = process.env.DEMO_SSH_CONFIG || join(homedir(), '.ssh', 'config');

function portOpen(port) {
  return new Promise(resolve => {
    const socket = createConnection({ host: '127.0.0.1', port });
    socket.once('connect', () => { socket.destroy(); resolve(true); });
    socket.once('error', () => { socket.destroy(); resolve(false); });
  });
}

async function waitForPort(port, attempts = 40) {
  for (let attempt = 0; attempt < attempts; attempt += 1) {
    if (await portOpen(port)) return true;
    await new Promise(resolve => setTimeout(resolve, 250));
  }
  return false;
}

let tunnel;
let server;
let ownsTunnel = false;
let shuttingDown = false;
async function stop() {
  if (shuttingDown) return;
  shuttingDown = true;
  server?.kill('SIGTERM');
  if (ownsTunnel) tunnel?.kill('SIGTERM');
}
process.once('SIGINT', stop);
process.once('SIGTERM', stop);

if (await portOpen(tunnelPort)) {
  console.log(`Reutilizando túnel existente en 127.0.0.1:${tunnelPort}`);
} else {
  tunnel = spawn('ssh', [
    '-F', sshConfig,
    '-o', 'BatchMode=yes',
    '-o', 'ExitOnForwardFailure=yes',
    '-N', '-L', `${tunnelPort}:127.0.0.1:8080`, sshHost,
  ], { stdio: 'inherit' });
  ownsTunnel = true;
  tunnel.once('exit', code => {
    if (!shuttingDown) {
      console.error(`El túnel SSH terminó antes de tiempo (código ${code ?? 'desconocido'}).`);
      void stop();
    }
  });
  if (!await waitForPort(tunnelPort)) {
    console.error(`No se pudo abrir el túnel SSH a ${sshHost}.`);
    await stop();
    process.exitCode = 1;
    process.exit();
  }
}

server = spawn(process.execPath, ['scripts/serve-demo.js'], {
  env: { ...process.env, PORT: String(uiPort), DEMO_API_UPSTREAM: `http://127.0.0.1:${tunnelPort}` },
  stdio: 'inherit',
});
server.once('exit', () => { if (!shuttingDown) void stop(); });
console.log(`Demo conectada al VPS: http://127.0.0.1:${uiPort}/?source=vps`);
