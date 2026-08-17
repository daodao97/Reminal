// The marketing site for reminal.
//
// Deliberately its own Worker, separate from reminal-relay: the relay carries
// live sessions, and a copy tweak should never be able to take it down.
//
// Everything is static except two things this handles by hand:
//
//   * short install URLs — `curl -fsSL https://reminal.app/install.sh | sh` is
//     what goes on the site, in posts, and in people's shell history, so it
//     redirects to the script on main rather than pinning a copy that quietly
//     goes stale;
//   * one canonical host — reminal.dev and any www. variant fold into
//     reminal.app so links, OG cards and analytics don't fragment.

const RAW = "https://raw.githubusercontent.com/harshalgajjar/Reminal/main";
const REPO = "https://github.com/harshalgajjar/Reminal";

const CANONICAL_HOST = "reminal.app";

// Hosts we own and fold into the canonical one. workers.dev and localhost are
// absent on purpose — that's where the thing gets tested before DNS exists.
const ALIASES = new Set([
  "reminal.dev",
  "www.reminal.dev",
  "www.reminal.app",
]);

const REDIRECTS = new Map([
  ["/install.sh", `${RAW}/install.sh`],
  ["/install.ps1", `${RAW}/install.ps1`],
  ["/github", REPO],
  ["/repo", REPO],
  ["/security", `${REPO}/blob/main/SECURITY.md`],
  ["/licensing", `${REPO}/blob/main/LICENSING.md`],
  ["/releases", `${REPO}/releases`],
]);

export default {
  async fetch(request, env) {
    const url = new URL(request.url);

    // Empty 204 the page times to show the visitor their own round-trip to
    // the nearest Cloudflare edge. Uncached and bodiless so the number is
    // network latency and nothing else.
    if (url.pathname === "/ping") {
      return new Response(null, {
        status: 204,
        headers: {
          "cache-control": "no-store, no-cache, must-revalidate",
          "access-control-allow-origin": "*",
          "timing-allow-origin": "*",
        },
      });
    }

    if (ALIASES.has(url.hostname)) {
      url.hostname = CANONICAL_HOST;
      return Response.redirect(url.toString(), 301);
    }

    // Trailing slashes are forgiven so /install.sh/ isn't a 404 someone has to
    // debug from a phone.
    const path = url.pathname.length > 1 ? url.pathname.replace(/\/+$/, "") : url.pathname;
    const target = REDIRECTS.get(path);
    if (target) {
      // 302, not 301: the install scripts move around, and a permanent
      // redirect cached in someone's shell is a bug you can't recall.
      return Response.redirect(target, 302);
    }

    return env.ASSETS.fetch(request);
  },
};
