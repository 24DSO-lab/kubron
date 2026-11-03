---
layout: home

hero:
  name: Kubron
  text: Kubernetes, under control.
  tagline: A Kubernetes Operations Workspace for Desktop and self-hosted Web. Manage clusters, workloads, Helm and GitOps from one focused workspace.
  actions:
    - theme: brand
      text: Download Kubron
      link: https://github.com/24DSO-labs/kubron/releases
    - theme: alt
      text: Get started
      link: /guide/getting-started
    - theme: alt
      text: View on GitHub
      link: https://github.com/24DSO-labs/kubron

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
    details: Work with Argo CD applications, ApplicationSets and projects, including sync, diff and resource graphs.
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

Kubron brings everyday Kubernetes operations into one workspace. Choose a native Desktop application for individual work or a self-hosted Web edition for teams.

<div class="screenshot-grid">
  <figure>
    <img src="https://raw.githubusercontent.com/24DSO-labs/kubron/master/docs/images/screenshots/overview.png" alt="Kubron overview with cluster health, workload counts and resource signals" loading="lazy">
    <figcaption><strong>Cluster overview</strong><br>Health, capacity, blockers and recent changes at a glance.</figcaption>
  </figure>
  <figure>
    <img src="https://raw.githubusercontent.com/24DSO-labs/kubron/master/docs/images/screenshots/pods.png" alt="Kubron pods table with status and resource usage" loading="lazy">
    <figcaption><strong>Resource browser</strong><br>Inspect pods and workloads, then drill into the details.</figcaption>
  </figure>
  <figure>
    <img src="https://raw.githubusercontent.com/24DSO-labs/kubron/master/docs/images/screenshots/yaml-editor.png" alt="Kubron YAML editor with review diff before saving" loading="lazy">
    <figcaption><strong>YAML editor</strong><br>Review the diff before applying a manifest change.</figcaption>
  </figure>
  <figure>
    <img src="https://raw.githubusercontent.com/24DSO-labs/kubron/master/docs/images/screenshots/gitops-applications.png" alt="Kubron GitOps applications list" loading="lazy">
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
- **Optional metrics** — charts for pods, workloads, nodes, PVCs and ingresses when the metrics stack is installed.

## Built with Go, Wails and Vue

Both editions share the Kubron interface and core Kubernetes workflows. The Web edition adds centralized, multi-user operation on infrastructure you control.

[Explore the Web edition →](/server/)

## Privacy and access control

Desktop does not require an account, sends no telemetry and keeps your kubeconfig on your machine. Web adds local user accounts, fine-grained roles and permissions, session management and an audit log for shared environments.

[Read about security →](/features/security)

## Get started

Ready to connect a cluster?

- [Download the latest release](https://github.com/24DSO-labs/kubron/releases)
- [Follow the getting-started guide](/guide/getting-started)
- [Explore all features](/features/)
- [Read the source and contribute](https://github.com/24DSO-labs/kubron)

<div class="home-license">
  Kubron is open source under the Apache-2.0 license.
</div>
