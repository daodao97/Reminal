// Frame capture for the animated docs GIFs. Loads an HTML "scene" that exposes
// window.TOTAL (frame count) and window.render(tick), drives it one tick at a
// time in headless Chrome at 2x, and writes a PNG per frame. build.py then adds
// the border and assembles the GIF. See scripts/gif/README.md.
//
//   NODE_PATH=<dir-with-playwright> node capture.js <url> <outdir> <W> <H>
//
// Requires the `playwright` package and Google Chrome (channel: "chrome").
const { chromium } = require('playwright');

(async () => {
  const [url, outdir, W, H] = [process.argv[2], process.argv[3], +process.argv[4], +process.argv[5]];
  const browser = await chromium.launch({ channel: 'chrome' });
  const page = await browser.newPage({ viewport: { width: W, height: H }, deviceScaleFactor: 2 });
  await page.goto(url, { waitUntil: 'networkidle' });
  await page.waitForTimeout(200);
  const total = await page.evaluate(() => window.TOTAL || 48);
  for (let i = 0; i < total; i++) {
    await page.evaluate(t => window.render(t), i);
    await page.waitForTimeout(16); // let a layout/paint settle
    await page.screenshot({ path: `${outdir}/f${String(i).padStart(3, '0')}.png`, clip: { x: 0, y: 0, width: W, height: H } });
  }
  await browser.close();
  console.log('captured', total, 'frames');
})();
