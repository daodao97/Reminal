# reminal Security Architecture

**Status:** current as of the version in this repository.
**Audience:** security reviewers, IT administrators, and anyone deciding whether to
run reminal on a machine that matters.

Every claim below is traceable to source in this repository, and the relevant file
is cited so you can verify it rather than trust it. Where a property has a boundary,
that boundary is stated precisely — see
[What the relay can observe](#5-what-the-relay-can-observe) and
[Current limitations](#10-current-limitations).

---

## 1. What reminal does

reminal gives a web browser live, interactive access to a machine: a real terminal
(PTY), live capture of individual application windows or the whole desktop, and
synthetic keyboard and mouse input into those windows. It reaches the browser
without opening any inbound port on the host.

The security question this document answers is: **who can see or influence that
access, and what would each of them have to compromise to do so.**

## 2. Components and trust boundaries

There are three parties and one intermediary.

```
   ┌──────────────────┐         ┌────────────────┐         ┌──────────────────┐
   │   Agent (host)   │         │     Relay      │         │  Viewer (browser)│
   │  Go binary on    │◄───────►│  Cloudflare    │◄───────►│  JS in a tab     │
   │  your machine    │   WSS   │  Worker + DO   │   WSS   │                  │
   └──────────────────┘         └────────────────┘         └──────────────────┘
            │                     UNTRUSTED                          │
            │                     routes ciphertext                  │
            └───────────── end-to-end encrypted channel ─────────────┘
                        AES-256-GCM, key never sent to relay

            ╌╌╌╌╌╌╌╌╌╌╌╌ optional direct path ╌╌╌╌╌╌╌╌╌╌╌╌
            WebRTC DataChannel (DTLS), signaling carried
            inside the encrypted channel above
```

**Agent** — the `reminal` binary running on the host. Holds the PTY, performs
screen capture, injects input, and holds the session key. Fully trusted; it *is*
the machine.

**Relay** — a Cloudflare Worker with a per-session Durable Object
(`cloudflare/src/`). It matches an agent to its viewers by session ID and forwards
frames between them. **It is explicitly untrusted.** The design assumes the relay
may be passively recording everything and may be actively malicious, and the
cryptography is built to survive that. This is what makes the free public relay
acceptable and what makes self-hosting a deployment choice rather than a security
necessity.

**Viewer** — the web client in a browser. Becomes trusted only after proving it
holds the PIN or an enrolled owner key.

The trust boundary that matters is between the encrypted end-to-end channel and
everything the relay sits on. All the cryptography below exists to place the relay
outside that boundary.

## 3. Credentials and identity

| Credential | Form | Entropy | Lifetime | Storage |
|---|---|---|---|---|
| Session ID | 8 chars, 32-symbol unambiguous alphabet (`internal/session/id.go`) | 32⁸ ≈ 1.1 × 10¹² (~40 bits) | One agent run | Memory only |
| PIN | 6 digits (`internal/session/pin.go`) | 10⁶ (~20 bits) | One agent run | Memory only |
| Device owner key | Ed25519 | 128-bit security | Until revoked | `~/.reminal/device_ed25519`, mode 0600 |
| Machine identity key | Ed25519 | 128-bit security | Until re-provisioned | Host key store, mode 0600 |
| Session key | 256-bit random (`internal/crypto/box.go`) | 256 bits | One agent run | Memory only, both ends |

Session IDs and PINs are generated with `crypto/rand`, never a seeded PRNG. Neither
is written to disk and neither survives the agent process — `Ctrl+C` destroys them
irrecoverably.

The session ID and PIN are **separate factors on purpose**. The session ID is the
relay's routing key and is therefore necessarily known to the relay; the PIN never
reaches the relay in any form it can use. Compromising one yields nothing.

## 4. Key establishment

A fresh random 256-bit AES-256-GCM session key is minted per agent run
(`crypto.NewSessionKey`). Every viewer, however it authenticates, ends up holding
that same key, so the agent encrypts the PTY stream once regardless of viewer count.
The key is delivered by one of two handshakes.

### 4.1 PIN path — EKE-style authenticated ECDH

Implemented in `internal/crypto/kex.go`.

1. Agent and viewer each generate an **ephemeral X25519 keypair, per WebSocket
   connection**.
2. Each blinds its public key by XOR with a 32-byte mask
   `HKDF-SHA256(IKM = PIN, salt = "reminal-blind-v2", info = "reminal-kex-v2")`.
   Every 32-byte string is a valid Montgomery u-coordinate, so a blinded key is
   uniformly distributed for *every* PIN — an observer cannot test a PIN guess
   against it.
3. Blinded keys are exchanged, each side unblinds with its own PIN, and both run
   ECDH.
4. The wrap key is `HKDF-SHA256(IKM = shared secret, salt = ex_id, info = "reminal-wrap-v2")`,
   where `ex_id` is a fresh 16-byte per-handshake correlation ID.
5. The agent wraps the session key under that key with AES-256-GCM; the viewer
   unwraps. **A successful unwrap is the proof that both sides used the same PIN.**

The essential property: **there is nothing here a passive observer can attack
offline.** To test a PIN guess an attacker needs either an ephemeral private key
(destroyed after the handshake) or a value whose distribution depends on the PIN
(there isn't one). Recorded traffic stays unreadable even if the PIN is later
disclosed — the handshake is forward-secret.

> **Historical note, stated deliberately.** Before v2 the wire key was
> `HKDF(PIN, sessionID)`. Since the session ID is the relay's routing key, that key
> had only ~20 bits of secrecy against the relay, and a passive relay could have
> recovered it offline from a single captured frame. This was found and fixed
> (GitHub issue #1); the current construction exists specifically to close it. The
> comments in `kex.go` preserve the analysis. Reviewers should assume any deployment
> older than v2 is compromised against its relay.

### 4.2 Owner path — mutually-authenticated signed ECDH

Implemented in `internal/crypto/owner.go`. An enrolled device connects with **no
PIN at all**, which removes the weakest credential from routine use.

The exchange is Noise-IK in shape: raw (unblinded) ephemeral X25519 keys are
exchanged, both sides run ECDH, and each signs a transcript with its long-lived
Ed25519 identity — the device with its owner key, the agent with its machine key.
The transcript is a length-prefixed digest binding the session ID, **both**
ephemeral public keys, and **both** identity public keys, under distinct
domain-separation tags for the client and server directions. A signature is
therefore meaningless on any other exchange, and no identity can be substituted by
a relay in the middle.

Two directions of authentication matter here:

- The agent verifies the device signature against its authorised owner list, so
  only enrolled devices connect.
- The device verifies the agent signature against the machine key it **pinned on
  first connect** (trust-on-first-use, `~/.reminal/known_machines.json`). A relay
  impersonating the host is detected, and the user is told the machine identity
  changed rather than silently connected.

**Enrollment is privilege-separated.** The authorised owner list lives in a
root-owned system location (`/etc/reminal/owners.json`), which is what makes
`add owner` require `sudo` — an unprivileged process on the host cannot enroll
itself as an owner. Revocation is deliberately asymmetric: tombstones live in the
agent-writable `~/.reminal/revoked_owners.json` and **override** the root-owned
list, so revoking a lost device never requires `sudo` and cannot be undone by
restoring a backup of `owners.json` (`internal/client/revoked.go`).

## 5. What the relay can observe

This section is the one a security reviewer should read most carefully.

### 5.1 Terminal, window, and desktop sessions — end-to-end encrypted

Session payloads are sealed with AES-256-GCM under the session key
(`internal/crypto/box.go`) before they reach the relay. The relay sees:

- The session ID (it needs it to route).
- Frame sizes and timing.
- Connection metadata: when an agent attached, how many viewers, when they left.
- Ciphertext.

It does **not** see terminal output, keystrokes, screen contents, filenames, window
titles, or the session key. It cannot obtain the key: it never transits the relay
in usable form, and the relay performs no PIN verification of its own — by design.
The comment in `internal/relay/auth.go` is explicit about why: a relay that could
check a 6-digit PIN would be able to brute-force it offline and, worse, would be
able to unblind both ephemeral keys and MITM the exchange. So it deliberately
holds no capability it does not need.

**WebRTC.** When a direct peer-to-peer path is negotiated, the signaling (SDP, ICE)
travels *inside* the already-encrypted session channel. The relay therefore cannot
tamper with DTLS fingerprints, which closes the usual signaling-server MITM window.
Media and data frames on the DataChannel are DTLS-protected end-to-end.

### 5.2 Copy/paste rendezvous — end-to-end encrypted

`reminal copy` → `reminal paste` is brokered by a separate blind Durable Object
(`cloudflare/src/rendezvous.ts`) that pairs two sockets by a short code and relays
frames verbatim. The code-authenticated handshake runs end-to-end through it, so
the relay learns neither the code, the transfer key, the filename, nor the bytes.

A short code is safe here because of three properties that do not hold for a
store-and-forward system: the source must be **online**, so there is no stored
ciphertext to attack offline; the offer is **burned on first pairing**, so a code
is worth exactly one live guess; and burned, expired, and never-existed codes all
return an **identical error**, so the relay never confirms a code was real. An
unclaimed offer is capped by a one-hour server-side TTL.

### 5.3 `reminal expose` — NOT end-to-end encrypted

**This is a real and deliberate exception to the claims above.**

The port-forward feature publishes a local HTTP service at a relay URL for an
ordinary web visitor. That visitor is a normal browser with no reminal key
material, so there is no end-to-end key to use. Request and response bodies are
therefore relayed through the Durable Object as base64-encoded **plaintext**
(`cloudflare/src/session.ts`), and the relay can observe them.

What still protects it: TLS on every hop; an optional bcrypt-hashed PIN gate
enforced at the relay, with a 5-attempt lockout and a 5-minute cooldown; and an
HMAC-signed, `HttpOnly` `Secure` `SameSite=Lax` session cookie scoped to that
session's path only.

**Guidance:** treat `reminal expose` as equivalent to putting the service behind a
third-party HTTP proxy you do not control. Do not use it for regulated or sensitive
data on a relay you do not operate. Organizations that need it should run a
self-hosted relay, where the plaintext stays on infrastructure they control.

## 6. Data at rest

### 6.1 On the relay

The per-session Durable Object stores only what it needs for routing and
reattachment (`cloudflare/src/session.ts`):

| Key | Purpose |
|---|---|
| `token` | High-entropy reattach credential, so only the original agent can reclaim a room |
| `pinHash` | Legacy bcrypt credential, superseded by `token`; also gates `expose` |
| `agentAuthed`, `viewerAuthed` | Whether a room has a live agent holding it |
| `failedAttempts`, `lockedUntil` | Lockout state for the `expose` PIN gate |
| `tunnelMeta` | Port, gate mode, and cookie signing key for an active port-forward |

**No session content is stored at any point.** There is no scrollback, no frame
buffer, no recording, and no message log.

**Retention.** When the agent disconnects, an alarm is armed for 10 minutes to
allow reattachment across a network blip. On expiry, connected viewers are closed
and the DO calls `deleteAll()` — every key above is destroyed. Rendezvous rooms are
capped at one hour. **Maximum retention of any session record is therefore 10
minutes past disconnect, and it is enforced by the runtime, not by policy.**

**Logging.** The Worker source contains no `console` logging of any kind — verifiable
with `grep -rn "console\." cloudflare/src`. Cloudflare's own edge request metadata is
outside reminal's control; see [subprocessors](subprocessors.md).

### 6.2 On the host

| Path | Contents | Mode |
|---|---|---|
| `~/.reminal/settings.json` | User preferences | 0600 |
| `~/.reminal/device_ed25519` | This device's owner key | 0600 |
| `~/.reminal/known_machines.json` | Pinned machine identities (TOFU) | 0600 |
| `~/.reminal/revoked_owners.json` | Revocation tombstones | Agent-writable |
| `/etc/reminal/owners.json` | Authorised owner devices | Root-owned |

Key files are written atomically at 0600, and a present-but-corrupt key is surfaced
as an error rather than silently regenerated — silent regeneration would swap the
machine's identity and orphan every trust relationship pinned against it.

**No session content is ever written to disk**, and no long-lived remote-access
credential exists on disk. This is the concrete sense in which reminal has "no keys
on disk" relative to SSH: there is no equivalent of a stealable `id_rsa` that grants
standing access.

## 7. Network posture

The agent makes **outbound connections only**. It binds no TCP port, so there is
nothing on the network to scan, brute-force, or exploit — the entire class of
"exposed service" vulnerabilities does not apply. All hops use WSS/TLS in
production.

For completeness, since a reviewer running `lsof` will see them: the agent does
create **Unix domain sockets** for local inter-process communication with its
capture and control helpers (`internal/client/mirror.go`,
`internal/client/control.go`). These are filesystem objects reachable only by local
processes with the requisite permissions, not network endpoints. Separately, a
**self-hosted relay** does bind a TCP listener (`internal/client/relay.go`) — that
is the server role, and it is the operator's to place behind their own TLS
termination and network controls.

Outbound destinations are limited to the configured relay, GitHub (release and
version checks), and — for WebRTC — STUN/TURN as configured.

## 8. Host privileges

On macOS, window capture and input injection require explicit TCC grants: **Screen
Recording** (ScreenCaptureKit) and **Accessibility** (`CGEvent` injection is
silently dropped without it). These are consent-gated by the operating system and
visible in System Settings; reminal cannot grant them to itself. Capture and input
are handled by a dedicated helper (`native/reminal-capture`) rather than the main
binary.

reminal is packaged as a signed application bundle so that these grants anchor to
one stable identity across upgrades rather than re-prompting each release.

**This is a genuinely powerful capability set and should be reviewed as such.** A
compromise of the agent process is equivalent to full interactive control of the
user's session. The mitigations are that the capability requires OS-level user
consent, the agent holds no standing credential that would let a remote party
reattach after exit, and the relay cannot originate a session on its own.

## 9. Software supply chain

- Source is public and AGPL-3.0 licensed; the relay Worker is in-repo, so the
  server side is auditable and self-hostable rather than a black box.
- Releases are built in GitHub Actions from tagged commits.
- macOS builds are code-signed for a stable TCC identity.
- The updater fetches release archives from GitHub over HTTPS and installs them
  atomically with rollback on failure.
- Clients check for updates at most every 24 hours. The relay serves a
  `critical_min` floor at `/version`, letting a security release be forced to all
  clients within that window.
- **No telemetry or analytics.** There is no usage reporting, crash reporting, or
  third-party SDK in the client — verifiable by grepping the tree for the usual
  vendors; there are no hits.

## 10. Current limitations

None of the below is a known vulnerability. Full detail, including the attack path
where one exists, is in the threat model's
[current limitations and roadmap](threat-model.md#current-limitations-and-roadmap).

| Limitation | What it means | Status |
|---|---|---|
| `reminal expose` transits the relay in plaintext | The relay operator can observe port-forwarded HTTP (§5.3) | Inherent to serving visitors who hold no reminal key. Self-host the relay for sensitive services |
| Release archives are not signature-verified beyond TLS | An attacker who first compromised release publishing could serve a malicious update | Fix identified: sign releases and verify client-side in `internal/updater` |
| macOS builds are code-signed but not Apple-notarized | Affects Gatekeeper prompts and MDM deployment; not an attack path | Requires an Apple Developer ID |
| No third-party cryptographic review | The handshakes have not been evaluated by an independent reviewer; their rationale is documented inline for those who wish to | Scoped, unfunded |
| No SSO/SAML, SCIM, or connection audit logging | Limits deployment under enterprise identity governance | Not built |
| Legacy `pinHash` accepted for reattach alongside `token` | A weaker credential remains on the reattach path | Removable once older clients age out |

## 11. Verifying these claims

```bash
# Session encryption and key generation
cat internal/crypto/box.go

# PIN-authenticated key exchange, with its own security analysis in comments
cat internal/crypto/kex.go

# Owner-device authentication
cat internal/crypto/owner.go

# Why the relay deliberately cannot verify the PIN
cat internal/relay/auth.go

# Everything the relay stores, and the 10-minute deleteAll() alarm
grep -n "storage\|alarm" cloudflare/src/session.ts

# No logging in the relay
grep -rn "console\." cloudflare/src        # no output

# No telemetry in the client
grep -rIn "analytics\|telemetry\|posthog\|sentry\|mixpanel" internal/ cmd/   # no output
```
