# Architecture

Kubron is one Go core with two shells and one Vue interface. This page explains how the pieces fit together, so you can find your way around the repository. The decisions behind them are recorded as ADRs in [`docs/adr`](https://github.com/24DSO-lab/kubron/blob/master/docs/adr/README.md).

[[toc]]

## The big picture

- **The core** (`internal/app`) holds all application logic: clusters, Kubernetes access, Helm, Argo CD, authentication and audit. It does not import Wails.
- **Two shells** run the core. The **Desktop** shell is `main.go` at the repository root, built with Wails. The **Web** shell is `cmd/kubron-server`, an HTTP server. Method names are the same in both, so the same interface works on top of either.
- **One interface** (`frontend/`, Vue 3) is built for one of two targets: `desktop` or `web`.

```
 Vue 3 interface (frontend/)
        |  built for VITE_TARGET = desktop | web
        v
 Wails bindings (Desktop)         HTTP: POST /api/rpc/{method}, WebSocket /api/ws (Web)
        |                                   |
   main.go (shell)                  cmd/kubron-server (shell)
        \_______________  _______________/
                        \/
                internal/app  (the core)
        clusters - Kubernetes - Helm - Argo CD - auth - audit
                        |
              Store: files (Desktop)  |  PostgreSQL (Web)
```

## Repository layout

| Path | What is there |
|---|---|
| `main.go`, `wails.json` | Desktop shell (Wails) |
| `cmd/kubron-server` | Web shell: HTTP server, admin commands (`create-admin`, `rekey`, `import-files`) |
| `internal/` | Private Go packages (see below) |
| `pkg/ext/` | Public extension contracts: a separate Go module with only the standard library |
| `frontend/` | Vue 3 interface, i18n catalogs and the `platform/` layer |
| `deploy/helm/kubron` | Helm chart for the Web edition |
| `manifests/metrics-stack.yaml` | Optional metrics stack installed from the application |
| `build/` | Wails packaging assets and Linux/Windows packaging files |
| `docs/` | ADRs, operations guides and decision records |
| `docs-site/` | This website (VitePress) |
| `scripts/` | Release, license-header, DCO, i18n and restore checks |
| `examples/ext-authenticator` | A minimal external module |

## Core and shells

The core talks to its shell only through the small `Platform` interface: emit an event to the interface, pick a file, open a URL, report the shell's capabilities. The Wails and web shells implement it, and a lint rule forbids the core from importing Wails (ADR-0002).

The shell reports **capabilities** (file dialog, port-forward mode, terminals, node shell, local kubeconfig access, SSO), and the interface hides what is unavailable. Desktop has everything local; the web server turns some features off or changes them, for example port-forward goes through a proxy.

## How the Web edition talks to the browser

- **API.** `POST /api/rpc/{method}` with a JSON array of arguments. A reflection-based dispatcher calls the core method, but only methods on an allowlist.
- **Events and terminals.** One WebSocket (`/api/ws`) carries events to the browser, and binary WebSocket frames carry terminal data.
- **Sessions.** A cookie plus CSRF and Origin checks. With PostgreSQL, sessions are shared between replicas. See [HTTP security](/server/http-security) and [High availability](/server/ha).
- **Events are addressed** to a session, and the identifiers the browser sees are server-generated.

### The life of a request

1. The browser sends `POST /api/rpc/{method}` with the session cookie and a CSRF token. The server checks that the `Origin` matches the `Host` and that the host is allowed.
2. It finds the session (shared through PostgreSQL when there are several replicas) and the user's role.
3. The dispatcher calls the core method, but only if the method is on the allowlist.
4. The core's first step is `authz(permission)`: your Kubron role, and in Web your team role, must allow the action. A refusal is recorded in the audit log as **Denied**.
5. The core uses the session's view of the cluster manager to connect with the cluster's credentials. Kubernetes RBAC checks the call again.
6. The result goes back to the browser, an allowed privileged action is written to the audit log, and later changes reach the browser through the WebSocket.

## Clusters and Kubernetes access

- A shared **connection pool** keeps informers per cluster, with reference counting and a time-to-live, so lists and the dashboard are live without constant polling.
- Each user session gets a **session view** of the manager: its own active cluster, terminals, log streams and port-forwards, sharing the pool.
- Authentication to clusters is handled by **credential providers** (OIDC, AKS, EKS, GKE, `exec` on Desktop) behind one sign-in contract. See [Connecting to Kubernetes](/features/cluster-auth).
- Addresses supplied by users (clusters, identity providers, Helm repositories) go through an **SSRF guard** (`netpolicy`).

## Storage and secrets

The core uses a `Store` interface with two implementations: **files** under `~/.kubron` (Desktop and single-user server) and **PostgreSQL** (service mode). PostgreSQL is external and operated by you. Schema migrations are plain SQL files applied under a PostgreSQL advisory lock, so replicas starting together do not race. Cross-replica notifications use `LISTEN/NOTIFY`.

Secrets in the database (kubeconfigs, cluster passwords, Argo CD tokens, Helm repository secrets, saved sign-in credentials) are encrypted with envelope encryption keyed by `KUBRON_SECRET_KEY`. See [Backup, restore and key rotation](/server/backup).

## Multi-tenancy

In the Web edition the hierarchy is organization, then team, then cluster. Roles are assigned per team, and a cluster belongs to exactly one team. See [Teams](/features/teams).

## The interface

`frontend/src/platform/` contains a thin layer (`api`, `events`, `files`) whose implementation is chosen at build time with `VITE_TARGET=web|desktop`. Components do not call Wails bindings directly (a lint rule enforces it). Resource views are described by data in `frontend/src/resources`, and all text goes through vue-i18n, with English as the base language.

## Main internal packages

| Package | Purpose |
|---|---|
| `app` | The core: application methods used by both shells |
| `k8s` | Kubernetes, Helm and Argo CD access, dashboard calculations |
| `auth`, `teams`, `websession` | Local users and roles, teams, shared web sessions |
| `credential`, `signin`, `idpnet`, `secrets` | Cluster sign-in providers, pending sign-in calls, the identity-provider HTTP client, envelope encryption |
| `store`, `pgstore`, `migrate`, `secretbox` | Storage interface, PostgreSQL store, migrations, secret encryption |
| `server`, `publicerr`, `streamid`, `termbuf` | HTTP shell, user-safe error texts, stream identifiers, terminal buffers |
| `replicaevents`, `jobs`, `clusterhealth` | Messages between replicas, single-replica jobs, cluster health checks |
| `pfproxy` | HTTP port-forward for the Web edition |
| `netpolicy` | Outbound address policy (SSRF protection) |
| `audit`, `logging`, `metrics` | Audit log, process log, Prometheus metrics |
| `platform` | The Wails and web implementations of `Platform` |

## Where to read more

- [ADR index](https://github.com/24DSO-lab/kubron/blob/master/docs/adr/README.md): numbered decisions from code layout to password hashing and logging.
- [Extending Kubron](/development/extending): the public `pkg/ext` contracts.
- [Contributing](/development/contributing).
