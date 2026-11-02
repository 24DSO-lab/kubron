# Docker and Kubernetes deployment

Kubron publishes a server container image and Helm chart for tagged releases.

- Container image: `ghcr.io/24dso-labs/kubron`
- OCI chart: `oci://ghcr.io/24dso-labs/charts/kubron`
- Chart source: [deploy/helm/kubron](https://github.com/24DSO-labs/kubron/tree/master/deploy/helm/kubron)

## Prerequisites

- PostgreSQL 14+
- A TLS-terminating ingress or reverse proxy
- A persistent, backed-up database
- A 32-byte encryption key encoded as `id:base64`

Generate a key:

```sh
echo "k1:$(openssl rand -base64 32)"
```

Store it securely before deploying. Losing the key means losing access to encrypted kubeconfigs and GitOps tokens.

## Install with Helm

```sh
kubectl create namespace kubron
kubectl -n kubron create secret generic kubron-secrets \
  --from-literal=KUBRON_SECRET_KEY='k1:…' \
  --from-literal=KUBRON_DATABASE_URL='postgres://kubron:…@db.example.com:5432/kubron?sslmode=verify-full'

helm install kubron oci://ghcr.io/24dso-labs/charts/kubron \
  --namespace kubron \
  --set secrets.existingSecret=kubron-secrets \
  --set image.tag=<release-version>
```

Review the chart's `values.yaml` and `values.schema.json` before deployment. Configure ingress TLS, trusted proxy settings, network policy, resource requests and limits, and backups for PostgreSQL.

## First administrator

On a fresh database nobody can sign in, so Kubron has to create the first administrator. Network setup is protected
by a token; pick one of two ways:

- **Setup token.** Put `KUBRON_SETUP_TOKEN` (at least 16 characters) into the secret and set
  `--set secrets.keys.setupToken=KUBRON_SETUP_TOKEN`. The setup screen asks for the token; it is checked in constant time
  and wrong attempts are rate-limited per address.
- **Command line.** Without a token, network setup is disabled. Run
  `kubron-server create-admin --username <name>` next to the server (it talks to the database directly); the password
  is read from `KUBRON_ADMIN_PASSWORD` or stdin, never from an argument.

The token is not needed once an administrator exists, and a single-user server on loopback does not use it.

## Build the image yourself

```sh
docker build -t kubron:dev .
```

## High availability

Multiple replicas are supported when PostgreSQL is configured. Sessions and user state are shared, changes propagate through PostgreSQL notifications, and replicas drain WebSocket and terminal connections during shutdown. See the [HA operations guide](https://github.com/24DSO-labs/kubron/blob/master/docs/ha.md) and [session design](https://github.com/24DSO-labs/kubron/blob/master/docs/ha-sessions.md).

## Important operational notes

- The server expects a TLS proxy; it does not terminate TLS itself.
- The metrics listener is separate from the application listener and should only be reachable by monitoring infrastructure.
- Server-mode port-forward requires `KUBRON_PF_BASE_DOMAIN`, wildcard DNS and a wildcard TLS certificate.
- Configure PostgreSQL backups and encryption-key recovery before production use.

For the full list of settings, chart values, network policies and operational procedures, use the [deployment guide](https://github.com/24DSO-labs/kubron/blob/master/docs/deploy.md).
