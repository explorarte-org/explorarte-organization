import { cp, mkdir, rm, access } from 'node:fs/promises';
import { fileURLToPath } from 'node:url';
import { join } from 'node:path';

const root = fileURLToPath(new URL('../', import.meta.url));
await access(join(root, 'index.html'));
await rm(join(root, 'dist'), { recursive: true, force: true });
await mkdir(join(root, 'dist'), { recursive: true });
await cp(join(root, 'index.html'), join(root, 'dist/index.html'));
await cp(join(root, 'mission-demo.html'), join(root, 'dist/mission-demo.html'));
await cp(join(root, 'src'), join(root, 'dist/src'), { recursive: true });
console.log('Frontend compilado en web/dist (sin dependencias de terceros).');
