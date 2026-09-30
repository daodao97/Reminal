# reminal.app

The marketing site. Static, with a small Worker in front for the short install
URLs (`reminal.app/install.sh`) and to fold `reminal.dev` into `reminal.app`.

Separate from `../cloudflare` (the relay) on purpose: the relay carries live
sessions, and a copy change should never be able to take it down.

## Develop

```bash
npm install
npm run dev          # builds assets, then wrangler dev on localhost:8787
```

`build.sh` turns the README's gifs in `../docs` into H.264 mp4s plus poster
frames — about 9× smaller and smoother than the gifs, which matters when five
of them are on one page. Output lands in `public/assets/` and is gitignored;
the gifs stay the single source of truth. Needs `ffmpeg` (`brew install ffmpeg`).

## Link previews

Each page's share image (`og:image`, what WhatsApp, iMessage, X or Slack show)
is a card in `public/og/`, rendered from `og/card.html` with the text in
`og/cards.json`. Chat apps crop the card to its middle square, so the template
keeps everything that must read inside the centre 630×630. To change a card,
edit its line in `cards.json` and re-render (the PNGs are committed):

```bash
npx -y -p playwright node og/render.mjs    # first time: npx playwright install chromium
```

Chat apps cache a URL's preview, so a changed card can take a while to show
for links that were already shared.

## Deploy

```bash
npm run deploy
```

## Before it goes live

- [ ] Nameservers for `reminal.app` and `reminal.dev` moved to Cloudflare
- [ ] Both domains attached to the `reminal-site` Worker in the dashboard
- [ ] `LOOPS_ENDPOINT` in `public/index.html` set to the real Loops form
- [ ] `curl -fsSL https://reminal.app/install.sh | sh` verified end to end
