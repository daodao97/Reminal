// Capture a scroll-driven section of a live page as an image sequence.
//
// The docs GIFs are rendered from purpose-built scenes (see build.py). This one
// is different: the thing worth showing IS the website's scroll choreography,
// so the page itself is the scene and scroll position is the clock.
//
//   node capture-scroll.js <url> <selector> <outDir> <frames> [width] [height] [hideSelector] [endAt]
//
// hideSelector is hidden before capture — chrome that belongs to the page but
// not to the story, e.g. the sticky nav.
//
// endAt (0–1, default 1) stops short of the section's end. A `position:sticky`
// pin releases on the final pixels of its container, so scrolling the whole way
// captures the scene sliding out of frame — which then gets frozen by any
// trailing hold. Stop before the release and the last frame is the composition
// you actually want to rest on.
//
// Every transform in the lid stage is a pure function of scroll progress, so
// stepping the scrollbar and screenshotting is exact — no waiting on
// animations to settle, no frame-timing luck.
const { chromium } = require("playwright");

(async () => {
  const [url, selector, outDir, framesArg, wArg, hArg, hideSel, endArg] = process.argv.slice(2);
  const endAt = Math.min(1, Math.max(0, parseFloat(endArg || "1")));
  const frames = parseInt(framesArg || "48", 10);
  const width = parseInt(wArg || "1280", 10);
  const height = parseInt(hArg || "800", 10);

  const browser = await chromium.launch({ channel: "chrome" });
  const page = await browser.newPage({
    viewport: { width, height },
    deviceScaleFactor: 2,          // capture at 2x, downscale later — same as build.py
  });

  await page.goto(url, { waitUntil: "networkidle" });

  // Smooth scrolling would make every scrollTo an animation we'd have to wait
  // out; instant is both faster and reproducible.
  await page.addStyleTag({ content: "html{scroll-behavior:auto !important}" });
  if (hideSel) await page.addStyleTag({ content: `${hideSel}{display:none !important}` });

  const box = await page.evaluate(sel => {
    const el = document.querySelector(sel);
    if (!el) return null;
    return { top: el.offsetTop, height: el.offsetHeight };
  }, selector);
  if (!box) { console.error(`selector not found: ${selector}`); process.exit(1); }

  const travel = Math.max(1, box.height - height);
  console.log(`section ${box.height}px, ${travel}px of travel, ${frames} frames, ending at ${endAt}`);

  for (let i = 0; i < frames; i++) {
    const p = (i / (frames - 1)) * endAt;
    await page.evaluate(y => window.scrollTo(0, y), box.top + travel * p);
    // One rAF for the scroll handler to run, one more for the paint it queues.
    await page.evaluate(() => new Promise(r => requestAnimationFrame(() => requestAnimationFrame(r))));
    await page.waitForTimeout(45);
    await page.screenshot({ path: `${outDir}/f${String(i).padStart(3, "0")}.png` });
  }

  await browser.close();
  console.log(`captured ${frames} frames`);
})();
