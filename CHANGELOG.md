# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Changed

- **Cluster authentication network policy:** native AKS sign-in now uses the shared IdP HTTP client, enforcing `KUBRON_CLUSTER_ADDR_POLICY` on Azure authority connections and redirects, with HTTPS-only web requests, bounded responses/timeouts and no environment proxy.
- **Cluster authentication docs:** updated the English and Russian user guides to describe the implemented credential broker, OIDC and AKS sign-in modes, Desktop exec confirmation, Web manual-token fallback and unsupported Azure modes.

### Added

- **GKE sign-in with a service account key:** a kubeconfig that uses `gke-gcloud-auth-plugin` now asks for a service
  account key (JSON) instead of showing a manual-token command. The key is exchanged for a token with the JWT-bearer
  grant at the fixed Google address and is saved encrypted. The credentials of the server (Application Default
  Credentials, the metadata server) are never used, and `token_uri` from the key file is ignored.

## [1.0.0] - 2026-11-03

### Security

- **Password hashing:** new passwords use Argon2id (v=19, 19 MiB, 2 iterations, 1 lane). Existing PBKDF2-HMAC-SHA256 hashes remain verifiable and are upgraded after a successful login; no forced password reset is required.
- **Roles:** the `user` role no longer has `secrets.read`. It could read Secret values, Helm release values/manifests
  and certificate expiry, which contradicts a read-only role. `operator` and `admin` keep the permission. The UI shows
  “no access” instead of hidden controls. **Behavior change:** grant the `operator` role to users who need these values.
- **Helm:** managing Helm repositories, OCI registries and the operation journal is now checked against the team
  of the **active cluster**. Before, `clusters.manage` was accepted from an admin of *any* team, so an admin of
  one team with a read-only role in another could change that team's repositories while its cluster was active.
  No new permission was introduced.
- **Port-forward proxy and terminals:** with `KUBRON_CLUSTER_ADDR_POLICY` enabled, the HTTP port-forward proxy
  (ADR-0017) and terminals open their connection to the API server through the address policy as well; a test
  forbids calling the stock SPDY/WebSocket dialers directly.
- **First-run setup:** the first administrator can no longer be created by whoever reaches the server first. In the
  service mode (PostgreSQL or a non-loopback listener) `SetupAdmin` requires a **setup token** (`KUBRON_SETUP_TOKEN`)
  while there are no users. **Upgrade:** a new mandatory step for new installations. The Helm chart creates the token in
  a Secret (or reads `setup.existingSecret`), and its `NOTES.txt` shows how to read it; the administrator can also be
  created with `kubectl exec -i deploy/<name> -- /kubron-server create-admin --username <name>` (the password goes on stdin or in
  `KUBRON_ADMIN_PASSWORD`, never as an argument).
  Installations that already have users are not affected. See `docs/deploy.md`.
- **Cluster addresses:** `KUBRON_CLUSTER_ADDR_POLICY` now defaults to `permissive` (it was `off`): API-server addresses
  in loopback, link-local and cloud-metadata ranges are rejected. **Upgrade:** a cluster whose API server is on such
  an address (for example kind on `127.0.0.1`) needs `KUBRON_CLUSTER_ADDR_ALLOW_CIDRS` (chart:
  `kubron.clusterAddrAllowCidrs`) or `KUBRON_CLUSTER_ADDR_POLICY=off`.
- **Cluster settings:** secrets inside a kubeconfig (tokens, client keys, passwords) are masked when a cluster is
  opened for editing; a secret is replaced only when a new value is entered.
- **Chart:** the egress NetworkPolicy to cluster API servers and identity providers excludes link-local and
  cloud-metadata addresses (`169.254.0.0/16`, `fd00:ec2::254/128`) by default; the setup token and the Host allowlist
  (`KUBRON_ALLOWED_HOSTS`, taken from `ingress.hosts` and `*.pf.<baseDomain>`) are set by the chart.
- **Supply chain:** the server image and the chart are signed with cosign (keyless) and carry GitHub attestations of
  provenance and SBOM; Trivy blocks publishing an image with a fixable HIGH/CRITICAL vulnerability. How to verify:
  `docs/deploy.md`, "Image and chart verification".

### Fixed

- **Web mode:** `OpenArgoUI` was still on the denied-method list, so Argo CD could not be opened from the browser; it is now
  allowed and returns the proxy stream address (or the published Argo CD address from the cluster settings).
- **Helm with several replicas:** the repository index and OCI tag caches live in each replica's `emptyDir`; a
  repository added on one replica is now fetched on first use on the others (the list and update checks fill the cache
  in the background). Cache file names include a hash of the URL, so a repository re-added under the same name with
  another address does not pick up a stale cache.

### Added

- **Release:** SBOMs for the image (SPDX), the chart and the desktop build (CycloneDX) are attached to the GitHub Release
  and listed in `SHA256SUMS.txt`; `docs/releasing.md` now covers the server side (image, chart, signatures), the
  pre-tag checklist, the single version number and rollback (`helm rollback`, migration policy).
- **CI:** `deps-audit` job (`npm audit --omit=dev --audit-level=high`, osv-scanner), Trivy reports for the image and the
  rendered chart, a weekly scan of the latest published image (issue with the `security` label), exceptions only in
  `.trivyignore` / `osv-scanner.toml` with a reason and an expiry (checked by `scripts/check-ignores.py`); Dependabot
  also covers `pkg/ext`, `docs-site`, Docker and weekly GitHub Actions updates.
- **Chart:** `setup.existingSecret` / `setup.tokenKey` (generated and kept across upgrades when not set),
  `kubron.allowedHosts`, `kubron.clusterAddrPolicy`, `kubron.clusterAddrAllowCidrs`, `rbac.profile`
  (`readonly` | `operator` | `custom`) and `networkPolicy.egress.{clusterApi,idp}` with configurable CIDRs and ports.
- **Chart:** `portForward` values (`baseDomain`, `scheme`, `idleTimeout`, `maxDuration`) set the `KUBRON_PF_*` variables and add the
  `*.pf.<baseDomain>` rule to the Ingress; without a domain port-forward stays off in the service mode.
- **High availability:** the Helm chart now defaults to two replicas (PodDisruptionBudget, spreading across nodes,
  `kubron.drainDelay`, optional `secrets.keys.dbListenUrl`, `kubron.auditRetention`); the "several replicas are not
  supported" warning is gone. New CI job `e2e-ha` runs two replicas on one PostgreSQL: login on one replica and a request
  on the other, session end and login lockouts across replicas, 50 sessions, and killing a replica during a session.
- **Network policy:** the `oidc-password` identity provider address (issuer and the `token_endpoint` from its answer)
  follows `KUBRON_CLUSTER_ADDR_POLICY` as well.
- **Decision log:** `docs/decisions/` records the decisions taken without a separate discussion, who took them and
  how to revert.
- **Tests:** Helm through the PostgreSQL store end to end, and `kubron-server rekey` re-encrypting the Helm secrets.
- **Operations:** structured JSON logging (`log/slog`) in `kubron-server` with a secret redactor (passwords,
  tokens, `Authorization` headers, URL credentials, encryption key, database passwords), a per-request
  `request_id` (`X-Request-Id` header), `KUBRON_LOG_LEVEL` and `KUBRON_LOG_FORMAT`. `KUBRON_AUDIT_STDOUT=1`
  mirrors every audit record to stdout as a JSON line (`"type":"audit"`) for an external collector. See
  `docs/logging.md`.
- **Operations:** `docs/backup.md` (order of backup, restore, key rotation and `rekey`) and a CI job
  `restore-check` (`scripts/restore-check.sh`: data → `pg_dump` → empty database → same key reads the secret;
  a wrong key gives a clear error). The server now checks at startup that `KUBRON_SECRET_KEY` reads the
  encrypted data in the database and refuses to start otherwise; `KUBRON_ALLOW_KEY_MISMATCH=1` starts it
  without access to cluster secrets.
- **Port-forward:** with several replicas a stream carries its owner in the id (one DNS label); a request to a stream of
  another replica gets `421` before the one-time ticket is spent, and `GetPortForwardURL`/`StopPortForward` answer
  `409 stream_elsewhere` (the UI retries). See ADR-0017, section 7.

### Changed

- **Docs:** README, the roadmap and `docs/tickets/stage-1.md` no longer describe the image as unpublished, the server as
  single-replica or `SetupAdmin` as open; stale "PR n" and branch notes were removed from `docs/ha.md` and
  `docs/ha-sessions.md`; `docs/tickets/stage-4-5.md` records the status of stages 4-6 and what is still open.
- **Release:** `publish.yml` is called by `release.yml` after CI instead of reacting to the tag on its own, so SBOMs reach the
  immutable GitHub Release and the image is never published from an untested commit. A published image or chart version is
  never overwritten. `Chart.yaml` holds `0.0.0-dev` placeholders; the version comes from the tag.

## [0.6.0] - 2026-11-01

### Added

- **Argo CD:** published address in cluster settings (`argoUrl`, migration `0007`). If set, `OpenArgoUI` opens it
  (`mode=external`) and does not create a port-forward or a proxy stream; works even with
  `KUBRON_DISABLE_PORT_FORWARD`. Only `http(s)` addresses without credentials, query or fragment are accepted.
- **Terminals:** separate `nodeshell` permission for the node shell (admin only by default), flags
  `KUBRON_DISABLE_EXEC`, `KUBRON_DISABLE_NODE_SHELL`, `KUBRON_DISABLE_PORT_FORWARD` (reflected in
  `GetCapabilities`), `KUBRON_TERMINAL_MAX_DURATION` / `KUBRON_TERMINAL_IDLE_TIMEOUT`, audit of terminal
  start and end, and a per-frame owner check on the terminal WebSocket.
- **Operations:** `/readyz` checks the database (when PostgreSQL is used) and answers `503` while the replica
  is draining; the Helm chart uses it for `readinessProbe` (`/healthz` stays for liveness and startup).

- **Operations:** Prometheus metrics on a separate port (`KUBRON_METRICS_LISTEN`, off by default): RPC
  requests, errors and latency by method, sessions, WebSocket connections, terminals, log streams,
  port-forwards, cluster health states, health-check leader, login lockouts and `429`, DB pool, build info.
  No user, team or cluster names in labels. The chart adds a separate metrics Service (not reachable through
  the Ingress), an optional `ServiceMonitor` and a NetworkPolicy rule for the monitoring namespace.
  See `docs/metrics.md`.

## [0.5.0] - 2026-11-01

### Added

- **Repository:** OpenSSF Scorecard workflow (`scorecard.yml`, results in Code scanning) and
  `.github/CODEOWNERS`.
- **Release:** `publish.yml` publishes the kubron-server image (`ghcr.io/24dso-labs/kubron`) and the Helm
  chart (`oci://ghcr.io/24dso-labs/charts/kubron`) for `vX.Y.Z` tags, with build provenance for the image.
  A manual run is a dry run unless `push` is ticked.
- **HA:** with `KUBRON_DATABASE_URL` web sessions and users live in PostgreSQL and are shared by all
  `kubron-server` replicas: a cookie issued by one replica works on any other, a user created or changed on
  one replica is visible on the rest within ~2 s, and a DB outage of up to 30 s is served from cache without
  logging anyone out (then `503` with `Retry-After`). See `docs/ha-sessions.md`.
- **HA:** replica identity (`KUBRON_REPLICA_ID` / `--replica-id`) and graceful shutdown: on SIGTERM the replica
  drains (`/readyz` reports not ready, `503` for new WebSocket and terminal connections, `KUBRON_DRAIN_DELAY`
  pause), then closes event and terminal sockets with code `1012` (Service Restart); the web client reconnects
  quickly and restores its subscriptions. See `docs/ha.md`.
- **HA:** changes propagate between replicas over PostgreSQL `LISTEN/NOTIFY` (logout, password change, access
  changes, cluster health); a direct connection for the listener can be set with `KUBRON_DB_LISTEN_URL`
  (pgbouncer in transaction mode does not support `LISTEN`). Missed messages are repaired by a full resync.
- **Web mode:** HTTP port-forward proxy (ADR-0017, `docs/adr/0017-http-port-forward-web.md`): each stream gets
  its own origin `{id}.pf.<base-domain>` (`KUBRON_PF_BASE_DOMAIN`; needs a DNS wildcard and certificate), a
  one-time ticket, a host-only cookie and idle/max duration limits; `portforward.start/stop` are audited. The
  UI opens the URL returned by the backend (`portForwardMode`: `local` / `proxy` / `off`); in proxy mode the
  local-port field is hidden. Port-forward can be disabled with `KUBRON_DISABLE_PORT_FORWARD`.
- **Helm:** repositories, OCI registries and the operations journal are stored in PostgreSQL per team in
  service mode (desktop and single-user mode keep the files) with registry passwords encrypted (secretbox,
  AAD) and covered by `kubron-server rekey`; Helm now also works for clusters stored in the database (no
  kubeconfig file is written). See `docs/deploy.md`, section "Helm: состояние и доступ к сети".

### Changed

- **Docs:** refreshed README (logo, badges, `.deb` download, links to the user guides, server mode section,
  roadmap with statuses, screenshots of the overview, pods, YAML editor and GitOps); fixed the local
  web-server build steps. Added a social preview image (`docs/images/social-preview.png`).
- **Docs:** README no longer names third-party products or describes paid features; the GitOps wording is
  tool-neutral and the comparison table was replaced with a short "Why Kubron" list.
- **Deploy:** the Helm chart now defaults to `image.repository: ghcr.io/24dso-labs/kubron` (it still pointed
  to the previous account).
- **Deploy:** the chart still defaults to `replicaCount: 1` and warns above it; the server itself now
  supports several replicas (see the HA entries above).

### Security

- **Helm (SSRF):** Helm HTTP and OCI traffic goes through an address policy (`KUBRON_HELM_ADDR_POLICY`,
  `strict` by default; exceptions via `KUBRON_HELM_ALLOW_CIDRS`): loopback, link-local and cloud metadata
  addresses are always blocked, redirects are re-checked, and a blocked address looks to the user like a
  connection refused. OCI registry passwords are passed to the registry host from memory and are no longer
  written to `registry-config.json` in service mode.
- **Clusters:** optional address policy for the cluster API server taken from a user kubeconfig
  (`KUBRON_CLUSTER_ADDR_POLICY`, off by default; `KUBRON_CLUSTER_ADDR_ALLOW_CIDRS`): the address is checked on
  add and on every connect, and all REST, discovery, informer, Helm, terminal and port-forward connections go
  through the policy dialer; `proxy-url` is rejected while the policy is on.

## [0.4.0] - 2026-10-31

### Added

- **Deploy:** `Dockerfile` for kubron-server (distroless, non-root, read-only rootfs) and the Helm chart
  `deploy/helm/kubron` (Deployment, Service, Ingress, NetworkPolicy, PodDisruptionBudget, optional
  read-only RBAC for in-cluster mode). Guide: `docs/deploy.md`. Single replica only until HA.

### Security

- **HTTP:** `kubron-server` now sends `Content-Security-Policy` (scripts `'self'`, no `unsafe-inline`/`unsafe-eval`),
  `Strict-Transport-Security` (secure non-loopback requests only), `Permissions-Policy`, COOP and CORP.
  `KUBRON_CSP_REPORT_ONLY=1` switches the policy to report-only. See `docs/http-security.md`.
- **Rate limit:** the client address behind a proxy is read from the right of `X-Forwarded-For`
  (`KUBRON_TRUSTED_PROXY_HOPS`, `KUBRON_TRUSTED_PROXIES`); IPv6 is keyed by /64; a flood of unique logins or
  addresses no longer wipes active lockouts; `ChangePassword` guessing is limited; anonymous RPC calls get a
  per-address token bucket (`429` with `Retry-After`).
## [0.3.0] - 2026-10-29

### Added

- **Helm:** Chart upgrade preview now includes the new chart's notes and a diff of old vs new chart default values.

### Fixed

- Helm: fixed issues #37 and #38.

## [0.2.0] - 2026-10-29

### Added

- Helm: upgrade a release to another chart version ("Chart version" tab). Choose the repository
  and version, keep or edit the current values, preview the manifest diff, then apply with live
  progress and cancel. Downgrades and deprecated versions are flagged. The preview also shows the
  new chart's notes and a diff of the old and new chart default values.
- Helm: audit-log labels for install, upgrade, rollback, uninstall and preview actions.
- Multilingual UI (English base + Russian) on vue-i18n v11: language switcher


### Fixed

- Helm: the "Edit values" tab showed a raw translation key instead of its title.
- Helm: the "Add" button on the repositories page showed a stray "≥" instead of "+".
- Helm: repository badges (`oci`, `auth`, `no TLS verify`) and `deprecated` in the catalog are now translated.
- Helm: the "release changed" warning and the empty operations list showed literal `{{ ... }}`
  placeholders; untranslated Russian strings in the Helm section.
- Helm: OCI chart versions with build metadata (`1.0.0+build`) were listed with `_` and could not
  be selected.
- Docs: the 0.1.0 notes said cancel is available for rollback and uninstall; it is available for
  install and upgrade only (the Helm SDK takes no context for the other two).
- Web mode: idle sessions are now cleaned up in the background; per-user resource limits are
  configurable in the server config; a Helm operation is bound to its owner session (only the
  owner can cancel it).

## [0.1.0] - 2026-10-28

First public release.

### Added

- Desktop app (Wails + Go + Vue) with multi-cluster management and namespace scoping.
- Cluster dashboard: health, capacity, problem pods, recent changes, expiring certificates.
- Resource browser for built-in Kubernetes types and CRDs, with a Monaco YAML editor.
- Pod tools: logs, exec, port forwarding. Deployment scale, restart and rollback.
  Node cordon, drain and shell.
- Optional metrics stack (Prometheus, kube-state-metrics, node-exporter) with charts.
- Helm: releases, history and diff, rollback, chart catalog (including OCI registries),
  install with dry-run, upgrade, update indicator. Live progress with cancel for install,
  upgrade, rollback and uninstall, an operations journal view, Secret and ConfigMap
  release storage.
- Argo CD: applications, ApplicationSets, projects, sync and other actions, diff, graph.
- Local users with `admin` / `operator` / `user` roles, audit log with rotation and client
  address, auto-logout on inactivity.
- Public extension API (`pkg/ext`) for external modules.
- Web mode (`cmd/kubron-server`): several concurrent sessions, per-session
  cluster managers with per-user resource limits, idle informers released after a TTL.

[Unreleased]: https://github.com/24DSO-labs/kubron/compare/v1.0.0...HEAD
[1.0.0]: https://github.com/24DSO-labs/kubron/compare/v0.6.0...v1.0.0
[0.6.0]: https://github.com/24DSO-labs/kubron/compare/v0.5.0...v0.6.0
[0.5.0]: https://github.com/24DSO-labs/kubron/compare/v0.4.0...v0.5.0
[0.4.0]: https://github.com/24DSO-labs/kubron/compare/v0.3.0...v0.4.0
[0.3.0]: https://github.com/24DSO-labs/kubron/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/24DSO-labs/kubron/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/24DSO-labs/kubron/releases/tag/v0.1.0
