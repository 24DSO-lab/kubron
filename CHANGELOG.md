# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Security

- **Helm:** managing Helm repositories, OCI registries and the operation journal is now checked against the team
  of the **active cluster**. Before, `clusters.manage` was accepted from an admin of *any* team, so an admin of
  one team with a read-only role in another could change that team's repositories while its cluster was active.
  No new permission was introduced.
- **Port-forward proxy and terminals:** with `KUBRON_CLUSTER_ADDR_POLICY` enabled, the HTTP port-forward proxy
  (ADR-0017) and terminals open their connection to the API server through the address policy as well; a test
  forbids calling the stock SPDY/WebSocket dialers directly.

### Added

- **Tests:** Helm through the PostgreSQL store end to end, and `kubron-server rekey` re-encrypting the Helm secrets.

## [0.6.0] - 2026-11-01

- **Argo CD:** published address in cluster settings (`argoUrl`, migration `0006`). If set, `OpenArgoUI` opens it
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
- Experimental web mode (`cmd/kubron-server`): several concurrent sessions, per-session
  cluster managers with per-user resource limits, idle informers released after a TTL.

[Unreleased]: https://github.com/24DSO-labs/kubron/compare/v0.6.0...HEAD
[0.6.0]: https://github.com/24DSO-labs/kubron/compare/v0.5.0...v0.6.0
[0.5.0]: https://github.com/24DSO-labs/kubron/compare/v0.4.0...v0.5.0
[0.4.0]: https://github.com/24DSO-labs/kubron/compare/v0.3.0...v0.4.0
[0.3.0]: https://github.com/24DSO-labs/kubron/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/24DSO-labs/kubron/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/24DSO-labs/kubron/releases/tag/v0.1.0
