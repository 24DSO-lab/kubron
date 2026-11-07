# Requirements, download and verification

Kubron comes in two editions, **Desktop** and **Web**. Pick the one that matches how you work, then follow the matching guide. This page holds the requirements, the download table and the verification steps shared by both.

| I want to… | Edition | Guide |
|---|---|---|
| Manage clusters from my own computer | **Desktop** | [Desktop edition](/guide/desktop) |
| Try the web interface on my machine | **Web, local trial** | [Web quick start](/server/quick-start) |
| Give a team browser access to clusters | **Web, production** | [Docker and Kubernetes deployment](/server/deploy) |
| Build Kubron myself or contribute | **From source** | [Build from source](/guide/build-from-source) |

## System requirements

### Desktop

| Platform | Supported | Also needed |
|---|---|---|
| Windows | Windows 10 and 11, 64-bit (x64) | Microsoft Edge **WebView2 Runtime** (preinstalled on Windows 11 and current Windows 10) |
| macOS | Apple Silicon and Intel (one universal build) | Nothing extra |
| Linux | 64-bit (x86-64) with a desktop environment | **GTK 3** and **WebKitGTK 4.1** (`libwebkit2gtk-4.1`) |

Common to all platforms:

- Network access from your machine to the Kubernetes API servers you want to manage.
- A kubeconfig (or credentials to build one). Kubron signs in to Azure AKS, AWS EKS and Google GKE itself. For any other `exec` credential plugin the command must be installed and reachable on your `PATH`, and you confirm it in Kubron before it runs.
- `kubectl` is **not** required. Kubron talks to the Kubernetes API itself.

::: info Linux builds
Release builds are produced and tested on Ubuntu 24.04. WebKitGTK 4.1 is also available on Debian 12. Arm64 builds for Linux and Windows are not published.
:::

### Web edition (server)

| Component | Requirement |
|---|---|
| PostgreSQL | **14 or newer.** Required for production and multi-user use. Migrations run automatically at startup |
| Encryption key | 32 random bytes in `id:base64` format, stored in your secret manager |
| TLS | A reverse proxy or ingress that terminates TLS. The server does not do it itself |
| Kubernetes (for the Helm chart) | **1.27 or newer** |
| Helm (for the chart) | Helm 3 with OCI support |
| Container runtime | Any OCI runtime for the published image (`linux`, non-root user `65532`) |

## Download

All desktop builds are published on the [Releases page](https://github.com/24DSO-lab/kubron/releases).

| Platform | File | Contents |
|---|---|---|
| Windows (x64) | `Kubron_v<version>_windows-amd64.zip` | `Kubron.exe` |
| macOS (universal) | `Kubron_v<version>_macos-universal.zip` | `Kubron.app` |
| Linux (x64) | `Kubron_v<version>_linux-amd64.tar.gz` | `Kubron` executable |
| Debian / Ubuntu (x64) | `kubron_<version>_amd64.deb` | Package with menu entry and icon |
| All | `SHA256SUMS.txt` | Checksums of every file |
| All | `kubron-sbom_v<version>_*.json` | Software bills of materials (CycloneDX / SPDX) |

For example, version 1.0.0 is `Kubron_v1.0.0_windows-amd64.zip` and `kubron_1.0.0_amd64.deb`. Note that the archive names include the `v` from the release tag, while the `.deb` file does not.

Release candidates (versions such as `1.1.0-rc.1`) are marked as pre-releases on GitHub. Use the latest non-pre-release version unless you are testing.

## Verify your download

Builds are not code-signed yet, so we recommend verifying files yourself. It takes under a minute.

### 1. Check the checksum

Download `SHA256SUMS.txt` into the same folder as your file.

::: code-group

```bash [Linux]
sha256sum --check --ignore-missing SHA256SUMS.txt
```

```bash [macOS]
shasum -a 256 --check --ignore-missing SHA256SUMS.txt
```

```powershell [Windows (PowerShell)]
(Get-FileHash .\Kubron_v1.0.0_windows-amd64.zip -Algorithm SHA256).Hash
# Compare the result with the matching line in SHA256SUMS.txt (case does not matter)
```

:::

You should see `OK` next to your file name. If you see `FAILED`, delete the file and download it again.

### 2. Check build provenance

Every desktop file carries a signed attestation that links it to the exact commit and the GitHub Actions run that built it. With the [GitHub CLI](https://cli.github.com/) installed:

```bash
gh attestation verify Kubron_v1.0.0_linux-amd64.tar.gz --repo 24DSO-lab/kubron
```

A successful run prints the workflow that produced the file. The same works for the `.deb` and for the Windows and macOS archives.

## About the unsigned-build warnings

Because the builds are not code-signed, your operating system will warn you the first time you run Kubron. This is expected. The warnings and how to proceed are covered per platform in [Desktop edition](/guide/desktop#troubleshooting). If you would rather not trust a binary at all, you can [build Kubron from source](/guide/build-from-source).

## Next steps

- [Desktop edition](/guide/desktop)
- [Add your first cluster](/guide/first-cluster)
- [Server quick start](/server/quick-start)
