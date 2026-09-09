import { createServer, request as httpRequest } from 'node:http';
import { readFile } from 'node:fs/promises';
import { fileURLToPath } from 'node:url';
const root = new URL('../', import.meta.url);
const apiUpstream = process.env.DEMO_API_UPSTREAM ? new URL(process.env.DEMO_API_UPSTREAM) : null;
const allowed = new Map([['/', 'mission-demo.html'], ['/mission-demo.html', 'mission-demo.html'], ...['app.js','model.js','projection.js','remote.js','floor.js','panels.js','styles.css','offices.css'].map(name => ['/src/mission-demo/'+name, 'src/mission-demo/'+name]), ['/src/api.js', 'src/api.js'], ['/src/data.js', 'src/data.js']]);
for (const name of ['connected-floor.png','team-sheet.png']) allowed.set('/src/mission-demo/assets/'+name,'src/mission-demo/assets/'+name);
createServer(async (req,res) => {
 const pathname = new URL(req.url,'http://localhost').pathname;
 if (pathname.startsWith('/api/') && apiUpstream) {
  const proxyReq = httpRequest({hostname:apiUpstream.hostname,port:apiUpstream.port,path:req.url,method:req.method,headers:{...req.headers,host:apiUpstream.host}}, proxyRes => { res.writeHead(proxyRes.statusCode || 502, proxyRes.headers); proxyRes.pipe(res); });
  proxyReq.on('error', error => { res.writeHead(503, {'Content-Type':'application/json; charset=utf-8','Cache-Control':'no-store'}); res.end(JSON.stringify({error:'No se pudo conectar con el VPS.', detail:error.message})); });
  req.pipe(proxyReq); return;
 }
 const file = allowed.get(pathname);
 if (!file || !['GET','HEAD'].includes(req.method)) { res.writeHead(404);res.end();return; }
 try { const body=await readFile(fileURLToPath(new URL(file,root)));res.writeHead(200,{'Content-Type':file.endsWith('.js')?'text/javascript':file.endsWith('.css')?'text/css':file.endsWith('.webp')?'image/webp':file.endsWith('.png')?'image/png':'text/html; charset=utf-8','Cache-Control':'no-store'});res.end(req.method==='HEAD'?undefined:body); } catch {res.writeHead(500);res.end('No se pudo cargar la demo. Reinicia el servidor local.');}
}).listen(Number(process.env.PORT||4184),'127.0.0.1',()=>console.log(`Demo: http://127.0.0.1:${Number(process.env.PORT||4184)}`));
