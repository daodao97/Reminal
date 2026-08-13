# Security Policy

reminal grants a browser live control of a real machine — terminal, windows, input.
That makes its security properties the product, not a feature of it. Reports are
welcome and taken seriously.

## Reporting a vulnerability

**Report privately through GitHub Security Advisories:**
[Report a vulnerability](https://github.com/harshalgajjar/Reminal/security/advisories/new)

Please do **not** open a public issue, discussion, or pull request for a suspected
vulnerability. Public disclosure before a fix ships puts every running agent at risk,
because a reminal agent is by definition attached to someone's live machine.

If GitHub advisories are unavailable to you, open a public issue containing only the
words "security contact request" and no technical detail, and a private channel will
be arranged.

### What to include

The more of this you can provide, the faster the turnaround:

- Affected version (`reminal version`) and platform.
- Which component: agent (Go client), relay (Cloudflare Worker), or web viewer.
- A description of the attacker's starting position — what they know and control at
  the outset (e.g. "passive observer of relay traffic", "operator of the relay",
  "holds the session ID but not the PIN"). This matters more than anything else;
  see [the threat model](docs/security/threat-model.md) for the positions already
  considered in scope.
- Reproduction steps or a proof of concept.
- Impact as you see it.

### What to expect

| Stage | Target |
|---|---|
| Acknowledgement of report | 3 business days |
| Initial assessment and severity | 10 business days |
| Fix or documented mitigation for critical issues | 30 days |
| Public advisory | Coordinated with you, after a fix is available |

These are targets rather than contractual SLAs. If a date slips, you will be told.

Because reminal ships as a self-updating binary, a critical fix can be pushed to
every installed client without user action: the relay serves a `critical_min`
version floor at `/version`, and clients below it force-upgrade on their next
check (at most 24 hours). This is the mechanism used for security releases.

### Disclosure

Coordinated disclosure is the norm here. A public GitHub Security Advisory is
published once a fix is available, and reporters are credited by name unless they
ask otherwise. There is no bug bounty; reports are handled and credited regardless.

## Scope

**In scope** — the code in this repository:

- The Go agent and CLI (`internal/`, `cmd/`).
- Cryptography: the PIN-authenticated key exchange, owner-device authentication,
  and session encryption (`internal/crypto/`).
- The relay Worker and Durable Objects (`cloudflare/src/`).
- The web viewer (`internal/client/web/`).
- The install and update path (`install.sh`, `internal/updater/`).

**Out of scope:**

- Vulnerabilities in Cloudflare's platform — report those to Cloudflare.
- Attacks that require pre-existing root or physical access to the host machine.
  reminal does not defend against an attacker who already controls the machine it
  runs on; see [Non-goals](docs/security/threat-model.md#non-goals).
- Social engineering of a user into sharing their session ID and PIN. Both are
  intentionally human-transmissible.
- Denial of service against the public relay by volume.
- Missing hardening headers or similar findings on the static landing page with no
  demonstrated impact on a session.

## Current limitations

Two properties are worth knowing before you rely on them. Full detail and roadmap
live in the
[threat model](docs/security/threat-model.md#current-limitations-and-roadmap).

- **`reminal expose` is not end-to-end encrypted.** Port-forwards serve ordinary
  browser visitors who hold no reminal key material, so that traffic transits the
  relay in plaintext. Every other session path — terminal, window, desktop, and
  copy/paste — is end-to-end encrypted. Use a self-hosted relay for sensitive
  services.
- **Release archives are protected by TLS to GitHub.** Client-side signature
  verification of the archive, and Apple notarization of macOS builds, are on the
  roadmap.

## Security documentation

- [Security architecture](docs/security/architecture.md) — components, trust
  boundaries, key establishment, and exactly what the relay can observe.
- [Threat model](docs/security/threat-model.md) — adversaries, mitigations,
  residual risk, and non-goals.
- [Subprocessors and data handling](docs/security/subprocessors.md) — what is
  stored, where, and for how long.
- [Self-assessment](docs/security/self-assessment.md) — CAIQ-style questionnaire
  responses for security reviews.
