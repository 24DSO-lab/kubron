# Getting Started

Kubron is a lightweight, open-source Kubernetes desktop IDE built with **Go** and **Wails**.

No telemetry, no mandatory account — your kubeconfig stays on your machine.

## 1. Download

Grab the latest release for your platform from the [Releases](https://github.com/24DSO-labs/kubron/releases) page:

| Platform | File |
|---|---|
| Windows (x64) | `Kubron_<version>_windows-amd64.zip` |
| Linux (x64) | `Kubron_<version>_linux-amd64.tar.gz` |
| Linux (Debian/Ubuntu) | `kubron_<version>_amd64.deb` |
| macOS (Apple Silicon + Intel) | `Kubron_<version>_macos-universal.zip` |

Checksums are in `SHA256SUMS.txt`. Releases also carry build provenance:

```bash
gh attestation verify <file> --repo 24DSO-labs/kubron
```

::: tip
Builds are not code-signed yet. On Windows SmartScreen may warn you. On macOS right-click → Open, or run:

```bash
xattr -dr com.apple.quarantine Kubron.app
```
:::

## 2. Run

Unzip / install and start the app. On first launch Kubron will ask you to import a kubeconfig (or pick the default `~/.kube/config`).

## 3. Next steps

- [Installation details](/guide/installation) — system dependencies, build from source
- [Add your first cluster](/guide/first-cluster)
- [Features overview](/features/)
- [Server mode](/server/) (multi-user)
