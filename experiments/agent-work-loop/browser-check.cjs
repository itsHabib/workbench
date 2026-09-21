// Optional independent browser acceptance. Install Playwright outside worker
// checkouts; pass the running application's URL as the first argument.
const assert = require('node:assert/strict');
const { chromium } = require('playwright');

async function main() {
  let browser;
  try {
    browser = await chromium.launch(process.env.PLAYWRIGHT_CHANNEL
      ? { channel: process.env.PLAYWRIGHT_CHANNEL } : {});
  } catch (error) {
    console.error(`Browser setup failed: ${error.message}`);
    process.exitCode = 2;
    return;
  }
  try {
    const page = await browser.newPage({ viewport: { width: 390, height: 844 } });
    await page.goto(process.argv[2]);
    const csv = 'title,body\r\n"Harbor, quoted","Line one\nLine two ""quoted"""\r\n';
    const response = page.waitForResponse(r => r.url().endsWith('/imports') && r.request().method() === 'POST');
    await page.locator('input[type=file]').setInputFiles({ name: 'quoted.csv', mimeType: 'text/csv', buffer: Buffer.from(csv) });
    assert.equal((await response).status(), 201, 'quoted CSV upload failed');
    await page.locator('tbody tr').first().waitFor();
    const rows = await page.locator('tbody tr').allTextContents();
    assert.equal(rows.length, 1, 'one quoted multiline document must display as one row');
    const cells = await page.locator('tbody tr').first().locator('td').allTextContents();
    assert.ok(cells.includes('Harbor, quoted'), 'title must preserve its comma');
    assert.ok(cells.includes('Line one\nLine two "quoted"'), 'body must preserve quotes and newline');
    const width = await page.evaluate(() => ({ page: document.documentElement.scrollWidth, viewport: innerWidth }));
    assert.ok(width.page <= width.viewport, 'mobile page overflows horizontally');
    const bad = page.waitForResponse(r => r.url().endsWith('/imports') && r.request().method() === 'POST');
    await page.locator('input[type=file]').setInputFiles({ name: 'invalid.csv', mimeType: 'text/csv', buffer: Buffer.from('wrong,header\nA,B\n') });
    assert.equal((await bad).status(), 400, 'invalid header must be rejected');
    await page.waitForFunction(() => /error|invalid|malformed/i.test(document.body.innerText));
    assert.ok(!/successfully/i.test(await page.locator('body').innerText()), 'old success remains after failed upload');
    console.log(JSON.stringify({ ok: true, checks: ['quoted multiline upload', 'exact displayed values', '390px layout', 'visible invalid-upload error'] }));
  } finally {
    await browser.close();
  }
}

main().catch(error => { console.error(`Browser upload/render check failed: ${error.message}`); process.exitCode = 1; });
