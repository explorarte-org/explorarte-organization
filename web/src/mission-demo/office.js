// Star Office UI artwork is bundled for this non-commercial demo only.
// See assets/ATTRIBUTION.md and assets/STAR-OFFICE-LICENSE.txt.
const files = ['office_bg.webp','desk-v3.webp','guest_role_1.png','guest_role_2.png','guest_role_3.png'];
const art = new Map();
export async function loadOfficeArt() {
 await Promise.all(files.map(async file => {
  const img = new Image();img.src=new URL(`./assets/${file}`,import.meta.url).href;
  await img.decode();art.set(file,img);
 }));
}
export function positionFor(index, phase, tick=0) {
 const work=[{x:345,y:515},{x:500,y:535}];
 const research=[{x:335,y:282},{x:440,y:310}];
 const review=[{x:545,y:355},{x:625,y:410}];
 const rest=[{x:240,y:615},{x:365,y:620}];
 const point=[research,work,review,rest][phase][index%2];
 return {...point,y:point.y+(tick%2 ? 2:0)};
}
export function drawOffice(canvas, people, selected, tick, phase, department) {
 const ctx=canvas.getContext('2d');ctx.imageSmoothingEnabled=false;
 const bg=art.get('office_bg.webp');if(!bg)return;
 // A square viewport into the reference's furnished office, with no distortion.
 ctx.drawImage(bg,280,0,720,720,0,0,720,720);
 const rect=(x,y,w,h,c)=>{ctx.fillStyle=c;ctx.fillRect(x,y,w,h);};
 // Department-specific notice board, laptop station and shared meeting table.
 rect(25,68,126,90,'#725239');rect(31,74,114,78,'#ddcba5');
 ['#8cab87','#7295b2','#b99262','#9c819f','#a5a368','#75a19a'].slice(department,department+1).forEach(c=>{rect(40,85,44,25,c);rect(91,85,40,25,c);});
 rect(40,121,90,4,'#78674d');rect(40,132,70,4,'#78674d');
 ctx.drawImage(art.get('desk-v3.webp'),235,390,205,160);
 if(people.length>1)ctx.drawImage(art.get('desk-v3.webp'),445,430,175,136);
 // Seats in the lounge are drawn geometry, separate from the sourced scenery.
 rect(145,569,140,63,'#705947');rect(150,573,130,53,'#a78e69');rect(144,594,17,47,'#8a704f');rect(269,594,17,47,'#8a704f');
 people.forEach((p,j)=>{
  const pos=positionFor(j,phase,tick);
  ctx.fillStyle=p.id===selected?'#d4ed9b':'#594f3b';ctx.beginPath();ctx.ellipse(pos.x,pos.y+24,24,7,0,0,Math.PI*2);ctx.fill();
  const sprite=art.get(`guest_role_${(department+j)%3+1}.png`);
  ctx.drawImage(sprite,(tick%4)*32,0,32,32,pos.x-48,pos.y-67,96,96);
 });
}
