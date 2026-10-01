import { chromium, devices } from 'playwright';
const BASE = 'http://127.0.0.1:18080';
const [A, APIN, B, BPIN] = process.argv.slice(2);
const b = await chromium.launch();
const out = [];

// ---- TEST 1: one PIN handshake per connect (was two) ----
{
  const ctx = await b.newContext({ ...devices['Pixel 7'] });
  const p = await ctx.newPage();
  let sends = 0;
  p.on('console', m => { if (/pake_init sent/.test(m.text())) sends++; });
  await p.goto(`${BASE}/?s=${A}&debug=true#p=${APIN}`, { waitUntil: 'domcontentloaded' });
  await p.waitForFunction(() => {
    const r = document.querySelector('.xterm-rows');
    return r && r.innerText.replace(/\s/g, '').length > 0;
  }, { timeout: 30000 }).catch(() => {});
  await p.waitForTimeout(2000);
  out.push(`TEST 1  pake_init sent per connect: ${sends}   ${sends === 1 ? 'PASS (was 2)' : 'FAIL'}`);
  await ctx.close();
}

// ---- TEST 2: a wrong PIN keeps saying PIN mismatch ----
{
  const ctx = await b.newContext({ ...devices['Pixel 7'] });
  const p = await ctx.newPage();
  const t0 = Date.now();
  await p.goto(`${BASE}/?s=${A}&debug=true#p=${BPIN}`, { waitUntil: 'domcontentloaded' });
  const seen = [];
  let prev = null;
  while (Date.now() - t0 < 16000) {
    const s = await p.evaluate(() => document.getElementById('status')?.textContent || '');
    if (s !== prev) { seen.push(`${Date.now() - t0}ms "${s}"`); prev = s; }
    await p.waitForTimeout(100);
  }
  const final = prev;
  out.push(`TEST 2  wrong PIN, status after 16s: "${final}"   ${/PIN mismatch/.test(final) ? 'PASS (no longer overwritten)' : 'FAIL'}`);
  out.push(`        timeline: ${seen.join(' → ')}`);
  await ctx.close();
}

// ---- TEST 3: Share must not hand out the previous session's PIN ----
{
  const ctx = await b.newContext({ ...devices['Pixel 7'] });
  const p = await ctx.newPage();
  await p.goto(`${BASE}/?s=${A}&debug=true#p=${APIN}`, { waitUntil: 'domcontentloaded' });
  await p.waitForFunction(() => {
    const r = document.querySelector('.xterm-rows');
    return r && r.innerText.replace(/\s/g, '').length > 0;
  }, { timeout: 30000 }).catch(() => {});
  const openShare = () => p.evaluate(() => {
    const btn = [...document.querySelectorAll('button,a,div')].find(e => /^\s*Share\s*$/.test(e.textContent || ''));
    if (btn) btn.click();
  });
  const shown = () => p.evaluate(() => ({
    session: document.getElementById('share-session')?.textContent || '',
    pin: document.getElementById('share-pin')?.textContent || '',
  }));
  await openShare(); await p.waitForTimeout(600);
  const first = await shown();
  await p.evaluate(() => { const m = document.getElementById('share-modal'); if (m) m.style.display = 'none'; });
  await p.evaluate(([id, pin]) => {
    const inp = document.getElementById('session-input');
    const pinEl = document.getElementById('pin-input');
    inp.value = id;
    pinEl.value = pin;
    pinEl.dispatchEvent(new Event('input', { bubbles: true }));
    inp.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true }));
    const f = inp.closest('form'); if (f) f.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }));
  }, [B, BPIN]);
  await p.waitForTimeout(4000);
  await openShare(); await p.waitForTimeout(600);
  const after = await shown();
  const leaked = after.pin === APIN;
  const switched = after.session.toUpperCase() === B.toUpperCase();
  if (!switched) out.push(`        NOTE: did not switch (still on ${after.session}) — test inconclusive`);
  out.push(`TEST 3  on ${first.session} Share showed pin ${first.pin}; after switching to ${after.session} it shows "${after.pin}"`);
  out.push(`        ${leaked ? 'FAIL — still the old session\'s PIN' : 'PASS — old PIN no longer handed out'}`);
  await ctx.close();
}
console.log(out.join('\n'));
await b.close();
