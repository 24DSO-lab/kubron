---
layout: home

hero:
  name: Kubron
  text: Fast, private Kubernetes desktop IDE
  tagline: Multi-cluster · Helm · GitOps · Logs, exec, port-forward · YAML editor. No telemetry. No mandatory account.
  image:
    src: /logo.svg
    alt: Kubron
  actions:
    - theme: brand
      text: Get Started
      link: /guide/getting-started
    - theme: alt
      text: Download
      link: https://github.com/24DSO-labs/kubron/releases
    - theme: alt
      text: GitHub
      link: https://github.com/24DSO-labs/kubron

features:
  - icon: ⚡
    title: Lightweight & native
    details: Built with Go and Wails. Uses the system webview — compact binary, fast startup, low memory.
  - icon: 🔒
    title: Private by default
    details: No telemetry, no mandatory account. Your kubeconfig never leaves your machine.
  - icon: 🖥
    title: Desktop + web
    details: Same UI as a desktop app or served over HTTP for teams (web mode is experimental).
  - icon: ⎈
    title: Helm & GitOps built-in
    details: Manage releases and Argo CD applications without extra plugins.
  - icon: 📦
    title: Multi-cluster
    details: Switch clusters in one window. Name, color, icon and environment tags (dev / test / stage / prod).
  - icon: 🛡
    title: Local users & audit
    details: Admin / operator / user roles, fine-grained permissions, audit log. Open core under Apache-2.0.
---

## Why Kubron

| | Kubron | Lens | k9s |
|---|---|---|---|
| UI | Desktop + web (WIP) | Desktop | Terminal |
| Built with | Go + Wails (native webview) | Electron | Go TUI |
| Argo CD / GitOps | Built in | Paid extension | Via plugins |
| Account required | No | Yes | No |
| Telemetry | None | Yes | None |
| License | Apache-2.0 | Proprietary + OSS | Apache-2.0 |

## Quick links

- [Getting Started](/guide/getting-started)
- [Installation](/guide/installation)
- [Features overview](/features/)
- [Server mode](/server/)
- [Contributing](/development/contributing)
- [Releases](https://github.com/24DSO-labs/kubron/releases)
