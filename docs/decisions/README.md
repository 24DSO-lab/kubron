# Security and implementation decisions

Decisions in this directory record security-sensitive implementation choices that require explicit human review.
Each decision gets a file `<date>-<area>.md` with a status and waits for human review. Format: problem, options,
decision, consequences, rollback.

- [AKS credential provider](2026-11-04-credential-aks.md) — Azure device-code, service-principal and ROPC flows; status: ждёт ревью человеком.
- [GKE credential provider](2026-11-04-gke.md) — sign-in with a service account key (JWT-bearer grant, ticket B.7b, ADR-0018); status: ждёт ревью человеком.
- [Credential answer attempt limits](2026-11-04-credential-attempt-limits.md) — per-user and per-IdP failure windows; status: ждёт ревью человеком.
- [OIDC web browser callback state binding](2026-11-04-oidc-browser-callback.md) — session-bound OAuth state, shared challenge lookup and one-time callback; status: ждёт ревью человеком.

- [AWS EKS credential provider](2026-11-05-eks.md) — SigV4 token, access keys, AssumeRole and IAM Identity Center device flow (ticket B.7a, ADR-0018); status: ждёт ревью человеком.

| 2026-11-05 | GKE, вход пользователя (B.7b, п. 5) | [2026-11-05-gke-user.md](2026-11-05-gke-user.md) | ждёт ревью человеком |
| 2026-11-05 | AWS EKS: токен SigV4, ключи, AssumeRole, IAM Identity Center (B.7a) | [2026-11-05-eks.md](2026-11-05-eks.md) |
| 2026-11-05 | GKE, десктоп: слушатель и настройка клиента | [2026-11-05-gke-desktop.md](2026-11-05-gke-desktop.md) | ждёт ревью человеком |

