import assert from 'node:assert/strict';

const { chromium } = await import(process.env.PLAYWRIGHT_MODULE || 'playwright');
const browser = await chromium.launch({
  ...(process.env.CHROMIUM_PATH ? { executablePath: process.env.CHROMIUM_PATH } : {}),
  headless: true,
  args: ['--no-sandbox'],
});

try {
  const page = await browser.newPage({ viewport: { width: 1536, height: 1024 } });
  const errors = [], failedRequests = [], serverErrors = [];
  page.on('pageerror', error => errors.push(error.message));
  page.on('requestfailed', request => failedRequests.push(`${request.method()} ${request.url()}`));
  page.on('response', response => {
    if (response.status() >= 500) serverErrors.push(`${response.status()} ${response.url()}`);
  });

  const liveUrl = process.env.LIVE_URL || 'http://127.0.0.1:4185/?source=vps';
  await page.goto(liveUrl, { waitUntil: 'domcontentloaded' });
  await page.locator('.floor-art').waitFor();
  await page.waitForFunction(() => document.querySelector('.demo-label')?.textContent.includes('VPS'));

  assert.match(await page.locator('.demo-label').innerText(), /VPS/);
  assert.equal(await page.locator('.room-label').count(), 6);
  assert.equal(await page.locator('.room-status').count(), 6);
  const options = await page.locator('#mission-select option').evaluateAll(nodes => nodes.map(node => ({ value: node.value, label: node.textContent })));
  assert.ok(options.length > 0, 'El VPS debe entregar al menos una misión.');

  for (const option of options) {
    await page.locator('#mission-select').selectOption(option.value);
    await page.waitForTimeout(450);
    assert.equal(await page.locator('.room-label').count(), 6, option.label);
    assert.equal(await page.locator('.room-status').count(), 6, option.label);
    assert.equal(await page.locator('.agent[data-person="ceo"]').count(), 1, option.label);
    assert.equal(await page.locator('.agent[data-person="research"]').count(), 1, option.label);
    assert.equal(await page.locator('.agent-bubble').count(), await page.locator('.agent').count(), option.label);
  }

  for (const option of options.filter(option => /MS-(598|599)\b/.test(option.label))) {
    await page.locator('#mission-select').selectOption(option.value);
    await page.waitForTimeout(650);
    assert.equal(await page.locator('.agent.chosen').getAttribute('data-person'), 'ceo', option.label);
    assert.match(await page.locator('.assignment').innerText(), new RegExp(`Task ID\\s+${option.label.match(/MS-(598|599)/)[1]}`));
    assert.match(await page.locator('.agent-work-card').innerText(), /ExecutivePlan/);
  }

  const broadMission = options.find(option => /MS-(556|96|95)\b/.test(option.label)) || options[0];
  await page.locator('#mission-select').selectOption(broadMission.value);
  await page.waitForTimeout(900);
  assert.equal(await page.locator('.agent[data-person="ceo"]').count(), 1);
  assert.equal(await page.locator('.agent[data-person="research"]').count(), 1);
  const agentCount = await page.locator('.agent').count();
  assert.ok(agentCount >= 2, 'La misión debe proyectar CEO e Investigación.');
  assert.equal(await page.locator('.agent-bubble').count(), agentCount);
  assert.equal(await page.locator('.agent canvas[data-sprite]').count(), agentCount);
  const seatPositions = await page.locator('.agent').evaluateAll(nodes => nodes.map(node => `${node.style.left}:${node.style.top}`));
  assert.equal(new Set(seatPositions).size, seatPositions.length, 'Los puestos proyectados no deben compartir asiento.');
  const agentIds = await page.locator('.agent').evaluateAll(nodes => nodes.map(node => node.dataset.person));
  for (const agentId of agentIds) {
    const agent = page.locator(`.agent[data-person="${agentId}"]`);
    const floorSprite = await agent.locator('canvas[data-sprite]').getAttribute('data-sprite');
    await agent.click();
    assert.equal(await page.locator('canvas[data-portrait]').getAttribute('data-portrait'), floorSprite, `El retrato debe coincidir con ${agentId}.`);
  }
  assert.match(await page.locator('.agent-work-card').innerText(), /Actividad observada.*VPS/i);
  await page.locator('[data-tab="activity"]').click();
  assert.match(await page.locator('#detail-panel').innerText(), /Actividad de/);

  if (await page.locator('.mission-result').count()) {
    assert.match(await page.locator('.mission-result').innerText(), /VPS/);
  }

  // El navegador nunca debe enviar una misión de prueba al VPS. Interceptamos
  // los POST para comprobar el contrato del chat y la confirmación de /mision.
  await page.locator('[data-tab="chat"]').click();
  await page.locator('#ceo-chat-input').fill('Texto que debe sobrevivir al refresco');
  await page.waitForTimeout(5400);
  assert.equal(await page.locator('#ceo-chat-input').evaluate(element => element === document.activeElement), true, 'El refresco live no debe sacar el foco del chat.');
  assert.equal(await page.locator('#ceo-chat-input').inputValue(), 'Texto que debe sobrevivir al refresco');
  let chatRequest;
  await page.route('**/api/organization/ceo/messages', async route => {
    if (route.request().method() !== 'POST') return route.continue();
    chatRequest = JSON.parse(route.request().postData() || '{}');
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ message: 'Respuesta de prueba del CEO.' }),
    });
  });
  const chatRequestSent = page.waitForRequest(request => request.method() === 'POST' && request.url().includes('/api/organization/ceo/messages'), { timeout: 10000 });
  await page.locator('#ceo-chat-input').fill('Dame un resumen del estado');
  await page.locator('#ceo-chat-form').evaluate(form => form.requestSubmit());
  await chatRequestSent;
  await page.waitForFunction(() => document.querySelector('.ceo-chat-messages')?.innerText.includes('Respuesta de prueba del CEO'));
  assert.deepEqual(chatRequest, { message: 'Dame un resumen del estado' });
  assert.match(await page.locator('.ceo-chat-messages').innerText(), /Respuesta de prueba del CEO/);

  let missionRequest;
  await page.route('**/api/organization/missions', async route => {
    if (route.request().method() !== 'POST') return route.continue();
    missionRequest = {
      body: JSON.parse(route.request().postData() || '{}'),
      idempotency: route.request().headers()['idempotency-key'],
    };
    await route.fulfill({
      status: 201,
      contentType: 'application/json',
      body: JSON.stringify({
        mission: { id: 'MS-CHAT-TEST' },
        message: 'Misión de prueba recibida por el CEO.',
      }),
    });
  });
  await page.locator('#ceo-chat-input').fill('/mision Auditar la oficina');
  await page.locator('#ceo-chat-form').evaluate(form => form.requestSubmit());
  await page.locator('#mission-composer-form').waitFor();
  await page.locator('#mission-budget').fill('5');
  const missionRequestSent = page.waitForRequest(request => request.method() === 'POST' && request.url().endsWith('/api/organization/missions'), { timeout: 10000 });
  await page.locator('#mission-composer-form').evaluate(form => form.requestSubmit());
  await missionRequestSent;
  await page.waitForFunction(() => document.querySelector('.ceo-chat-messages')?.innerText.includes('Misión de prueba recibida'));
  assert.deepEqual(missionRequest.body, { objective: 'Auditar la oficina', budgetMicrousd: 5000000 });
  assert.match(missionRequest.idempotency, /^[0-9a-f-]{36}$/);

  await page.setViewportSize({ width: 390, height: 844 });
  assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), true, 'La vista live no debe desbordar en móvil.');
  assert.equal(await page.locator('.agent-bubble').evaluateAll(nodes => nodes.some(node => {
    const box = node.getBoundingClientRect();
    return box.left < 0 || box.right > innerWidth;
  })), false, 'Los globos live deben quedar dentro de la pantalla móvil.');
  assert.equal(await page.locator('.simulation-status').innerText().then(text => /VPS/.test(text)), true);
  assert.deepEqual(errors, []);
  assert.deepEqual(failedRequests, []);
  assert.deepEqual(serverErrors, []);
  console.log(`PASS: snapshot VPS, reportes, ${agentCount} puestos, globos, sprites coherentes, seis oficinas y resultado observado.`);
} finally {
  await browser.close();
}
