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
| PIN | 6 digits (`internal/session/pin.go`) | 10⁶ (~20 bits) | Until the session ends or `reminal repin` | Memory; sealed on disk for restore and `reminal info` (§6.2) |
| Device owner key | Ed25519 | 128-bit security | Until revoked | `~/.reminal/device_ed25519.sealed`, encrypted (§6.2) |
| Machine identity key | Ed25519 | 128-bit security | Until re-provisioned | Host key store, mode 0600 |
| Session key | 256-bit random (`internal/crypto/box.go`) | 256 bits | One agent run | Memory only, both ends |

Session IDs and PINs are generated with `crypto/rand`, never a seeded PRNG. So that
a session can come back as itself after its machine restarts, its ID and PIN are
kept on disk, sealed (§6.2), until the session is ended on purpose. The session
key is never written anywhere.

The session ID and PIN are **separate factors on purpose**. The session ID is the
relay's routing key and is therefore necessarily known to the relay; the PIN never
reaches the relay in any form it can use. Compromising one yields nothing.

## 4. Key establishment

A fresh random 256-bit AES-256-GCM session key is minted per agent run
(`crypto.NewSessionKey`). Every viewer, however it authenticates, ends up holding
that same key, so the agent encrypts the PTY stream once regardless of viewer count.
The key is delivered by one of two handshakes.

### 4.1 PIN path — CPace

Implemented in `internal/crypto/cpace.go` (and mirrored in the web viewer).
CPace is a balanced password-authenticated key exchange
(draft-irtf-cfrg-cpace), run here over the ristretto255 group.

1. Both sides derive the same generator from what they share:
   `G = hash_to_group(PIN, channel, sid)`, where `sid` binds the session ID and a
   fresh 16-byte per-handshake `ex_id`.
2. Each picks a fresh random scalar per handshake and sends `Y = y·G`.
3. Both compute the shared element `y·Y_peer`, rejecting a peer element that does
   not decode or is the identity, and derive a key from it together with the
   whole transcript (channel, sid, both elements in order).
4. The wrap key is `HKDF-SHA256(IKM = that key, salt = ex_id, info = "reminal-wrap-v2")`.
5. The agent wraps the session key under it with AES-256-GCM; the viewer
   unwraps. **A successful unwrap is the proof that both sides used the same PIN.**

The PIN never masks or encrypts anything that goes over the wire: it only selects
the generator. The elements exchanged are uniformly distributed group elements
for every PIN.

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
  first connect** (trust-on-first-use). Trust is held per machine
  (`~/.reminal/owned_machines.json`; the browser keeps the same list), so a machine
  the device has connected to before is recognised on any of its sessions. A
  session that one of the device's machines reports, answered by a different key,
  is refused. A key the device has not seen before is used only after the person
  confirms it, showing the machine's id. `~/.reminal/known_machines.json` still
  records which machine each session was on, and a session answered by a key other
  than that one is refused.

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
(`internal/crypto/box.go`) before they reach the relay.

When both ends support it (agent and viewer 3.15.7 or later), each message is
sealed as a frame (`internal/crypto/frames.go`): one key per direction, derived
from the session key, and the message's type, its sender's stream, its position in
that stream, and its scrollback sequence number bound in as associated data. Each
handshake gives the viewer its own stream and tells it where the agent's counter
stands, encrypted under the handshake's key. A frame is accepted once, in order,
in the direction and as the type it was sent. An end that predates frames is
answered in the earlier form; while such a viewer is attached the agent writes both
forms, and does not take its own earlier-form messages back as input. The relay sees:

- The session ID (it needs it to route).
- Frame sizes and timing.
- Connection metadata: when an agent attached, how many viewers, when they left.
- Ciphertext.

It does **not** see terminal output, keystrokes, screen contents, filenames, window
titles, or the session key. It cannot obtain the key: it never transits the relay
in usable form, and the relay performs no PIN verification of its own — by design.
The comment in `internal/relay/auth.go` is explicit about why: a relay that could
check a 6-digit PIN would be able to brute-force it offline and, worse, would be
able to take part in the exchange as either side. So it deliberately
holds no capability it does not need.

**WebRTC.** When a direct peer-to-peer path is negotiated, the signaling (SDP, ICE)
travels *inside* the already-encrypted session channel. The relay therefore cannot
tamper with DTLS fingerprints, which closes the usual signaling-server MITM window.
Media and data frames on the DataChannel are DTLS-protected end-to-end.

### 5.2 Copy/paste rendezvous — end-to-end encrypted

`reminal copy` → `reminal paste` is brokered by a separate blind Durable Object
(`cloudflare/src/rendezvous.ts`) that pairs two sockets and relays frames
verbatim. A code is ten characters in two halves (`ABCDE-FGHJK`): the relay is
given the first five, which is only where the two ends meet; the second five
never leave the two machines. The whole code is the secret for a CPace handshake
(§4.1) that runs end-to-end through the relay, so the relay learns neither the
transfer key, the filename, nor the bytes, and cannot complete a transfer itself
without guessing the half it was never given.

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
| `~/.reminal/device_ed25519.sealed` | This device's owner key, encrypted under the at-rest key below | 0600 |
| `~/.reminal/device_ed25519` | A one-line placeholder once the key is encrypted, so an older version run again stops instead of making a second identity | 0600 |
| `~/.reminal/owned_machines.json` | Machines this device trusts as an owner | 0600 |
| `~/.reminal/known_machines.json` | Which machine each session was on | 0600 |
| `~/.reminal/revoked_owners.json` | Revocation tombstones | Agent-writable |
| `/etc/reminal/owners.json` | Authorised owner devices | Root-owned |

Key files are written atomically at 0600, and a present-but-corrupt key is surfaced
as an error rather than silently regenerated — silent regeneration would swap the
machine's identity and orphan every trust relationship pinned against it.

**Saved sessions.** A session's details are kept on disk so it can come back as
itself after a restart, and so `reminal info` can show its PIN from any terminal.
Everything below is sealed with AES-256-GCM under one random 32-byte key per user,
and each file is bound to its kind and session ID, so one session's file never
opens as another's (`internal/atrest`).

| Path | Contents | Kept |
|---|---|---|
| `~/.reminal/restore/<id>.sealed` | Session ID, PIN, relay token, name, folder, the coding agent running and its conversation ID | Until the session is ended on purpose (`reminal kill`, `reminal stop`, its shell exiting) |
| `~/.reminal/restore/<id>.scrollback.sealed` | The session's terminal history, so a restored session shows what came before | Same as the record; rewritten as output arrives |
| `~/.reminal/restore/<id>.conv` | The coding agent's conversation ID (not sealed; not a credential) | Same as the record |
| `~/.reminal/active-<id>.json` | A running session's ID, name, folder and viewer counts, with the PIN sealed | While the session runs; left behind by a crash until the next `reminal list` |
| `~/.reminal/scrollback-<id>.json` | Terminal history handed from one process to the next during a hot restart or upgrade | Seconds: read once and deleted by the new process. Sealed under a one-time key passed to that process directly, never written to disk; any copy a crash leaves behind is removed at the next start |
| `~/.reminal/restore/quarantine/` | Saved sessions that could not be opened (their key was gone, or the file was damaged), with a note saying why | 7 days, then removed; `reminal doctor` mentions them |

Where the key lives:

| OS | Key storage | Notes |
|---|---|---|
| macOS | Login Keychain, item `reminal-at-rest-key` | Written and read with `/usr/bin/security`, with the key passed on its stdin, not its argv. Builds do not use cgo, so the item's access list trusts the `security` tool, not the reminal app: any program running as the same user can read it without a prompt. This is no weaker than a 0600 file, and the key stays out of backups and disk images. The login Keychain is a file-based keychain and is not synced to iCloud. |
| Windows | DPAPI, current user (`~/.reminal/atrest.key.dpapi`) | Opens only for the same Windows account. The background daemon runs as that user. |
| Linux desktop | Secret Service (GNOME Keyring, KWallet) via `secret-tool` | Used when `secret-tool` is installed and a session bus is reachable. |
| Linux without a keyring, containers | `~/.reminal/atrest.key`, mode 0600 | Same protection as the plain files it replaces: the contents are not greppable, but anyone who can read `~/.reminal` can read the key. |

`~/.reminal/atrest.json` records which of these holds the key. A run with `HOME`
pointed somewhere other than the user's real home never touches the OS keystore at
all, not even to read, so a test can never reach the person's own keychain.
`REMINAL_KEYSTORE=file` keeps a new key out of the OS keystore; a key already kept
there is still used.

The key `atrest.json` names is the one every running session and the daemon
save with. If its store loses it (the key file deleted, a keychain entry removed)
while they run, that is damage, not a new key: nothing sealed under it is treated
as gone, saving pauses rather than minting a replacement, any process that still
holds the key in memory writes it back on its next save, and `reminal doctor` says
so plainly (`reminal doctor --repair-key` asks a running session for the key and
puts it back). Only a key `atrest.json` does not name counts as gone.

Every keystore call has a 3-second limit; one that does not answer in time is
treated as locked. A locked keystore never stops a session from starting and never
costs a save: while it cannot be reached, details are sealed at once with the key
file instead, and once the keystore answers again that key is moved into it (under
its own entry, so everything sealed with it still opens) and the file is deleted.
Restoring after a reboot waits for records it cannot open yet and tries again every
30 seconds, so sessions come back once the keystore unlocks.

Each sealed file names the key it was sealed with, and is opened from whichever
store holds that key. A key counts as gone only when every store that could hold it
answered and none did, and an OS keystore's "not found" is believed only after a
throwaway check entry can be written and read back (and is then deleted), since a
locked keyring or a keychain outside this login session can say the same. A damaged
`atrest.json` or a key file that is present but unreadable counts as locked, never
as gone, and an existing key or key file is never written over. Even when a key is
gone, the files it sealed are moved to the quarantine, not deleted. Key and record
files are flushed to disk before they replace anything, and records are written
under a per-session lock so two processes converting the same record cannot lose
it. Deleting a file is a plain delete: on SSDs and copy-on-write file systems
overwriting in place does not reach the old blocks, so reminal does not pretend to.

Files written by versions before 3.15.11 held the PIN, the relay token and the
terminal history in the clear (mode 0600). The first start of 3.15.11 or later
seals them and deletes the plain copies. An older version that is run again cannot
read the sealed files, so those sessions start fresh instead of being restored.

**Where else a PIN is, at runtime.** In the memory of the session's own process,
and in the memory of a viewer that typed it. Older versions also put it in the
session shell's environment (`REMINAL_SESSION_PIN`); from 3.15.11 that variable is
no longer set, so programs started inside a session do not inherit it. Anything
running as the same user can read a process's memory and its saved files, sealed or
not: sealing protects copies of the disk, not a live account.

**Long-lived keys.** This device's owner key is the one file that grants standing
access: it opens every machine the device owns until revoked (`reminal owners
revoke`). Since 3.15.12 it is kept encrypted under the same per-user key as saved
sessions, so on macOS and Windows a copy of `~/.reminal` cannot be used to act as
the owner; on a Linux machine without a keyring the key that seals it sits beside
it, and `~/.reminal` deserves the care `~/.ssh` gets. An identity key has stricter
rules than a saved session: when it cannot be opened (the keychain is locked over
SSH, or its key is gone) owner commands stop with a message, and reminal never
replaces it on its own, since a new identity would make the device a stranger to
every machine it owns. Only `reminal own reset` makes a new one. The key a 3.15.11
or earlier version wrote in the clear is encrypted at its first use by a newer
version, and the old file is left holding a placeholder line that older versions
refuse to parse, so a downgrade cannot quietly mint a second identity either. If
both files end up holding different valid keys (an older version made one after a
downgrade, or one was restored from a backup), reminal changes neither and says so.

Two consequences are new relative to 3.15.11. Because the key that seals the owner
key lives in the Keychain or in DPAPI, copying `~/.reminal` to another machine, or
resetting the login Keychain or the Windows profile, no longer carries the owner
identity with it: the device gets a new one with `reminal own reset` and is enrolled
again on each machine. And the protection holds once that sealing key is in the OS
keystore: a first run over SSH, with the keychain locked, leaves the fallback key
file beside the sealed owner key until the keychain answers and the key is moved in.

The machine's identity key (`machine_ed25519`) stays a 0600 file, like an SSH host
key: it proves which machine a device is talking to and opens nothing, the
background daemon needs it before any keychain is unlocked (a Linux desktop after a
reboot, a headless Mac), and a machine identity that changes is treated as an
attack by every device that pinned it.

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
cat internal/crypto/cpace.go internal/crypto/kex.go

# Owner-device authentication
cat internal/crypto/owner.go

# Why the relay deliberately cannot verify the PIN
cat internal/relay/auth.go

# Everything the relay stores, and the 10-minute deleteAll() alarm
grep -n "storage\|alarm" cloudflare/src/session.ts

# No logging in the relay
grep -rn "console\." cloudflare/src        # no output

# No telemetry in the client
# (testdata excluded: other tools' captured --help text, read only by tests)
grep -rIn --exclude-dir=testdata "analytics\|telemetry\|posthog\|sentry\|mixpanel" internal/ cmd/   # no output
```
