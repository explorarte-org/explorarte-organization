// Optional browser acceptance suite. Install Playwright in your test environment,
// or set PLAYWRIGHT_MODULE to its module URL. The application has no runtime deps.
import assert from 'node:assert/strict';
import { mkdir } from 'node:fs/promises';
import { demoSnapshot } from '../src/data.js';
const { chromium } = await import(process.env.PLAYWRIGHT_MODULE || 'playwright');
const browser = await chromium.launch({
  headless: true,
  ...(process.env.CHROMIUM_PATH ? { executablePath: process.env.CHROMIUM_PATH } : {}),
});
const base = process.env.UI_URL || 'http://127.0.0.1:4173';
await mkdir('test-results', { recursive: true });
const page = await browser.newPage({ viewport: { width: 1600, height: 1100 } });
const errors = [];
page.on('pageerror', error => errors.push(error.message));
try {
  await page.goto(base);
  await page.locator('.desk').first().waitFor();
  assert.match(await page.locator('.mode-badge').innerText(), /Demostración/);
  await page.screenshot({ path: 'test-results/office-desktop.png', fullPage: true });
  await page.locator('.desk').first().click();
  assert.equal(await page.locator('#modal').evaluate(el => el.open), true);
  await page.keyboard.press('Escape');
  await page.locator('#search').fill('sin-coincidencias-qa');
  assert.equal(await page.locator('.desk').count(), 0);
  await page.locator('#search').fill('');
  await page.locator('#chat-input').fill('/mision Campaña de prueba UI');
  await page.locator('#chat-form').evaluate(form => form.requestSubmit());
  await page.locator('#objective').waitFor({ state: 'visible' });
  assert.equal(await page.locator('#objective').inputValue(), 'Campaña de prueba UI');
  await page.locator('#budget').fill('-1');
  await page.locator('#mission-form').evaluate(form => form.requestSubmit());
  assert.ok((await page.locator('#mission-error').innerText()).length > 0);
  await page.locator('#budget').fill('2.125');
  await page.locator('#mission-form').evaluate(form => form.requestSubmit());
  await page.waitForFunction(() => !document.querySelector('#modal').open);
  await page.getByRole('heading', { name: 'Campaña de prueba UI', exact: true }).waitFor();
  await page.reload();
  await page.locator('[data-view="missions"]').click();
  assert.equal(await page.getByRole('heading', { name: 'Campaña de prueba UI', exact: true }).count(), 1);
  await page.locator('[data-view="learning"]').click();
  assert.match(await page.locator('.main-content').innerText(), /Skills aprendidas/);
  await page.locator('[data-view="finance"]').click();
  assert.match(await page.locator('.main-content').innerText(), /Estimación/);
  await page.locator('[data-view="office"]').click();
  await page.setViewportSize({ width: 390, height: 844 });
  await page.screenshot({ path: 'test-results/office-mobile.png', fullPage: true });
  assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), true, 'mobile must not overflow');
  await page.goto(base + '/?mode=live');
  await page.locator('.error').waitFor();
  assert.equal(await page.locator('.desk').count(), 0, 'live unavailable must not show demo roles');
  assert.equal(await page.locator('#new-mission').isDisabled(), true);
  assert.equal(await page.locator('#chat-input').isDisabled(), true);
  const hostile = structuredClone(demoSnapshot);
  hostile.departments[0].roles[0].name = '<img src=x onerror="window.__xss=1">';
  hostile.departments[0].roles[0].id = 'role" onclick="window.__xss=1';
  hostile.departments[0].color = 'red" onmouseover="window.__xss=1';
  await page.route('**/api/organization/snapshot', route => route.fulfill({ json: hostile }));
  await page.goto(base + '/?mode=live');
  await page.locator('.desk').first().waitFor();
  assert.equal(await page.locator('.desk img').count(), 0);
  await page.locator('.desk').first().click();
  assert.equal(await page.evaluate(() => Boolean(window.__xss)), false, 'API text must not execute HTML');
  assert.deepEqual(errors, []);
  console.log('PASS: desktop/mobile, role details, search, /mision, invalid budget, persisted campaign, tabs, live unavailable, escaped API text, no browser errors');
} finally {
  await browser.close();
}
