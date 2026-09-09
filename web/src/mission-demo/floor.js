// Visual geometry belongs to this renderer, not to OfficeProjection.
export const layout = [
  { id: 'direction', x: 3.2, y: 3.5, w: 29.3, h: 34.6, seats: [[25.5, 35.0]] },
  { id: 'research', x: 35.9, y: 3.5, w: 28.6, h: 34.6, seats: [[59.8, 35.0]] },
  { id: 'engineering', x: 67.6, y: 3.5, w: 28.9, h: 34.6, seats: [[73.8, 34.0], [83.2, 34.0], [92.0, 34.0]] },
  { id: 'business', x: 3.2, y: 62.0, w: 29.3, h: 35.0, seats: [[18.0, 91.0], [29.4, 91.0]] },
  { id: 'skills', x: 35.9, y: 62.0, w: 28.6, h: 35.0, seats: [[48.6, 91.0], [60.5, 91.0]] },
  { id: 'services', x: 67.6, y: 62.0, w: 28.9, h: 35.0, seats: [[91.4, 90.0]] },
];
const sheets = [[230,35,220,440],[650,35,220,440],[1070,35,220,440],[230,495,220,465],[650,495,220,465],[1080,495,220,465]];
const spriteByAgent = { ceo: 0, research: 1, ar: 2, fe: 3, op: 5, gr: 4, an: 3, sk: 4, ev: 2, qa: 5 };
export const spriteIndexFor = id => spriteByAgent[id] ?? 4;
let sprite;
const frames = [];
export async function loadFloorArt() {
  const floor = new Image(); floor.src = new URL('./assets/connected-floor.png', import.meta.url);
  sprite = new Image(); sprite.src = new URL('./assets/team-sheet.png', import.meta.url);
  await Promise.all([floor.decode(), sprite.decode()]);
  frames.length = 0;
  for (const [sx, sy, sw, sh] of sheets) {
    const frame = document.createElement('canvas'); frame.width = sw; frame.height = sh;
    const context = frame.getContext('2d', { willReadFrequently: true });
    context.drawImage(sprite, sx, sy, sw, sh, 0, 0, sw, sh);
    const pixels = context.getImageData(0, 0, sw, sh), seen = new Uint8Array(sw * sh), queue = [];
    const visit = index => {
      if (seen[index]) return; seen[index] = 1;
      const offset = index * 4, rgb = pixels.data.subarray(offset, offset + 3);
      if (Math.min(...rgb) < 214 || Math.max(...rgb) - Math.min(...rgb) > 25) return;
      pixels.data[offset + 3] = 0; queue.push(index);
    };
    for (let x = 0; x < sw; x++) { visit(x); visit((sh - 1) * sw + x); }
    for (let y = 0; y < sh; y++) { visit(y * sw); visit(y * sw + sw - 1); }
    for (let head = 0; head < queue.length; head++) {
      const index = queue[head], x = index % sw, y = Math.floor(index / sw);
      if (x) visit(index - 1); if (x < sw - 1) visit(index + 1);
      if (y) visit(index - sw); if (y < sh - 1) visit(index + sw);
    }
    context.putImageData(pixels, 0, 0); frames.push(frame);
  }
}
function shortName(name) {
  if (name === 'Diseñador de skills' || name === 'Diseñador de Skills') return 'Skills';
  if (name === 'Investigador') return 'Research';
  if (/Arquitecto/i.test(name)) return 'Arquitecto';
  if (/Frontend/i.test(name)) return 'Frontend';
  if (/Operaciones/i.test(name)) return 'Operaciones';
  if (/Evaluador/i.test(name)) return 'Evaluador';
  if (/Calidad|QA/i.test(name)) return 'Calidad';
  if (/Analista/i.test(name)) return 'Analista';
  return name;
}
function seatFor(room, index, count) {
  if (count <= room.seats.length) return room.seats[index];
  const columns = room.id === 'engineering' ? 4 : room.seats.length > 1 ? 2 : 1;
  const rows = Math.ceil(count / columns);
  const column = index % columns;
  const row = Math.floor(index / columns);
  const left = room.x + 8;
  const right = room.x + room.w - 8;
  const bottom = room.y + room.h - 6;
  const top = Math.max(room.y + 8, bottom - Math.max(1, rows - 1) * 9);
  const x = columns === 1 ? room.x + room.w / 2 : left + (right - left) * (column / (columns - 1));
  const y = rows === 1 ? bottom : bottom - (bottom - top) * (row / (rows - 1));
  return [x, y];
}
function bubblePosition(x, y, index, count, room) {
  if (count > 4) {
    const column = index % 2, row = Math.floor(index / 2);
    return {
      x: room.x + room.w * (column ? .73 : .27),
      y: room.y + 15 + row * 5.5,
      maxWidth: `${Math.min(18, room.w * .42)}%`,
    };
  }
  const preferredX = x + (count > 1 ? (index ? 2.4 : -2.4) : 0);
  return { x: Math.max(12, Math.min(88, preferredX)), y: Math.max(11, y - (count > 1 ? 14 : 12)), maxWidth: '' };
}
export function floorMarkup(snapshot, selected, selectedRoom, running) {
  return `<div class="floor-scene ${running ? 'is-running' : ''}" id="floor-scene">
    <img class="floor-art" src="/src/mission-demo/assets/connected-floor.png" alt="Piso conectado con biblioteca, escritorios, salas de trabajo y pasillo central" draggable="false">
    ${layout.map(room => {
      const people = snapshot.agents.filter(agent => agent.room === room.id), roomState = snapshot.rooms.find(item => item.id === room.id);
      return `<div class="room-boundary ${selectedRoom === room.id ? 'selected-room' : ''}" style="left:${room.x}%;top:${room.y}%;width:${room.w}%;height:${room.h}%"></div>
        <button class="room-label" style="left:${room.x + room.w / 2}%;top:${room.y + 1}%" data-room="${room.id}" aria-pressed="${selectedRoom === room.id}" aria-label="${roomState.name}. ${roomState.statusLabel}">${roomState.name}</button>
        <span class="room-status room-status-${roomState.status}" style="left:${room.x + room.w / 2}%;top:${room.y + room.h - 4}%">${roomState.statusLabel}</span>
        ${people.map((agent, index) => {
          const [x, y] = seatFor(room, index, people.length), bubble = bubblePosition(x, y, index, people.length, room);
          return `<span class="agent-bubble activity-${agent.activity}" style="left:${bubble.x}%;top:${bubble.y}%;${bubble.maxWidth ? `max-width:${bubble.maxWidth}` : ''}"><strong>${agent.activityLabel}</strong><span>${agent.bubble}</span></span><button class="agent ${selected === agent.agentId ? 'chosen' : ''}" style="left:${x}%;top:${y}%" data-person="${agent.agentId}" data-activity="${agent.activity}" aria-pressed="${selected === agent.agentId}" aria-label="Ver puesto de ${agent.name}. ${agent.activityLabel}. ${agent.bubble}" title="${agent.activityLabel}: ${agent.bubble}"><canvas width="110" height="220" data-sprite="${spriteIndexFor(agent.visualId || agent.agentId)}" aria-hidden="true"></canvas><span class="agent-name"><span class="name-long">${agent.name}</span><span class="name-short">${shortName(agent.name)}</span></span></button>`;
        }).join('')}`;
    }).join('')}
  </div>`;
}
function drawContained(canvas, frame) {
  const context = canvas.getContext('2d'); context.clearRect(0, 0, canvas.width, canvas.height); context.imageSmoothingEnabled = false;
  const scale = Math.min(canvas.width / frame.width, canvas.height / frame.height), width = frame.width * scale, height = frame.height * scale;
  context.drawImage(frame, (canvas.width - width) / 2, (canvas.height - height) / 2, width, height);
}
export function paintSprites(root) {
  for (const canvas of root.querySelectorAll('[data-sprite]')) drawContained(canvas, frames[Number(canvas.dataset.sprite)]);
  for (const canvas of root.querySelectorAll('[data-portrait]')) drawContained(canvas, frames[Number(canvas.dataset.portrait)]);
}
