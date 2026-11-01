# Configuration

Important environment variables (non-exhaustive):

| Variable | Purpose |
|---|---|
| `KUBRON_SECRET_KEY` | Encryption key for kubeconfigs and GitOps tokens at rest |
| `KUBRON_ALLOW_REMOTE` | Set to `1` to allow non-loopback listen (still put TLS proxy in front) |
| Database URL | PostgreSQL 14+ connection string (when not using local file storage) |

See the Helm values and `docs/deploy.md` in the repository for the full list.
