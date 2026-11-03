# Security & access control

## Local users & roles

Built-in roles: `admin`, `operator`, `user`. The `user` role is read-only: it does not have `secrets.read`, so Secret values, Helm release content (values, manifest, revision diff) and certificate expiry are not available to it; `operator` and `admin` have this permission.

Permissions are fine-grained: read, secrets, operate, write, exec, port-forward, cluster and user management, audit.

## Safe defaults

- PBKDF2-SHA256 password hashing (600k iterations)
- Lockout after failed attempts
- Auto-logout on inactivity (closes terminals and port-forwards)

## Audit log

Tracks who did what, with rotation.

## Extension points

Public `pkg/ext` API for external modules (e.g. SSO/OIDC, custom storage).

SSO/OIDC for organizations is planned as a commercial add-on. The open core always keeps local users, roles, audit log and emergency admin access.

## Privacy

No telemetry. No mandatory account. Kubeconfig never leaves your machine in desktop mode.
