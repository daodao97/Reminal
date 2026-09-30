// Renders the link-preview cards in cards.json to public/og/<out>.png with
// card.html as the template. The PNGs are committed, so this only runs when a
// card's text or the template changes:
//
//   cd site && npx -y -p playwright node og/render.mjs
//
// (first run: `npx playwright install chromium`).
import { chromium } from "playwright";
import { readFileSync } from "node:fs";
import { fileURLToPath, pathToFileURL } from "node:url";
import { dirname, join } from "node:path";

const here = dirname(fileURLToPath(import.meta.url));
const cards = JSON.parse(readFileSync(join(here, "cards.json"), "utf8"));
const template = pathToFileURL(join(here, "card.html")).href;

// CHROMIUM=<path> uses a specific browser build instead of Playwright's own.
const browser = await chromium.launch(process.env.CHROMIUM ? { executablePath: process.env.CHROMIUM } : {});
const page = await browser.newPage({ viewport: { width: 1200, height: 630 }, deviceScaleFactor: 1 });
for (const c of cards) {
  await page.goto(`${template}?${new URLSearchParams({ t: c.t, l: c.l })}`);
  await page.evaluate(() => document.fonts.ready);
  const out = join(here, "..", "public", "og", `${c.out}.png`);
  await page.screenshot({ path: out });
  console.log("og/" + c.out + ".png");
}
await browser.close();
