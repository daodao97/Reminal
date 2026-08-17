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

## Deploy

```bash
npm run deploy
```

## Before it goes live

- [ ] Nameservers for `reminal.app` and `reminal.dev` moved to Cloudflare
- [ ] Both domains attached to the `reminal-site` Worker in the dashboard
- [ ] `LOOPS_ENDPOINT` in `public/index.html` set to the real Loops form
- [ ] `curl -fsSL https://reminal.app/install.sh | sh` verified end to end
