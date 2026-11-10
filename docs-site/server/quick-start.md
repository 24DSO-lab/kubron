# Web quick start (local)

This page gets the Web edition (`kubron-server`) running on **your own machine** in a few minutes, so you can try the web interface. It is a trial setup, not a production deployment. For a team installation behind TLS, go to [Docker and Kubernetes deployment](/server/deploy).

Choose one of two paths:

| Path | Best for | Needs |
|---|---|---|
| [A. Single user, from source](#a-single-user-from-source) | The fastest look at the interface | Go, Node.js and the Wails CLI (see [Build from source](/guide/build-from-source#prerequisites)) |
| [B. Multi-user trial with Docker Compose](#b-multi-user-trial-with-docker-compose) | Seeing the real server setup with PostgreSQL | Docker with the Compose plugin and `openssl` |

## A. Single user, from source

In this mode the server listens only on your own computer (`127.0.0.1`) and keeps its data in files under `~/.kubron`. No database and no encryption key are required. It is meant for one person.

1. Build the server as described in [Build from source → Server binary](/guide/build-from-source#server-binary).
2. Start it:

   ```bash
   ./kubron-server --listen 127.0.0.1:8080
   ```

3. Open <http://127.0.0.1:8080> in your browser.
4. On the **Welcome to Kubron** screen create the administrator (username 3 to 32 characters, password of at least 8). On a loopback address no setup token is needed.
5. Continue with [add your first cluster](#add-your-first-cluster).

Stop the server with `Ctrl+C`. To reset, delete `~/.kubron`.

::: warning
Do not change `--listen` to a public address in this mode. The server refuses to start on a non-loopback address unless you also pass `--allow-remote` (or set `KUBRON_ALLOW_REMOTE=1`), because it does not serve TLS and the session cookie would travel in clear text.
:::

## B. Multi-user trial with Docker Compose

This starts PostgreSQL and the published Kubron image together, exactly like a real deployment except for TLS. Everything stays bound to `127.0.0.1`.

### 1. Create the secrets

In an empty folder, generate the two secrets and keep them in an `.env` file:

```bash
cat > .env <<EOF2
KUBRON_SECRET_KEY=k1:$(openssl rand -base64 32)
KUBRON_SETUP_TOKEN=$(openssl rand -hex 16)
POSTGRES_PASSWORD=$(openssl rand -hex 16)
EOF2
chmod 600 .env
```

- `KUBRON_SECRET_KEY` encrypts kubeconfigs stored in the database. Its format is `id:base64`, where the base64 part is exactly 32 random bytes. For a trial, keep it in `.env`. **In production, store it in a secret manager and back it up**: without it, stored credentials cannot be recovered.
- `KUBRON_SETUP_TOKEN` (at least 16 characters) protects the creation of the first administrator, so nobody else can claim a freshly started server.

### 2. Create `compose.yaml`

```yaml
services:
  db:
    image: postgres:16
    environment:
      POSTGRES_USER: kubron
      POSTGRES_DB: kubron
      POSTGRES_PASSWORD: ${POSTGRES_PASSWORD:?run the .env step first}
    volumes:
      - pgdata:/var/lib/postgresql/data
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U kubron -d kubron"]
      interval: 5s
      timeout: 3s
      retries: 10

  kubron:
    image: ghcr.io/24dso-lab/kubron:1.2.0
    depends_on:
      db:
        condition: service_healthy
    ports:
      - "127.0.0.1:8080:8080"
    environment:
      KUBRON_ALLOW_REMOTE: "1"
      KUBRON_SECRET_KEY: ${KUBRON_SECRET_KEY:?run the .env step first}
      KUBRON_SETUP_TOKEN: ${KUBRON_SETUP_TOKEN:?run the .env step first}
      KUBRON_DATABASE_URL: postgres://kubron:${POSTGRES_PASSWORD}@db:5432/kubron?sslmode=disable
    read_only: true
    tmpfs:
      - /home/nonroot:uid=65532,gid=65532
      - /tmp
    cap_drop: [ALL]
    security_opt:
      - no-new-privileges:true

volumes:
  pgdata:
```

Replace `1.2.0` with the version you want (the image tag has no leading `v`).

A few details worth knowing:

- `KUBRON_ALLOW_REMOTE=1` is needed because the container listens on `0.0.0.0` inside its own network. The port is still published only to `127.0.0.1` on your machine.
- `sslmode=disable` is acceptable here only because PostgreSQL is on the private Compose network. In production use `sslmode=verify-full`.
- `read_only` plus the two `tmpfs` mounts follow the image's hardening: the process writes only to `/home/nonroot` and `/tmp`.

### 3. Start it

```bash
docker compose up -d
docker compose logs -f kubron
```

Wait until the log shows the server listening. Check readiness:

```bash
curl -fsS http://127.0.0.1:8080/readyz && echo
```

### 4. Create the administrator

1. Open <http://127.0.0.1:8080>.
2. On the **Welcome to Kubron** screen enter the **setup token** from your `.env` file, then the administrator **username** and **password**.
3. Click **Create administrator**.

You can also create the administrator from the command line, which does not need the token. The password is read from standard input, never from an argument:

```bash
printf '%s\n' 'a-long-passphrase' | docker compose exec -T kubron /kubron-server create-admin --username admin
```

### 5. Stop or reset

```bash
docker compose down         # stop, keep the data
docker compose down -v      # stop and delete the database volume
```

## Add your first cluster

In the Web edition the kubeconfig is uploaded to the server, and the **server** connects to your cluster. The server cannot read files from your computer and **never runs commands** from an uploaded kubeconfig. Kubron detects the credential type and, when sign-in is needed, opens a dialog.

| Credential in the kubeconfig | What happens on the server |
|---|---|
| Bearer token, basic credentials, client certificate and key embedded as `*-data` | Used as is |
| OIDC (`oidc-login`: password, authorization code, device code) | Sign-in dialog. The identity provider must use HTTPS and be reachable from the server |
| Azure AKS (`kubelogin`: `devicecode`, `spn`, `ropc`) | Kubron signs in itself |
| AWS EKS (`aws eks get-token`, `aws-iam-authenticator`) | Kubron issues the token from access keys or IAM Identity Center |
| Google GKE (`gke-gcloud-auth-plugin`) | Service-account key, or a Google account if the administrator configured an OAuth client |
| Any other `exec` plugin | Kubron shows the exact command; you run it on **your** computer and paste the token or `ExecCredential` JSON |
| Paths to files on the server (`certificate-authority`, `client-key`, `tokenFile`) | Not available. Embed the data instead |

Cluster addresses are checked by the server's address policy: loopback, link-local and cloud-metadata addresses are blocked by default. A kind or minikube cluster on `127.0.0.1` is unreachable from inside a container anyway; point the kubeconfig at an address the server can reach. All details, including shared team accounts, are in [Connecting to Kubernetes](/features/cluster-auth#web).

Then:

1. Sign in and add a cluster from the cluster list.
2. Paste the kubeconfig text or pick the file, choose the context, and test the connection.
3. Complete the sign-in dialog if one appears, then save. The cluster opens in the dashboard.

See [Add your first cluster](/guide/first-cluster) for the dialog in detail.

## Troubleshooting

| Symptom | Cause and fix |
|---|---|
| `address ... is not loopback` and the server exits | Add `KUBRON_ALLOW_REMOTE=1` (container) or `--allow-remote`, and put a TLS proxy in front |
| `KUBRON_SECRET_KEY is required when a database is used` | Set the encryption key whenever `KUBRON_DATABASE_URL` is set |
| The server refuses to start with a different key | The key must be the one that encrypted the existing data. Restore the original key, or reset with `docker compose down -v` in a trial |
| `the setup token is missing or invalid` on the welcome screen | The token must match `KUBRON_SETUP_TOKEN` exactly. The variable itself must be at least 16 characters, otherwise the server will not start |
| Adding a cluster fails because of its address | The address is blocked by the cluster address policy (loopback, link-local, cloud metadata). See the [configuration reference](/server/configuration) |
| `docker compose` cannot find the image | Check the tag exists on the [packages page](https://github.com/orgs/24DSO-lab/packages) |

## Next steps

- [Production deployment with Docker and Helm](/server/deploy)
- [Configuration reference](/server/configuration)
