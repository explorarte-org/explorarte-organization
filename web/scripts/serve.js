import { createServer, request as httpRequest } from 'node:http';
import { readFile, stat } from 'node:fs/promises';
import { fileURLToPath } from 'node:url';
import { resolve, extname, sep } from 'node:path';

const base = fileURLToPath(new URL('../', import.meta.url));
const root = process.argv.includes('--dist') ? resolve(base, 'dist') : resolve(base);
const port = Number(process.env.PORT || 4173);
const upstreamUrl = new URL(process.env.ORG_API_UPSTREAM || 'http://127.0.0.1:8080');
const types = { '.html': 'text/html; charset=utf-8', '.js': 'text/javascript; charset=utf-8', '.css': 'text/css; charset=utf-8', '.svg': 'image/svg+xml', '.json': 'application/json; charset=utf-8' };

createServer(async (req, res) => {
  res.setHeader('X-Content-Type-Options', 'nosniff');
  res.setHeader('Referrer-Policy', 'same-origin');
  res.setHeader('Cache-Control', 'no-store');
  try {
    const pathname = decodeURIComponent(new URL(req.url, 'http://localhost').pathname);
    if (pathname.startsWith('/api/')) {
      const headers = { ...req.headers, host: upstreamUrl.host };
      const proxyReq = httpRequest({
        hostname: upstreamUrl.hostname,
        port: upstreamUrl.port,
        path: req.url,
        method: req.method,
        headers,
      }, (proxyRes) => {
        res.writeHead(proxyRes.statusCode, proxyRes.headers);
        proxyRes.pipe(res);
      });
      proxyReq.on('error', (err) => {
        res.writeHead(503, { 'Content-Type': 'application/json' });
        res.end(JSON.stringify({
          error: 'No se pudo conectar con el backend de producción (VPS en ' + upstreamUrl.origin + '). Asegúrate de tener el túnel SSH activo.',
          detail: err.message,
        }));
      });
      req.pipe(proxyReq);
      return;
    }
    if (req.method !== 'GET' && req.method !== 'HEAD') {
      res.writeHead(405); return res.end();
    }
    const path = resolve(root, '.' + (pathname === '/' ? '/index.html' : pathname));
    if (!path.startsWith(root + sep) || !['.html','.js','.css','.svg','.json'].includes(extname(path))) {
      res.writeHead(404); return res.end('No encontrado');
    }
    if (!(await stat(path)).isFile()) { res.writeHead(404); return res.end('No encontrado'); }
    const body = await readFile(path);
    res.writeHead(200, { 'Content-Type': types[extname(path)] || 'application/octet-stream' });
    res.end(req.method === 'HEAD' ? undefined : body);
  } catch {
    res.writeHead(404); res.end('No encontrado');
  }
}).listen(port, '127.0.0.1', () => console.log(`UI organizacional: http://127.0.0.1:${port}`));
