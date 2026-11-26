---
layout: home

hero:
  name: Kubron
  text: Kubernetes, under control.

  tagline: A private Kubernetes Operations Workspace for desktop and web. Manage clusters, workloads, Helm and GitOps from one focused workspace.

  actions:
    - theme: brand
      text: Download Kubron
      link: https://github.com/24DSO-lab/kubron/releases
    - theme: alt
      text: Get started
      link: /guide/getting-started
    - theme: alt
      text: View on GitHub
      link: https://github.com/24DSO-lab/kubron

features:
  - icon: ⎈
    title: All your clusters, one workspace
    details: Switch between kubeconfig contexts, scope namespaces and keep an eye on cluster health without juggling terminals.
  - icon: ◈
    title: See what needs attention
    details: Spot unhealthy workloads, resource pressure, recent changes and certificates that are about to expire.
  - icon: ⌘
    title: Operate, don't just observe
    details: Inspect resources, edit YAML with a diff, stream logs, open terminals and forward ports.
  - icon: ⇧
    title: Helm built in
    details: Browse charts and repositories, install and upgrade releases, inspect revisions and roll back.
  - icon: ⟲
    title: GitOps in the same flow
    details: Work with Argo CD applications, ApplicationSets and projects, and with Flux CD Kustomizations and HelmReleases, from the same interface.
  - icon: ◉
    title: Private by design
    details: No telemetry or mandatory account in desktop mode. Your kubeconfig stays on your machine.
---

<div class="home-proof">
  <span>GO + WAILS + VUE</span>
  <span>WINDOWS · LINUX · macOS</span>
  <span>APACHE-2.0</span>
  <span>NO TELEMETRY</span>
</div>

## Kubernetes operations, without the clutter


Kubron brings everyday Kubernetes operations into one workspace across desktop and web: inspect cluster health, understand what changed, troubleshoot issues and take action without switching between a dozen tools.


Kubron is built around three jobs:

1. **Understand the state of a cluster and find the problem.** The [dashboard](/features/dashboard) shows health, problem Pods, capacity, blockers and expiring certificates.
2. **See what changed before it broke.** Recent rollouts, Helm updates, Argo CD syncs and Flux reconciles, plus events, in one timeline.
3. **Act without switching tools.** Logs, terminals, debug containers, scale, restart and rollback, Helm and GitOps actions, with a review step before changes. See the [typical scenarios](/guide/scenarios).

<div class="screenshot-grid">
  <figure>
    <img src="https://raw.githubusercontent.com/24DSO-lab/kubron/master/docs/images/screenshots/overview.png" alt="Kubron overview with cluster health, workload counts and resource signals" loading="lazy">
    <figcaption><strong>Cluster overview</strong><br>Health, capacity, blockers and recent changes at a glance.</figcaption>
  </figure>
  <figure>
    <img src="https://raw.githubusercontent.com/24DSO-lab/kubron/master/docs/images/screenshots/pods.png" alt="Kubron pods table with status and resource usage" loading="lazy">
    <figcaption><strong>Resource browser</strong><br>Inspect pods and workloads, then drill into the details.</figcaption>
  </figure>
  <figure>
    <img src="https://raw.githubusercontent.com/24DSO-lab/kubron/master/docs/images/screenshots/yaml-editor.png" alt="Kubron YAML editor with review diff before saving" loading="lazy">
    <figcaption><strong>YAML editor</strong><br>Review the diff before applying a manifest change.</figcaption>
  </figure>
  <figure>
    <img src="https://raw.githubusercontent.com/24DSO-lab/kubron/master/docs/images/screenshots/gitops-applications.png" alt="Kubron GitOps applications list" loading="lazy">
    <figcaption><strong>GitOps</strong><br>See application health and sync state in context.</figcaption>
  </figure>
</div>

## Two editions. One Kubernetes workspace.

### Desktop

Run Kubron locally on Windows, Linux or macOS. No server deployment or mandatory account is required; your kubeconfig stays on your machine.

[Get started with Desktop →](/guide/desktop)

### Web

Deploy Kubron in your own infrastructure and access it through a browser. Web adds multi-user accounts, roles, permissions and shared state backed by PostgreSQL. It is self-hosted, not a hosted SaaS service.

[Get started with Web →](/guide/web)

## Built for the way Kubernetes is operated

- **Multi-cluster workflows** — name and color-code clusters, tag environments, and choose one or several namespaces. Restricted RBAC is supported; partial access failures are surfaced.
- **Live resource views** — shared informers keep lists and the dashboard current without aggressive polling.
- **Broad resource coverage** — built-in Kubernetes resources plus automatically discovered CRDs.
- **Practical operations** — logs, exec, port-forward, workload scaling and restarts, node cordon and drain.
- **Helm lifecycle** — repositories (including OCI), chart discovery, dry-run install, upgrades, revision diffs and rollback.
- **Argo CD integration** — applications, ApplicationSets, projects, sync controls, live-vs-desired diff and resource graph.
- **Flux CD integration** — Kustomizations, HelmReleases, sources, notifications and image automation, with reconcile, suspend, resume and a chain from a source to the resources it applies. Flux has no diff or rollback in Kubron: see the [limits](/features/gitops#flux-cd).
- **Optional metrics** — charts for pods, workloads, nodes and volumes, plus network metrics of ingress-nginx controllers, from a small Prometheus stack that Kubron installs on request. It keeps two days of history and does not connect to an existing Prometheus. [Details](/features/metrics)

## Why teams choose Kubron

- **Light and native.** Go plus the system webview: no bundled browser engine, quick to start, easy on memory.
- **Private by design.** No telemetry, no mandatory account, and on Desktop your kubeconfig stays on your machine. The daily update check can be turned off, and problem reports are sent only on request.
- **Safe to operate.** Environment tags, a mandatory diff before saving YAML, and bulk actions that report failures instead of stopping.
- **Works with restricted access.** Restricted RBAC is a first-class case: no cluster-wide `list` required.
- **Signs in to your clusters.** OIDC, Azure AKS, AWS EKS and Google GKE are handled natively, and the Web server never runs commands from an uploaded kubeconfig.
- **Verifiable.** Release checksums and provenance, plus signed server images and chart with SBOMs.
- **Open and multilingual.** Apache-2.0, a public extension API, and an interface in English, Русский and 中文.

[What is Kubron? →](/guide/what-is-kubron)

## Built with Go, Wails and Vue


The desktop application is built with Go, Wails and Vue. Server mode serves the same UI over HTTP for multi-user teams, with PostgreSQL-backed state, encrypted credentials and support for multiple replicas. It is designed to run behind a TLS-terminating reverse proxy.


[Explore the Web edition →](/server/)

## Privacy and access control

Desktop does not require an account, sends no telemetry and keeps your kubeconfig on your machine. Kubron contacts GitHub once a day to check for a newer version (this can be turned off), and sends a problem report only when you press **Send** ([details](/features/updates)). Web adds local user accounts, fine-grained roles and permissions, session management and an audit log for shared environments.

[Read about security →](/features/security)

## Get started

Ready to connect a cluster?

- [Download the latest release](https://github.com/24DSO-lab/kubron/releases)
- [Choose Desktop or Web](/guide/getting-started)
- [Requirements and verification](/guide/installation)
- [Explore all features](/features/)
- [Read the source and contribute](https://github.com/24DSO-lab/kubron)

<div class="home-license">
  Kubron is open source under the Apache-2.0 license.
</div>
