# Security and access control

Kubron combines its own application permissions with the permissions of the Kubernetes identity used for a cluster connection. The backend checks permissions for every protected operation; hiding a button in the UI is not the security boundary.

## Built-in users and roles

Kubron has local user accounts and three built-in roles:

| Role | Permissions |
|---|---|
| `admin` | Full cluster access and privileged operations; organization-wide user/team administration and audit access are separately scoped |
| `operator` | Read, Secret-value access and safe operational actions such as scaling, restarting and rolling back workloads |
| `user` | Read-only access; no Secret values, write operations, terminals or port-forward |

Permissions are granular: resource read, `secrets.read`, `operate`, `write`, `exec`, `nodeshell`, `portforward`, cluster management, user management and audit access. For example, reading Secret values is separate from listing Secret objects.

## Permission reference

The backend checks one of these permissions for every protected operation. Roles are bundles of permissions: `user` has `read`; `operator` adds `secrets.read` and `operate`; `admin` has all of them.

| Permission | What it allows | Roles |
|---|---|---|
| `read` | Browse resources, logs, events, YAML and metrics; list Helm releases, repositories and Argo CD objects | all |
| `secrets.read` | Secret values; Helm release values, manifests and revision diffs, and upgrade previews; certificate expiry on the dashboard | `operator`, `admin` |
| `operate` | Scale, restart and roll back workloads; retry Jobs, trigger and suspend CronJobs; cordon nodes; roll back Helm releases; sync, refresh, terminate and roll back Argo CD applications | `operator`, `admin` |
| `write` | Apply, create and delete YAML; edit ConfigMaps and Secrets; evict Pods; drain nodes; install, upgrade and uninstall Helm releases; install or remove the metrics stack; suspend or resume Argo CD auto-sync and delete applications | `admin` |
| `exec` | Pod terminals | `admin` |
| `nodeshell` | Node shell (privileged host access) | `admin` |
| `portforward` | Port-forward | `admin` |
| `clusters.manage` | Add, change and remove clusters; manage Helm repositories and OCI registries | `admin` |
| `users.manage` | Manage users, roles and teams | `admin` |
| `audit.read` | View and export the audit log | `admin` |

The list covers the main actions and is not exhaustive. Kubernetes RBAC applies on top: a Kubron permission never grants more than the cluster credentials allow.

## Web team isolation

In PostgreSQL-backed Web team mode, a cluster belongs to one team and the user's role in that team determines the Kubron permissions for that cluster. An organization administrator can manage users and teams but does not automatically gain access to cluster data without team membership. Kubernetes RBAC remains an independent, additional restriction.

## Authentication and session protection

- Passwords are stored as Argon2id hashes (19 MiB of memory, 2 iterations, 1 thread, a random 16-byte salt). A password must be 8 to 256 bytes long. Hashes created by earlier versions (PBKDF2-SHA256) are still accepted and are upgraded automatically at the next successful sign-in, so no password reset is needed.
- Failed login attempts trigger a temporary lockout.
- Inactivity can end a session automatically; interactive resources such as terminals and port-forwards are closed with the session.
- Web sessions use protected cookies, CSRF validation and Origin checks. On HTTPS deployments, the session cookie is HttpOnly, Secure and SameSite=Lax.

## Secrets at rest

In PostgreSQL-backed Web service mode, kubeconfigs and sensitive Helm repository credentials are encrypted before they are stored. The server's encryption key must be protected and backed up separately from the database; losing it makes encrypted credentials unrecoverable. See [server configuration](/server/configuration) and [deployment guidance](/server/deploy).

Desktop stores kubeconfig data locally in the Kubron data directory with restrictive filesystem permissions. The kubeconfig is used by the local application and is not uploaded to a Kubron-hosted service.

## Audit history

The audit log records selected privileged and administrative actions, outcomes and actor context. Interactive session start/stop events (such as Pod terminals, node shell and port-forward) are also recorded. Ordinary read-only browsing is not intended to generate an audit record for every request.

## Server-side controls

Server operators can disable Pod exec, node shell or port-forward for all users. Web also applies network policies to cluster API and Helm repository connections to reduce SSRF risk. See [Web configuration](/server/configuration).

## Privacy and extensions

Kubron does not send product telemetry and does not require an account with a Kubron-hosted SaaS provider. It includes local user accounts for application access. The public Go extension API is available for integrations; organization SSO/OIDC is not part of the currently available built-in sign-in flow.