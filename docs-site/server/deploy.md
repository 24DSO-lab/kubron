# Docker and Kubernetes deployment

This is the step-by-step guide for running the Web edition (`kubron-server`) for a team: first with Docker, then on Kubernetes with the Helm chart. It follows the [full operations guide](https://github.com/24DSO-labs/kubron/blob/master/docs/deploy.md) in the repository, which remains the reference for advanced topics.

Want to try it on your laptop first? Use the [quick start](/server/quick-start).

[[toc]]

## Before you begin

| You need | Why |
|---|---|
| **PostgreSQL 14+** (external) | Stores users, clusters, audit log, web sessions and shared Helm state. The server applies migrations itself on startup |
| **An encryption key** | Encrypts kubeconfigs and Argo CD credentials in the database (`KUBRON_SECRET_KEY`) |
| **A TLS-terminating proxy** | The server does not serve TLS. Without TLS the session cookie would travel in clear text |
| **A DNS name** | For example `kubron.example.com` |
| **Kubernetes 1.27+ and Helm 3** | Only for the Helm installation |

Plan for **two or more replicas** in production. All state is in PostgreSQL, so replicas are interchangeable (see [high availability](https://github.com/24DSO-labs/kubron/blob/master/docs/ha.md)).

## Step 1. Prepare PostgreSQL

Create a dedicated role and database. Run as a PostgreSQL superuser:

```sql
CREATE ROLE kubron LOGIN PASSWORD 'choose-a-strong-password';
CREATE DATABASE kubron OWNER kubron;
```

The role owns the database, so it can create the tables during migrations. No extensions are required.

Build the connection string. In production use certificate verification:

```
postgres://kubron:PASSWORD@db.example.com:5432/kubron?sslmode=verify-full
```

If you reach PostgreSQL through **PgBouncer in transaction mode**, also provide a direct address in `KUBRON_DB_LISTEN_URL`, because `LISTEN/NOTIFY` does not work through a transaction-mode pooler.

::: tip Backups
Schedule PostgreSQL backups now. The key and the database must be backed up **separately** and stored in **different places**; see the [backup guide](https://github.com/24DSO-labs/kubron/blob/master/docs/backup.md).
:::

## Step 2. Generate and store the encryption key

The key is `id:base64`, where the base64 part is exactly 32 random bytes and `id` is a label (letters, digits, `_ . -`, up to 32 characters):

```bash
echo "k1:$(openssl rand -base64 32)"
```

Store the result in your password manager or KMS **before the first start**. If the key is lost, every stored kubeconfig and token is unrecoverable. Neither the image nor the chart generates it, precisely for that reason.

## Step 3a. Run with Docker

The image is distroless, runs as user `65532`, listens on port `8080` and is configured only through environment variables.

```bash
docker run -d --name kubron --restart unless-stopped \
  -p 127.0.0.1:8080:8080 \
  --read-only --tmpfs /home/nonroot:uid=65532,gid=65532 --tmpfs /tmp \
  --cap-drop ALL --security-opt no-new-privileges \
  -e KUBRON_ALLOW_REMOTE=1 \
  -e KUBRON_TRUST_PROXY=1 \
  -e KUBRON_ALLOWED_HOSTS=kubron.example.com \
  -e KUBRON_SETUP_TOKEN='a-random-token-of-16-or-more-characters' \
  -e KUBRON_SECRET_KEY='k1:...' \
  -e KUBRON_DATABASE_URL='postgres://kubron:...@db.example.com:5432/kubron?sslmode=verify-full' \
  ghcr.io/24dso-labs/kubron:1.0.0
```

What each part does:

- `KUBRON_ALLOW_REMOTE=1` is required because the container listens on `0.0.0.0`.
- Publish the port to `127.0.0.1` or an internal network and put your TLS proxy in front. Do not publish it to the internet.
- `KUBRON_TRUST_PROXY=1` makes the server trust `X-Forwarded-Proto` from the proxy and mark the cookie `Secure`. Enable it only when the server is reachable **only** through the proxy.
- `KUBRON_ALLOWED_HOSTS` lists the host names the server accepts; other `Host` headers get `421`.
- Pass secrets through the environment or your orchestrator's secrets, never as command-line flags: arguments are visible in the process list.
- Without `KUBRON_DATABASE_URL` the server falls back to file storage in a temporary directory. That is for evaluation only; the data disappears with the `tmpfs`.

Put a reverse proxy in front. A minimal **Caddy** example, which obtains the certificate automatically:

```
kubron.example.com {
    reverse_proxy 127.0.0.1:8080
}
```

For nginx, forward `Upgrade` and `Connection` headers (WebSocket is used for events and terminals), set a long `proxy_read_timeout` (for example `3600s`) and send `X-Forwarded-Proto`.

## Step 3b. Install with Helm

### Create the namespace and the Secret

The chart does not create the key. You create the Secret, and the chart references it:

```bash
kubectl create namespace kubron

kubectl -n kubron create secret generic kubron-secrets \
  --from-literal=KUBRON_SECRET_KEY='k1:...' \
  --from-literal=KUBRON_DATABASE_URL='postgres://kubron:...@db.example.com:5432/kubron?sslmode=verify-full'
```

### Write your values

Create `values.yaml`. This example uses ingress-nginx with TLS and two replicas:

```yaml
replicaCount: 2

secrets:
  existingSecret: kubron-secrets

ingress:
  enabled: true
  className: nginx
  annotations:
    # Events and terminals use WebSocket; ingress-nginx closes idle ones after 60 s by default
    nginx.ingress.kubernetes.io/proxy-read-timeout: "3600"
    nginx.ingress.kubernetes.io/proxy-send-timeout: "3600"
    nginx.ingress.kubernetes.io/proxy-body-size: 5m
    # Optional: prefer the same replica for the same browser
    nginx.ingress.kubernetes.io/affinity: cookie
    nginx.ingress.kubernetes.io/session-cookie-name: kubron-affinity
    nginx.ingress.kubernetes.io/session-cookie-samesite: Lax
    nginx.ingress.kubernetes.io/session-cookie-secure: "true"
    nginx.ingress.kubernetes.io/session-cookie-max-age: "43200"
  hosts:
    - host: kubron.example.com
      paths:
        - path: /
          pathType: Prefix
  tls:
    - secretName: kubron-tls
      hosts:
        - kubron.example.com

kubron:
  sessionTTL: 12h
```

Notes:

- With ingress and TLS enabled, the chart sets `KUBRON_TRUST_PROXY=1` automatically and builds `KUBRON_ALLOWED_HOSTS` from your ingress hosts.
- The default `networkPolicy` allows incoming traffic only from the `ingress-nginx` namespace. If your ingress controller lives elsewhere, set `networkPolicy.ingress.from` to match it, otherwise nothing will reach the pods.
- `values.yaml` in the chart is fully commented, and `values.schema.json` validates your input. Ready-made examples are in `deploy/helm/kubron/ci/`.

### Install

```bash
helm install kubron oci://ghcr.io/24dso-labs/charts/kubron \
  --version 1.0.0 -n kubron -f values.yaml
```

The chart version equals the release version (without `v`), and the image tag defaults to the same version. For a build from source, use `helm install kubron deploy/helm/kubron ...` and set `image.repository` and `image.tag` yourself.

Pin the image by digest in production: add `--set image.digest=sha256:...` (it overrides the tag).

### Check the rollout

```bash
kubectl -n kubron rollout status deploy/kubron
kubectl -n kubron get pods
kubectl -n kubron logs deploy/kubron | tail
```

Both pods should become `Ready`. Then open `https://kubron.example.com`.

## Step 4. Create the first administrator

Until the first user exists, anyone who could reach the server could claim it. Kubron therefore requires a **setup token** for this step.

**Helm:** the chart creates a random token automatically in a Secret named `<release>-setup`. Read it:

```bash
kubectl -n kubron get secret kubron-setup \
  -o jsonpath='{.data.KUBRON_SETUP_TOKEN}' | base64 -d; echo
```

Open the site, enter the token on the **Welcome to Kubron** screen, and create the administrator.

**Docker:** use the value you passed as `KUBRON_SETUP_TOKEN`.

**No token, no browser:** create the administrator directly in the database. Pass the password on standard input or via `KUBRON_ADMIN_PASSWORD`, never as an argument:

```bash
kubectl -n kubron exec -i deploy/kubron -- \
  /kubron-server create-admin --username admin < password.txt
```

Afterwards the token is no longer used. You may delete the `<release>-setup` Secret and set `setup.enabled=false`. **GitOps users:** set `setup.existingSecret` yourself; Argo CD and `helm template` render without cluster access, so a generated token would change on every render.

## Step 5. Add clusters and users

1. Sign in as the administrator.
2. Create teams and users and assign roles in the users section. The built-in roles are `user` (read-only), `operator` and `admin`.
3. Add clusters from the cluster list. The server never runs commands from a kubeconfig: embed credentials, use the sign-in dialog, or paste a token. See [Connecting to Kubernetes](/features/cluster-auth#web) and the [kubeconfig rules](/server/quick-start#add-your-first-cluster).

To manage the cluster that Kubron itself runs in, set `inCluster.enabled=true`. The server then uses its own ServiceAccount; choose the RBAC profile in the chart's `rbac` section and grant only what you need.

## Optional: browser sign-in and Google accounts

Users sign in to clusters through a dialog (see [Connecting to Kubernetes](/features/cluster-auth)). Two features need server-side settings. Both are optional; without them the paste-the-address and service-account-key methods keep working.

### Public URL for browser sign-in

For the OIDC `browser-callback` flow the server needs the address the identity provider returns the browser to. Set `KUBRON_PUBLIC_URL` to the public origin of Kubron:

```bash
KUBRON_PUBLIC_URL=https://kubron.example.com
```

- Production requires `https`. `http` is accepted only for local development on `localhost`, a `.localhost` name or a loopback IP.
- It must be an absolute URL with no path, credentials, query or fragment. The server validates it at startup and refuses to start with an unsafe value.
- Register `https://kubron.example.com/auth/cluster/callback` as a redirect URI on the identity provider's web client.
- Make sure your proxy passes callback requests through without changing their query parameters.

With Helm, use `kubron.extraEnv`:

```yaml
kubron:
  extraEnv:
    - name: KUBRON_PUBLIC_URL
      value: "https://kubron.example.com"
```

### Google accounts for GKE

By default GKE clusters sign in with a service-account key. To let users sign in with their own Google account, register an OAuth client in Google Cloud and pass it to the server:

```bash
KUBRON_GKE_OAUTH_CLIENT_ID=1234567890-abcdef.apps.googleusercontent.com
KUBRON_GKE_OAUTH_CLIENT_SECRET=...   # keep in a Secret, never in values
```

```yaml
kubron:
  extraEnv:
    - name: KUBRON_GKE_OAUTH_CLIENT_ID
      value: "1234567890-abcdef.apps.googleusercontent.com"
    - name: KUBRON_GKE_OAUTH_CLIENT_SECRET
      valueFrom:
        secretKeyRef: {name: kubron-gke-oauth, key: client-secret}
```

- With `KUBRON_PUBLIC_URL` set, create a **Web application** client and add `<KUBRON_PUBLIC_URL>/auth/cluster/callback` as an authorized redirect URI. Without it, create a **Desktop app** client and users paste the address Google redirected to.
- Kubron requests the `openid`, `userinfo.email` and `cloud-platform` scopes. `cloud-platform` is a sensitive scope: Google may require app verification for external users, and apps in Testing mode have user and refresh-token limits. Check Google's current OAuth consent screen rules before rollout.
- The server refuses to start with a client secret but no client ID. Previously saved keys keep working.

### What to allow on the identity provider

- **HTTPS issuer.** In the Web edition the issuer must use HTTPS with a certificate the server trusts. `insecure-skip-tls-verify` is rejected for web sign-in; pass the CA as embedded kubeconfig data.
- **Device grant (RFC 8628).** Enable it on the client if users should sign in by device code. Without it Kubron falls back to the browser method when configured.
- **Authorization Code with PKCE (S256)** for browser sign-in.
- **Refresh tokens.** The provider must issue them so users are not asked again at every connection. For Dex, add `--oidc-extra-scope=offline_access` to the kubeconfig.
- **Network egress.** Allow the server to reach the issuer and the endpoints from OIDC discovery (authorization, token, device, JWKS, userinfo, revocation). Set `networkPolicy.egress.idp.ports` or `idp.cidrs` for non-standard ports. Under the `strict` address policy, add an internal IdP to `KUBRON_CLUSTER_ADDR_ALLOW_CIDRS`.

## Upgrade

```bash
helm upgrade kubron oci://ghcr.io/24dso-labs/charts/kubron \
  --version 1.1.0 -n kubron -f values.yaml
```

- Replicas are replaced one by one with a rolling update. Database migrations run automatically on startup. Details for multi-replica operation are in the [HA guide](https://github.com/24DSO-labs/kubron/blob/master/docs/ha.md).
- Read the [changelog](https://github.com/24DSO-labs/kubron/blob/master/CHANGELOG.md) first. It lists behaviour changes, for example roles and permissions.
- Take a database backup before every upgrade.
- To roll back: `helm rollback kubron -n kubron`. A rollback cannot undo a database migration, so restore from backup if a release changed the schema and you need to go back further.

## Uninstall

```bash
helm uninstall kubron -n kubron
```

This removes the workload but **not** your Secret or the PostgreSQL data. Delete the database and the Secret yourself if you want everything gone.

## Verify the image and chart

Every release image and chart is signed with cosign and carries provenance and SBOM attestations. Always verify the **digest**, not only the tag.

```bash
VERSION=1.0.0
IMAGE=ghcr.io/24dso-labs/kubron
CHART=ghcr.io/24dso-labs/charts/kubron

# Provenance: built by this repository's publish workflow
gh attestation verify "oci://$IMAGE:$VERSION" --repo 24DSO-labs/kubron \
  --signer-workflow 24DSO-labs/kubron/.github/workflows/publish.yml

# Signature (keyless)
IDENTITY='^https://github\.com/24DSO-labs/kubron/\.github/workflows/publish\.yml@refs/tags/v[0-9].*$'
cosign verify "$IMAGE:$VERSION" \
  --certificate-identity-regexp "$IDENTITY" \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
cosign verify "$CHART:$VERSION" \
  --certificate-identity-regexp "$IDENTITY" \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
```

The full set of checks, including SBOM attestations and a Kyverno policy that blocks unsigned images, is in the [operations guide](https://github.com/24DSO-labs/kubron/blob/master/docs/deploy.md).

## Hardening checklist

- [ ] TLS terminates at a trusted proxy, and the pod is reachable only through it.
- [ ] `KUBRON_TRUST_PROXY=1` only when that is true.
- [ ] `KUBRON_ALLOWED_HOSTS` is set (the chart does this from `ingress.hosts`).
- [ ] `KUBRON_PUBLIC_URL`, if used, is an `https` origin that matches the ingress host.
- [ ] The encryption key is in a secret manager and backed up separately from the database.
- [ ] PostgreSQL uses `sslmode=verify-full`, has backups and is not exposed publicly.
- [ ] `networkPolicy` allows only your ingress controller; the metrics port is reachable only from monitoring.
- [ ] Two or more replicas with a `podDisruptionBudget`.
- [ ] Terminals, node shell and port-forward are disabled if you do not need them (`kubron.disableExec`, `kubron.disableNodeShell`, `kubron.disablePortForward`).
- [ ] Images are pinned by digest and verified.

## Troubleshooting

| Symptom | What to check |
|---|---|
| Pods `CrashLoopBackOff` right after start | `kubectl logs`: a missing `KUBRON_SECRET_KEY`, an unreachable database or a key that does not match the stored data stops startup on purpose |
| `ImagePullBackOff` | Wrong `image.tag` (source charts default to `0.0.0-dev`, which is not published) or a private registry without `imagePullSecrets` |
| The site answers `421` | The `Host` is not in `KUBRON_ALLOWED_HOSTS`; add it under `kubron.allowedHosts` |
| Cannot sign in; the cookie is lost | TLS is not terminated before the pod, or `trustProxy` is off behind an HTTPS proxy |
| Terminals and live updates disconnect after about a minute | The proxy times out idle WebSockets. Raise the read/send timeouts (see the ingress annotations above) |
| Nothing reaches the pods when `networkPolicy` is on | Set `networkPolicy.ingress.from` to your ingress controller's namespace |
| "Setup token is missing or invalid" | Read the token again from the `<release>-setup` Secret, or use `create-admin` |

## Learn more

- [Configuration reference](/server/configuration)
- [High availability](https://github.com/24DSO-labs/kubron/blob/master/docs/ha.md)
- [Prometheus metrics](https://github.com/24DSO-labs/kubron/blob/master/docs/metrics.md)
- [Backup and restore](https://github.com/24DSO-labs/kubron/blob/master/docs/backup.md)
