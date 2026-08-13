# Security Self-Assessment

**Status:** current as of the version in this repository.
**Format:** structured after the CSA Consensus Assessments Initiative Questionnaire
(CAIQ) domains, so it can be mapped onto most vendor security reviews.

---

## How to read this document

This is a **self-assessment**, not an audited attestation. Nothing here has been
verified by a third party.

Two framing points determine whether most questionnaire items apply at all:

1. **reminal is not a hosted service.** It is software you run on your own machine.
   There is no account system, no user database, no tenancy, and no customer data
   held by the project. Many questionnaire items assume a SaaS vendor holding
   customer data, and resolve here to "no such data exists" rather than to a
   control.
2. **The relay is untrusted by design and holds no session content.** Questions
   about protecting data at rest on the vendor's infrastructure likewise resolve to
   the absence of the exposure rather than to a compensating control.

Where an answer depends on architecture, it links to the
[security architecture](architecture.md) or [threat model](threat-model.md).

**Answer values:** *Yes* · *Partial* (scope stated) · *Not built* / *Not held*
(with roadmap where one exists) · *N/A* (with the reason it does not apply).

---

## Certifications and attestations

| # | Question | Answer |
|---|---|---|
| A.1 | SOC 2 Type I or II report | Not held |
| A.2 | ISO/IEC 27001 certification | Not held |
| A.3 | Independent penetration test in the last 12 months | Not performed |
| A.4 | Independent cryptographic review | Not performed |
| A.5 | PCI DSS / HIPAA / FedRAMP | Not held; none in scope |
| A.6 | Compliance roadmap | Sequenced: independent cryptographic review first, as it speaks directly to the properties the product rests on; then release signing and audit logging; then formal attestation if enterprise demand justifies it |

**Context.** reminal is maintained by an individual developer, and formal
attestation certifies organizational process rather than the cryptographic
properties this product actually rests on. The assurance available today is
different in kind and available immediately: **the entire system — client and
relay — is open source and independently auditable**, and the relay can be
self-hosted so that no third party sits in the trust chain at all.

## Application and interface security

| # | Question | Answer |
|---|---|---|
| AIS.1 | Secure development practices | Yes — code review on all changes, with security-relevant design decisions documented inline at the point of implementation. No separate written SDLC policy |
| AIS.2 | Automated testing | Yes — unit and integration tests, including dedicated key-exchange and owner-authentication tests, plus fuzz targets over untrusted cryptographic input and terminal/frame decoding |
| AIS.3 | Input validation on untrusted input | Yes — relay-supplied values are length-bounded and validated before use; WebSocket read limits and read deadlines bound resource consumption |
| AIS.4 | Dependency vulnerability scanning | Partial — GitHub Dependabot alerts; no SCA gate in CI |
| AIS.5 | Static analysis | Partial — `go vet` and race-detector runs in CI; no dedicated SAST tool |
| AIS.6 | Third-party libraries minimized | Yes — standard library plus a small dependency set. Cryptography is Go stdlib (`crypto/ecdh`, `crypto/aes`, `crypto/ed25519`) plus `golang.org/x/crypto/hkdf`. No custom primitives |

## Audit assurance and compliance

| # | Question | Answer |
|---|---|---|
| AAC.1 | Audit logging of user access | Not built — reminal records no connection history. This follows from the no-logging design that also means there is no data to disclose or seize; connection audit logging is on the roadmap for enterprise deployment |
| AAC.2 | Logs tamper-evident | N/A — no logs are produced |
| AAC.3 | Customer access to audit logs | N/A |
| AAC.4 | Independent audit of controls | Not performed |

## Business continuity and operational resilience

| # | Question | Answer |
|---|---|---|
| BCR.1 | Documented BC/DR plan | Not held as a formal document |
| BCR.2 | Availability SLA | Not offered — the public relay is best-effort. Self-hosting places availability under the operator's own control |
| BCR.3 | Data backup and restore | N/A — no persistent customer data exists to back up |
| BCR.4 | Resilience to vendor failure | Yes, structurally. The relay is open source and self-hostable, so no operator is dependent on the project's availability to reach their own machines. Sessions also negotiate direct peer-to-peer paths where the network allows |
| BCR.5 | Continuity of maintenance | reminal is maintained by a single developer. Because the entire system — client and relay — is AGPL source with no proprietary server component, continuity does not depend on the project: an operator can build, deploy, and maintain all of it independently |

## Change control and configuration management

| # | Question | Answer |
|---|---|---|
| CCC.1 | Version control with history | Yes — public Git |
| CCC.2 | Changes reviewed before release | Yes |
| CCC.3 | Releases reproducible from source | Yes — tagged commits built in GitHub Actions |
| CCC.4 | Rollback capability | Yes — atomic install with automatic rollback on failure |
| CCC.5 | Emergency patch mechanism | Yes — a `critical_min` version floor served by the relay upgrades all clients within 24 hours |
| CCC.6 | Release artifacts signed and verified | Partial — macOS builds are code-signed, and archives are served over TLS from GitHub. Client-side signature verification of the downloaded archive, and Apple notarization, are on the roadmap; see [limitations](threat-model.md#current-limitations-and-roadmap) |

## Data security and information lifecycle

| # | Question | Answer |
|---|---|---|
| DSI.1 | Customer data inventory | Yes — see [subprocessors](subprocessors.md). No account data exists; session content never reaches the relay |
| DSI.2 | Data classified by sensitivity | Yes — session content (never leaves the end-to-end channel) and routing metadata (relay-visible, ≤10 minutes) |
| DSI.3 | Data retention policy | Yes — routing metadata is destroyed ≤10 minutes after agent disconnect via `deleteAll()`; rendezvous rooms are capped at 1 hour. **Enforced by the runtime, not by policy** |
| DSI.4 | Secure deletion | Yes — as above. There is no backup or archival tier from which data could be recovered |
| DSI.5 | Data residency controls | Partial — Cloudflare edge placement is not pinned. This applies only to routing metadata with a ten-minute lifetime; self-hosting places all of it under the operator's control |
| DSI.6 | Production data used in testing | N/A — the project holds no production data |
| DSI.7 | Data portability and export | N/A — nothing is held that would require export, and there is no lock-in |

Several rows above are *N/A* because reminal's architecture removes the exposure
rather than because the question was skipped: there is no data at rest to encrypt,
no customer dataset to back up, and no production data to keep out of testing.

## Encryption and key management

Full detail in [architecture §4](architecture.md#4-key-establishment).

| # | Question | Answer |
|---|---|---|
| EKM.1 | Data encrypted in transit | Yes — WSS/TLS on every hop, with end-to-end AES-256-GCM beneath it. WebRTC data is DTLS-protected |
| EKM.2 | Data encrypted at rest | N/A — no session data is written to disk on the host or stored on the relay at any point, so there is no data at rest to protect |
| EKM.3 | End-to-end encryption | Yes for terminal, window, desktop, and copy/paste sessions. `reminal expose` is the exception: the visitor is an ordinary browser holding no reminal key, so port-forwarded traffic transits the relay in plaintext ([architecture §5.3](architecture.md#53-reminal-expose--not-end-to-end-encrypted)). Use a self-hosted relay for sensitive services |
| EKM.4 | Algorithms and key sizes | Yes — AES-256-GCM, X25519, Ed25519, HKDF-SHA256. Standard primitives, no custom cryptography |
| EKM.5 | Key generation | Yes — `crypto/rand` (CSPRNG) throughout; a fresh 256-bit session key per agent run |
| EKM.6 | Key rotation | Yes, automatic — a new session key per agent run, and fresh ephemeral X25519 keys per connection |
| EKM.7 | Forward secrecy | Yes — ephemeral keys per connection; recorded traffic remains unreadable even if the PIN is later disclosed |
| EKM.8 | Provider access to keys | None. The relay never receives the session key and cannot derive it, and it performs no PIN verification — a capability deliberately withheld, because holding it would enable the relay to man-in-the-middle the exchange (`internal/relay/auth.go`) |
| EKM.9 | Key material on disk | Yes, minimized — only long-lived Ed25519 identity keys, written atomically at mode 0600. Session keys and PINs are memory-only and destroyed on exit |
| EKM.10 | Cryptography independently reviewed | Not performed. The design and its rationale are documented inline in `internal/crypto/kex.go` and `owner.go` for reviewers who wish to evaluate it directly |

## Governance, risk, and human resources

| # | Question | Answer |
|---|---|---|
| GRM.1 | Written information security policy | These documents serve that function. No separate corporate policy set |
| GRM.2 | Named security contact | Yes — the maintainer; see [SECURITY.md](../../SECURITY.md) |
| GRM.3 | Risk assessment performed | Yes — [threat model](threat-model.md), with residual risk stated per adversary |
| GRM.4 | Background checks on personnel | N/A — single maintainer, no employees |
| GRM.5 | Security awareness training | N/A — no personnel |
| GRM.6 | Cyber liability insurance | Not held |
| GRM.7 | Legal entity, DPA, contract vehicle | No legal entity exists at present, so no vendor contract is available for signature. Organizations requiring contractual terms can deploy reminal as self-operated open-source software, under which they are their own controller and processor and no vendor DPA is required. See [subprocessors](subprocessors.md#gdpr-and-data-protection-posture) |

## Identity and access management

| # | Question | Answer |
|---|---|---|
| IAM.1 | Multi-factor authentication | Partial — two independent secrets are required (session ID and PIN), though both are bearer credentials rather than distinct authentication factors. Owner devices use public-key authentication bound to a specific device, which is the stronger path and the recommended one |
| IAM.2 | SSO / SAML / OIDC | Not built — on the roadmap for enterprise deployment |
| IAM.3 | SCIM provisioning | Not built |
| IAM.4 | Role-based access control | Not built — any authenticated viewer has full interactive control; there is no read-only or input-disabled role. Scope a session accordingly; see [adversary A4](threat-model.md#a4--malicious-viewer-holds-valid-credentials) |
| IAM.5 | Credential revocation | Yes — `Ctrl+C` destroys session credentials instantly. Owner devices are individually revocable, including self-revocation from any browser; revocation tombstones override the root-owned owner list and survive its restoration from backup |
| IAM.6 | Privilege separation for enrollment | Yes — the authorised owner list is root-owned, so enrollment requires `sudo` and an unprivileged local process cannot enroll itself |
| IAM.7 | Credential expiry | Yes — session credentials live only as long as the agent process. Owner keys persist until revoked |
| IAM.8 | Brute-force protection | Yes — an agent-side token bucket bounds PIN guessing to ~6 attempts per minute, all necessarily online; the `expose` gate additionally enforces a 5-attempt lockout with a 5-minute cooldown. Analysis: [brute-force economics](threat-model.md#brute-force-economics) |

## Infrastructure and network security

| # | Question | Answer |
|---|---|---|
| IVS.1 | Network segmentation | Yes — per-session isolation via distinct Durable Objects |
| IVS.2 | No inbound ports on the host | Yes — the agent binds no TCP port, making outbound connections only, so it presents no externally reachable attack surface. It uses local Unix domain sockets for helper IPC; a self-hosted relay binds a listener by design |
| IVS.3 | DDoS protection | Yes — inherited from Cloudflare |
| IVS.4 | Hardened runtime | Yes — Cloudflare Workers V8 isolates; no long-lived server administered by the project |
| IVS.5 | Host privilege requirements | On macOS, window capture and input injection require OS-level Screen Recording and Accessibility grants. These are consent-gated by the operating system, visible in System Settings, user-revocable at any time, and cannot be self-granted by reminal. Capture and input run in a dedicated helper rather than the main binary; see [architecture §8](architecture.md#8-host-privileges) |

## Incident response

| # | Question | Answer |
|---|---|---|
| SEF.1 | Documented vulnerability disclosure process | Yes — [SECURITY.md](../../SECURITY.md), via GitHub private advisories |
| SEF.2 | Published response timelines | Yes — 3 days to acknowledge, 10 to assess, 30 to fix critical issues. Targets rather than contractual SLAs |
| SEF.3 | Incident response plan | Partial — disclosure and patch-distribution processes are defined; no separate IR runbook |
| SEF.4 | Breach notification | Public security advisories are published on resolution. No contractual notification undertaking is available, as no legal entity exists |
| SEF.5 | Rapid patch distribution | Yes — the `critical_min` floor reaches all clients within 24 hours, which is materially faster than most self-managed remote-access tooling |
| SEF.6 | Track record | One significant cryptographic weakness has been identified and fixed: the pre-v2 key derivation was offline-brute-forceable by the relay. It was disclosed, fixed, and the full analysis retained in-source (`internal/crypto/kex.go`) rather than quietly removed. Deployments older than v2 should be treated as compromised against their relay |

## Supply chain

| # | Question | Answer |
|---|---|---|
| STA.1 | Subprocessors disclosed | Yes — [subprocessors.md](subprocessors.md): Cloudflare, GitHub, and STUN/TURN. No others |
| STA.2 | Subprocessor access to customer data | None to session content, by construction — with the `expose` exception noted at EKM.3 |
| STA.3 | Source available for audit | Yes — the entire system, client and relay, under AGPL-3.0 |
| STA.4 | Self-hosting available | Yes — the relay ships in-repo; deploying it removes Cloudflare from the trust chain entirely |
| STA.5 | SBOM | Not published as a separate artifact; `go.mod` and `go.sum` provide a complete, cryptographically verifiable dependency graph |
| STA.6 | Telemetry or analytics | None. No usage reporting, crash reporting, or third-party SDK of any kind — verifiable by grep |

## Threat and vulnerability management

| # | Question | Answer |
|---|---|---|
| TVM.1 | Vulnerability scanning | Partial — Dependabot alerts on dependencies |
| TVM.2 | Patch timelines | 30 days for critical issues, per [SECURITY.md](../../SECURITY.md) |
| TVM.3 | Threat model maintained | Yes — [threat-model.md](threat-model.md) |
| TVM.4 | Fuzzing | Yes — four Go fuzz targets covering untrusted cryptographic input and terminal/window frame decoding |
| TVM.5 | Bug bounty | Not offered. Vulnerability reports are welcomed and credited through GitHub advisories |

---

## Summary

### What reminal provides today

- **End-to-end encryption** of terminal, window, desktop, and copy/paste sessions,
  with the relay cryptographically unable to read them — and deliberately denied
  the PIN-verification capability that would let it try.
- **Forward secrecy** on every connection, so recorded traffic stays unreadable
  even if credentials are later disclosed.
- **No session content stored anywhere** — not on the relay, not on disk. Routing
  metadata is destroyed within ten minutes of disconnect by the runtime itself,
  not by policy.
- **No telemetry or analytics** of any kind.
- **No inbound network surface** on the host; the agent makes outbound connections
  only.
- **Full source availability**, client and relay both, so every claim in these
  documents is independently verifiable rather than taken on trust.
- **Self-hosting**, which removes the third-party relay from the trust chain
  entirely.
- **Rapid patch distribution** reaching all clients within 24 hours.

### What is not available today

- Third-party attestation: SOC 2, ISO 27001, an independent penetration test, or
  an independent cryptographic review. None has been performed.
- Enterprise identity and governance features: SSO/SAML, SCIM, role-based access
  control, and connection audit logging.
- Client-side signature verification of downloaded release archives, and Apple
  notarization of macOS builds.
- Contractual vehicles — DPA, breach-notification undertaking, cyber liability
  insurance — which require a legal entity that does not presently exist.
- End-to-end encryption for `reminal expose` port-forwards, which by their nature
  serve visitors holding no reminal key material.

Each of these has a stated path in
[current limitations and roadmap](threat-model.md#current-limitations-and-roadmap).

### Suggested framing for reviewers

reminal is most accurately evaluated as **open-source software an organization
deploys and operates itself**, rather than as a vendor-supplied service. Under that
framing the determining questions are architectural — what the relay can observe,
what is retained, what the cryptography guarantees — and those questions are
answerable, verifiable in source, and answered above.

Questions, or a finding that contradicts anything here:
[report it](../../SECURITY.md).
