# Command reference

`kubron-server` is the binary of the Web edition. The container image runs it as the entry point, so inside the image the path is `/kubron-server`. The image has no shell: run commands directly, for example `kubectl exec deploy/kubron -- /kubron-server rekey`, or in a one-off Job with the same environment.

[[toc]]

## Starting the server

Without a subcommand the binary starts the server. Settings come from environment variables and flags; see the [configuration reference](/server/configuration). Run `kubron-server --help` for the list of flags compiled into your version.

```bash
kubron-server --listen 127.0.0.1:8080
```

### Flags

Every flag below has an environment variable of the same meaning (`--listen` is `KUBRON_LISTEN`, and so on). Secrets and tokens such as `KUBRON_SECRET_KEY` and `KUBRON_SETUP_TOKEN` are **environment-only** on purpose, because command-line arguments are visible in the process list.

| Flag | Purpose |
|---|---|
| `--listen` | HTTP listen address |
| `--allow-remote` | Allow a non-loopback address |
| `--data-dir` | Data directory for file storage (default `~/.kubron`) |
| `--database-url` | PostgreSQL connection string. Prefer the environment variable |
| `--trust-proxy`, `--trusted-proxy-hops`, `--trusted-proxies` | Trust a reverse proxy for the client address and scheme |
| `--csp-report-only` | Send the Content-Security-Policy as report-only |
| `--session-ttl` | Web session lifetime |
| `--metrics-listen` | Address of the Prometheus listener |
| `--replica-id` | Unique replica identifier |
| `--drain-delay` | Pause between SIGTERM and closing connections |
| `--audit-retention` | How long audit entries are kept in PostgreSQL |
| `--cluster-check-interval` | How often cluster connectivity is checked |
| `--max-terminals`, `--max-log-streams`, `--max-port-forwards` | Per-user limits (`0` is unlimited) |
| `--disable-exec`, `--disable-node-shell`, `--disable-port-forward` | Turn interactive features off for everyone |
| `--terminal-max-duration`, `--terminal-idle-timeout` | Terminal lifetime limits |

### Endpoints

| Path | Purpose |
|---|---|
| `/healthz` | Liveness: `200` while the process runs |
| `/readyz` | Readiness: checks the database and returns `503` while the replica is draining |
| `/metrics` | Prometheus metrics, on the separate metrics port only (see [Logging, audit and metrics](/server/observability)) |

## Administrative commands

Three subcommands work directly with the database. They read their settings from the environment (`KUBRON_DATABASE_URL`, `KUBRON_SECRET_KEY`, `KUBRON_SECRET_KEY_PREVIOUS`) and stop with an error if `KUBRON_DATABASE_URL` is not set.

### `create-admin`

Creates the first administrator directly in the database, bypassing the network and the setup token.

```bash
printf '%s\n' 'a-long-passphrase' | kubron-server create-admin --username admin
```

- `--username` is required.
- The password is read from `KUBRON_ADMIN_PASSWORD`, or from the first line of standard input. It is **never** accepted as an argument, because arguments are visible in the process list and shell history; a stray argument is rejected.
- The username and password are validated exactly as in the web setup screen (username 3 to 32 characters; password 8 to 256 bytes).
- It refuses to run if users already exist.
- The creation is recorded in the audit log as `Setup` with the source `cli`.

On Kubernetes:

```bash
kubectl -n kubron exec -i deploy/kubron -- \
  /kubron-server create-admin --username admin < password.txt
```

### `rekey`

Re-encrypts stored secrets with the current `KUBRON_SECRET_KEY`. Used after [key rotation](/server/backup#rotate-the-key-and-rekey).

```bash
kubron-server rekey
# re-encrypted with key "2026-11": 12 kubeconfigs, 3 cluster secrets, 2 Argo CD tokens, 4 Helm repository secrets, 5 sign-in credentials (0 still in legacy format)
```

It is safe to repeat: records that are already re-encrypted are skipped.

### `import-files`

Imports the file storage of a single-user or Desktop installation into PostgreSQL, for example when you move from a personal setup to a shared server.

```bash
kubron-server import-files --from ~/.kubron
# imported from /home/alex/.kubron: 2 users, 5 clusters, 1 Argo CD tokens, 340 audit events
# source files are untouched and can serve as a backup
```

- `--from` is the data directory containing `users.json`, `clusters/` and `audit.log`. It defaults to `~/.kubron`.
- The command prints a summary of what it imported, plus any notes about records it could not carry over.
- The source files are not modified, so they remain a backup.
- Imported kubeconfigs are stored encrypted with your `KUBRON_SECRET_KEY`. Remember that the Web edition applies stricter rules to kubeconfig credentials than Desktop; see [Connecting to Kubernetes](/features/cluster-auth#web).

## Exit and signals

`SIGTERM` starts a graceful shutdown: see [High availability](/server/ha#stopping-a-replica-sigterm). Administrative commands print their result to standard output and exit with a non-zero status on error.
