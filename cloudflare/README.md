# reminal relay (Cloudflare Workers)

Free hosted relay for reminal. Uses Cloudflare Workers + Durable Objects for WebSocket session pairing.

## Deploy (one time, free)

Requires a Cloudflare **free** account. Durable Objects use the SQLite backend (`new_sqlite_classes`), which is required on the free plan.

```bash
cd cloudflare
npm install
npx wrangler login   # or: export CLOUDFLARE_API_TOKEN=...
# Forks: replace account_id in wrangler.toml with your Cloudflare account ID.
# The upstream value is deliberately pinned to prevent accidental production
# deployments to whichever account happens to be authenticated.
npm run deploy
```

After deploy, wrangler prints your URL, e.g. `https://reminal-relay.<account>.workers.dev`.

For a persistent default in binaries built from this checkout, create the
gitignored build configuration:

```bash
cd .. # repository root, if you are still in cloudflare/
cp reminal.build.env.example reminal.build.env
# edit reminal.build.env with your deployed URL, then:
./scripts/build.sh
```

You can also configure one run without rebuilding (setting either URL is
enough; reminal derives the other):

```bash
REMINAL_RELAY=wss://your-url/ws ./dist/reminal
# or: REMINAL_WEB=https://your-url ./dist/reminal
```

`REMINAL_DEFAULT_RELAY` and `REMINAL_DEFAULT_WEB` are build-time inputs used by
`scripts/build.sh`; `REMINAL_RELAY` and `REMINAL_WEB` are runtime overrides.
Plain source builds retain the upstream public relay. GitHub releases optionally
read the build-time values from repository Actions variables of the same names;
without them, upstream release behavior is unchanged.

`reminal.build.env` is parsed as inert `KEY=VALUE` data. Only the two documented
build-default keys are accepted; it is not sourced or executed as shell code.

Optional: add a custom domain in the Cloudflare dashboard (free).

## Local dev

```bash
npm run dev
# In another terminal:
REMINAL_RELAY=ws://localhost:8787/ws ./dist/reminal
```

## Cost

Cloudflare Workers free tier includes 100,000 requests/day and Durable Objects with generous limits — more than enough for personal use.
