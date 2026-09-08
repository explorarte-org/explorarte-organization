import { createServer, request as proxyRequest } from 'node:http';
import { readFile } from 'node:fs/promises';
import { fileURLToPath } from 'node:url';
import { resolve, relative, sep } from 'node:path';

const root = fileURLToPath(new URL('../', import.meta.url));
const host = process.env.WEB_HOST || '0.0.0.0';
const port = Number(process.env.WEB_PORT || 80);
const apiUpstream = new URL(process.env.WEB_API_UPSTREAM || 'http://127.0.0.1:8080');
const contentTypes = new Map([
  ['.html', 'text/html; charset=utf-8'],
  ['.js', 'text/javascript; charset=utf-8'],
  ['.css', 'text/css; charset=utf-8'],
  ['.json', 'application/json; charset=utf-8'],
  ['.png', 'image/png'],
  ['.webp', 'image/webp'],
  ['.txt', 'text/plain; charset=utf-8'],
  ['.md', 'text/plain; charset=utf-8'],
]);

function headers(type) {
  return {
    'Content-Type': type,
    'Cache-Control': 'no-store',
    'X-Content-Type-Options': 'nosniff',
    'Referrer-Policy': 'same-origin',
    'Content-Security-Policy': "default-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'; script-src 'self'; connect-src 'self'",
  };
}

function staticPath(pathname) {
  let decoded;
  try {
    decoded = decodeURIComponent(pathname);
  } catch {
    return null;
  }
  const requested = decoded === '/' ? 'mission-demo.html' : decoded.replace(/^\/+/, '');
  const file = resolve(root, requested);
  const fromRoot = relative(root, file);
  if (fromRoot.startsWith(`..${sep}`) || fromRoot === '..' || fromRoot.includes(`..${sep}`)) return null;
  return file;
}

function proxyAPI(req, res) {
  const upstream = proxyRequest({
    hostname: apiUpstream.hostname,
    port: apiUpstream.port,
    path: req.url,
    method: req.method,
    headers: { ...req.headers, host: apiUpstream.host, connection: 'close' },
  }, upstreamResponse => {
    res.writeHead(upstreamResponse.statusCode || 502, upstreamResponse.headers);
    upstreamResponse.pipe(res);
  });
  upstream.on('error', error => {
    if (res.headersSent) return res.destroy(error);
    res.writeHead(503, headers('application/json; charset=utf-8'));
    res.end(JSON.stringify({ error: 'No se pudo conectar con el backend organizacional.' }));
  });
  req.pipe(upstream);
}

const server = createServer(async (req, res) => {
  const pathname = new URL(req.url || '/', `http://${req.headers.host || 'localhost'}`).pathname;
  if (pathname.startsWith('/api/')) {
    proxyAPI(req, res);
    return;
  }
  if (!['GET', 'HEAD'].includes(req.method || '')) {
    res.writeHead(405, { Allow: 'GET, HEAD', ...headers('text/plain; charset=utf-8') });
    res.end(req.method === 'HEAD' ? undefined : 'Method Not Allowed');
    return;
  }
  const file = staticPath(pathname);
  if (!file) {
    res.writeHead(403, headers('text/plain; charset=utf-8'));
    res.end(req.method === 'HEAD' ? undefined : 'Forbidden');
    return;
  }
  try {
    const body = await readFile(file);
    const extension = file.slice(file.lastIndexOf('.')).toLowerCase();
    res.writeHead(200, headers(contentTypes.get(extension) || 'application/octet-stream'));
    res.end(req.method === 'HEAD' ? undefined : body);
  } catch {
    res.writeHead(404, headers('text/plain; charset=utf-8'));
    res.end(req.method === 'HEAD' ? undefined : 'Not Found');
  }
});

server.listen(port, host, () => {
  console.log(`Explorarte web: http://${host}:${port} · API ${apiUpstream.origin}`);
});
