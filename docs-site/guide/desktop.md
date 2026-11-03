# Desktop edition

The Desktop edition runs Kubron directly on your computer. It is the quickest way to explore Kubernetes with Kubron without deploying or maintaining a server.

## Download

Get the latest release from [GitHub Releases](https://github.com/24DSO-labs/kubron/releases):

| Platform | Package |
|---|---|
| Windows (x64) | `Kubron_<version>_windows-amd64.zip` |
| Linux (x64) | `Kubron_<version>_linux-amd64.tar.gz` |
| Linux (Debian/Ubuntu) | `kubron_<version>_amd64.deb` |
| macOS (Apple Silicon + Intel) | `Kubron_<version>_macos-universal.zip` |

Checksums are published in `SHA256SUMS.txt`. Release artifacts also include build provenance.

## Install and launch

1. Download the package for your operating system.
2. Unpack the archive or install the package.
3. Start Kubron.
4. On first launch, import a kubeconfig file or use the default `~/.kube/config`.

On Linux, the desktop application requires GTK 3 and WebKitGTK 4.1. Debian packages install runtime dependencies automatically. See [installation details](/guide/installation) for package names and build-from-source instructions.

::: tip Platform security prompts
Builds are not code-signed yet. Windows SmartScreen may show a warning. On macOS, use Finder's **Open** action from the context menu if Gatekeeper blocks the app.
:::

## Connect a cluster

After launch, add a cluster from **Settings → Clusters** or the cluster switcher. Follow [Add your first cluster](/guide/first-cluster).

## Continue exploring

- [Product capabilities](/features/)
- [Keyboard shortcuts](/guide/shortcuts)
- [Choose another edition](/guide/getting-started)
