// reminal.app storyline: one pinned scene beside chapters of copy (styles:
// story.css). Used by the home page and /agents. A page calls
//
//   Storyline(rootElement, ({ $, $$ }) => [drawChapter1, drawChapter2, …])
//
// where each draw function gets t, 0 → 1, for how far its chapter has been
// read, and sets its part of the scene from that alone, so scrolling back
// rewinds exactly. This file also carries the small scroll-animation maths
// the pages share (their lid stage uses it too), so load it before a page's
// own script.

// ── shared scroll-animation maths ──────────────────────────────────────
const reduced = matchMedia("(prefers-reduced-motion: reduce)").matches;
const clamp = (v, a = 0, b = 1) => v < a ? a : v > b ? b : v;
const mix = (a, b, t) => a + (b - a) * t;
const span = (t, a, b) => clamp((t - a) / (b - a));           // t's progress through [a, b]
const ease = t => t < .5 ? 4 * t * t * t : 1 - Math.pow(-2 * t + 2, 3) / 2;
const easeOut = t => 1 - Math.pow(1 - t, 3);                 // for count-ups
const seg = (t, a, b) => ease(span(t, a, b));                 // eased progress through [a, b]
const css = (el, o) => { if (el) for (const k in o) el.style[k] = o[k]; };
// Walk a list of [t, x, y] keyframes.
const path = (t, pts) => {
  if (t <= pts[0][0]) return pts[0].slice(1);
  for (let i = 1; i < pts.length; i++) {
    if (t <= pts[i][0]) {
      const u = ease((t - pts[i - 1][0]) / (pts[i][0] - pts[i - 1][0]));
      return [mix(pts[i - 1][1], pts[i][1], u), mix(pts[i - 1][2], pts[i][2], u)];
    }
  }
  return pts[pts.length - 1].slice(1);
};
// Type text into el as far as u (0 → 1) says, touching the DOM only on change.
const typedSoFar = new WeakMap();
const type = (el, text, u) => {
  const n = Math.round(text.length * clamp(u));
  if (typedSoFar.get(el) !== n) { el.textContent = text.slice(0, n); typedSoFar.set(el, n); }
};
// A click ripple at x, y, u through its life.
const ring = (el, x, y, u) => css(el, { transform: `translate(${x}px,${y}px) scale(${mix(.4, 1.6, u)})`, opacity: String(u > 0 && u < 1 ? 1 - u : 0) });
// The nav's height (--nav in theme.css): pinned things sit under it.
const NAV = parseFloat(getComputedStyle(document.documentElement).getPropertyValue("--nav")) || 52;

// ── the storyline ──────────────────────────────────────────────────────
function Storyline(story, chapters) {
  if (!story) return;
  const $ = (s) => story.querySelector(s);
  const $$ = (s) => [...story.querySelectorAll(s)];
  const chs = $$(".ch"), lys = $$(".ly");
  const box = $(".sx"), kEl = $(".sx-k"), num = $(".sx-count b");
  const stage = $(".story-stage"), pin = $(".story-pin");
  story.classList.add("js");
  const DRAW = chapters({ $, $$ });

  const HOLD_GAP = 20;                         // under the pinned scene → held copy (narrow)
  const FADE = 20;                             // px over which one scene hands to the next
  const narrow = () => innerWidth <= 900;
  // The reading area's top: under the nav beside the scene; under the pinned
  // scene on a narrow screen: where it is now, or where it sits once pinned.
  const readTop = (pinned) => !narrow() ? NAV : pinned ? NAV + stage.offsetHeight : stage.getBoundingClientRect().bottom;
  // A viewport height that holds still while a phone's address bar slides.
  const stableHeight = () => {
    const probe = document.createElement("div");
    probe.style.cssText = "position:fixed;top:0;height:calc(100 * var(--svh));visibility:hidden;pointer-events:none";
    document.body.appendChild(probe);
    const h = probe.offsetHeight; probe.remove();
    return h || innerHeight;
  };

  // Layout: where copy holds (--stick) and what holds there: the whole
  // chapter if it fits the reading area, otherwise each step that does.
  // Copy holds level with the top of the scene beside it, or just under the
  // scene pinned above it on a narrow screen.
  // Heights are read with nothing holding, then every class is set at once.
  function hold() {
    const vh = stableHeight();
    const stick = narrow() ? readTop(true) + HOLD_GAP : parseFloat(getComputedStyle(pin).top);
    const fits = el => el.offsetHeight <= vh - stick - 24;
    story.style.setProperty("--stick", stick + "px");
    const steps = $$(".step");
    chs.concat(steps).forEach(el => el.classList.remove("whole", "fits"));
    const whole = chs.map(ch => fits(ch.querySelector(".ch-in")));
    const each = steps.map(s => fits(s.firstElementChild));
    chs.forEach((ch, i) => ch.classList.toggle("whole", whole[i]));
    steps.forEach((s, i) => s.classList.toggle("fits", each[i] && !s.closest(".whole")));
    tail();
  }

  // On a narrow screen the scene is pinned above the copy. By default it lets
  // go only when the whole story has scrolled past, well after the last held
  // copy has, so that copy slid up underneath it before the scene left. The
  // scene lets go with the last held copy instead: its release point moves up
  // by --tail (margin under the pinned scene, taken back above the copy so
  // nothing in the flow moves). The last copy is held at the bottom of its
  // chapter when it lets go, HOLD_GAP under the scene.
  function tail() {
    let t = 0;
    const last = chs[chs.length - 1];
    const held = last.classList.contains("whole") ? last.querySelector(".ch-in")
      : last.querySelector(".step:last-child.fits > .step-in");
    if (narrow() && held) {
      const grid = stage.parentElement.getBoundingClientRect(), ch = last.getBoundingClientRect();
      t = grid.bottom - ch.bottom + parseFloat(getComputedStyle(last).paddingBottom) + held.offsetHeight + HOLD_GAP;
    }
    story.style.setProperty("--tail", t + "px");
  }

  // Each heading's offset inside its chapter, measured with nothing held.
  // Chapters hand over on their headings, not their boxes: a box starts a
  // screenful above its heading.
  let heads = [];
  function measure() {
    story.classList.add("measuring");
    heads = chs.map(ch => ch.querySelector("h2").getBoundingClientRect().top - ch.getBoundingClientRect().top);
    story.classList.remove("measuring");
  }

  let queued = false;
  function frame() {
    queued = false;
    const vh = innerHeight, top = readTop(false);
    // The reading line: a third of the way down the reading area.
    const line = top + (vh - top) / 3;
    // at[i]: where chapter i's heading is. A chapter whose copy has left the
    // reading area hands over at once, whatever the next heading is doing.
    const at = chs.map((ch, i) => ch.getBoundingClientRect().top + heads[i]);
    at.push(chs[chs.length - 1].getBoundingClientRect().bottom - (vh - line));
    for (let i = 0; i < chs.length - 1; i++) {
      const left = chs[i].querySelector(".step:last-child > .step-in").getBoundingClientRect().bottom - (top + 8);
      at[i + 1] = Math.min(at[i + 1], line + left);
    }
    // t: 0 → 1 from this heading crossing the line to the next one doing so.
    // out: the hand-over to the next scene, in the last FADE px before that.
    const ts = chs.map((ch, i) => clamp((line - at[i]) / (at[i + 1] - at[i])));
    const out = chs.map((ch, i) => i < chs.length - 1 ? clamp((line - at[i + 1] + FADE) / FADE) : 0);
    let shown = 0;
    lys.forEach((ly, i) => {
      const o = (i === 0 ? 1 : out[i - 1]) * (1 - out[i]);
      ly.style.opacity = String(o);
      ly.classList.toggle("on", o > 0);
      if (o > .5) shown = i;
      if (o > 0) DRAW[i](reduced ? 1 : span(ts[i], .02, .92));
    });
    if (num) num.textContent = String(shown + 1).padStart(2, "0");
  }

  // Holding is a layout decision, remade only when the layout really
  // changes: a new width, or fonts and images arriving. A height-only resize
  // (a phone's address bar) would otherwise flip chapters mid-scroll.
  let laidOutWidth = -1;
  function fit(e) {
    if (!(e && e.type === "resize" && innerWidth === laidOutWidth)) { laidOutWidth = innerWidth; hold(); }
    measure();
    const r = box.getBoundingClientRect();
    kEl.style.setProperty("--sxk", String(Math.min((r.width - 32) / 560, (r.height - 56) / 440)));
    frame();
  }
  addEventListener("scroll", () => { if (!queued) { queued = true; requestAnimationFrame(frame); } }, { passive: true });
  addEventListener("resize", fit);
  if (document.fonts) document.fonts.ready.then(() => fit());   // Geist changes every chapter's height
  addEventListener("load", () => fit());
  fit();
}
