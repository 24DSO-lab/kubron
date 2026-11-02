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

## Verify the image and chart

Release images and the OCI chart are signed with cosign (keyless) and carry build-provenance and SBOM attestations. Verify before installing:

```sh
gh attestation verify oci://ghcr.io/24dso-labs/kubron:<version> --repo 24DSO-labs/kubron
cosign verify ghcr.io/24dso-labs/kubron:<version> \
  --certificate-identity-regexp '^https://github.com/24DSO-labs/kubron/\.github/workflows/publish\.yml@refs/tags/v' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
```

The same commands work for `oci://ghcr.io/24dso-labs/charts/kubron`. See the [deployment guide](https://github.com/24DSO-labs/kubron/blob/master/docs/deploy.md) for details.

## Network and RBAC

- **NetworkPolicy.** Egress to cluster API servers is open except link-local and cloud metadata addresses (`169.254.0.0/16`, `fd00:ec2::254/128`). Narrow PostgreSQL, API-server and IdP egress to your own networks with `networkPolicy.egress.*.cidrs`.
- **In-cluster RBAC.** `rbac.profile` is `readonly` (default, no Secrets), `operator` (restart, scale, rollback, retry Job, cordon) or `custom`. The ServiceAccount is shared by all users, so the profile is the ceiling of what anyone can do in that cluster.

## Domains, hosts and TLS

- The chart sets `KUBRON_ALLOWED_HOSTS` from `ingress.hosts` and `*.pf.<portForward.baseDomain>`; add other addresses to `kubron.allowedHosts`.
- Set `portForward.baseDomain` to a **separate registrable domain**, not a subdomain of the Kubron host: JavaScript of a proxied app could otherwise overwrite Kubron cookies. It needs wildcard DNS and a wildcard certificate for `*.pf.<baseDomain>`.
- Enable `KUBRON_TRUST_PROXY` only when the pod is reachable solely through your proxy. Use sticky sessions (cookie affinity) on the Ingress: terminals, logs and port-forward streams live on the replica that opened them, and a port-forward ticket is single-use per replica.

## High availability

Multiple replicas are supported when PostgreSQL is configured. Sessions and user state are shared, changes propagate through PostgreSQL notifications, and replicas drain WebSocket and terminal connections during shutdown. See the [HA operations guide](https://github.com/24DSO-labs/kubron/blob/master/docs/ha.md) and [session design](https://github.com/24DSO-labs/kubron/blob/master/docs/ha-sessions.md).

## Important operational notes

- The server expects a TLS proxy; it does not terminate TLS itself.
- The metrics listener is separate from the application listener and should only be reachable by monitoring infrastructure.
- Server-mode port-forward requires `KUBRON_PF_BASE_DOMAIN`, wildcard DNS and a wildcard TLS certificate.
- Configure PostgreSQL backups and encryption-key recovery before production use.

For the full list of settings, chart values, network policies and operational procedures, use the [deployment guide](https://github.com/24DSO-labs/kubron/blob/master/docs/deploy.md).
