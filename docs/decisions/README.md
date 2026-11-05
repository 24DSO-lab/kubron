# Security and implementation decisions

Decisions in this directory record security-sensitive implementation choices that require explicit human review.

- [AKS credential provider](2026-11-04-credential-aks.md) — Azure device-code, service-principal and ROPC flows; status: ждёт ревью человеком.
- [Credential answer attempt limits](2026-11-04-credential-attempt-limits.md) — per-user and per-IdP failure windows; status: ждёт ревью человеком.
- [OIDC web browser callback state binding](2026-11-04-oidc-browser-callback.md) — session-bound OAuth state, shared challenge lookup and one-time callback; status: ждёт ревью человеком.
