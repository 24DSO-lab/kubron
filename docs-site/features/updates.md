# Version and updates

Kubron shows its version, checks once a day whether a newer release exists, and tells administrators exactly how to update for the way Kubron was installed. **Kubron does not install updates itself:** it shows the steps, and you run them.

[[toc]]

## Where to see the version

The current version is shown in the header. Every signed-in user sees it, which makes it easy to quote in a support request. When a newer release exists, administrators see **Update available: 1.3.0** there and a banner: **Kubron 1.3.0 is available (installed: 1.2.0)**. Choose **How to update** to open the **Version and updates** dialog, or **Hide until the next version** to dismiss the banner. The dismissal is remembered in your browser or window and applies until a newer version appears.

Details about updates are shown to **administrators** only (the `users.manage` permission), because the installation method and the Helm release name describe your deployment. Other users see just the version.

## How the check works

- Once a day, Kubron asks GitHub (`api.github.com`) for the **latest stable release** of the project. Drafts and pre-releases are ignored.
- The first check runs about 30 seconds after start, so startup never waits for the network. After a failure, Kubron retries after an hour.
- **Check for updates** in the dialog runs a check at once. It is available to administrators and is limited to once a minute; within that minute you see the last known result.
- Requests are conditional (`ETag`), so an unchanged answer does not use GitHub's request limit, and Kubron sends no token.
- A development build (built from source without a version) is never checked, because there is nothing to compare with. The dialog says so.

The only server Kubron contacts for this is GitHub. GitHub sees your IP address and a `User-Agent` that contains the Kubron version. No information about your clusters or users is sent.

## The Version and updates dialog

| Field | Meaning |
|---|---|
| **Installed version** | The version you run |
| **Installation** | How this copy was installed (see below) |
| **Latest version** | The newest stable release |
| **Last checked** | When the last check completed |

Under **How to update** the dialog shows the steps for your installation: a **Download** button for desktop files, commands with a **Copy** button, and **Release notes on GitHub**. For desktop downloads, it also links to `SHA256SUMS.txt`, so you can [verify the file](/guide/installation#verify-your-download).

## Steps for each installation

| Installation | What the dialog shows |
|---|---|
| **Helm chart** | `helm upgrade <release> oci://ghcr.io/24dso-lab/charts/kubron --version <new> --namespace <namespace> --reset-then-reuse-values`. Your current values are kept and the chart's new defaults are applied. The release name and namespace come from the chart (Helm 3.14 or newer is needed for `--reset-then-reuse-values`) |
| **Kubernetes (without Helm)** | The new image tag, `ghcr.io/24dso-lab/kubron:<new>`: change it in your manifests, Kustomize overlay or GitOps repository and apply it |
| **Container (Docker, Podman)** | `docker pull ghcr.io/24dso-lab/kubron:<new>` and `docker compose pull && docker compose up -d` |
| **Server binary** | Replace the binary with the new build and restart it. The dialog links to the release |
| **DEB package** | Download the `.deb` and run `sudo apt install ./kubron_<new>_amd64.deb`. The app uses the new version after a restart |
| **Archive** (Windows `.zip`, Linux `.tar.gz`) | Download the new archive and replace the files of the current installation |
| **macOS application** | Download the new archive and replace `Kubron.app` in Applications |
| **Unknown** | The dialog could not tell how Kubron was installed, so it shows a link to the release page rather than a wrong command |

Before upgrading a Web server, back it up and read the [upgrade notes](/server/deploy#upgrade).

### How the installation is detected

- **Helm chart:** the chart tells the server (`KUBRON_INSTALL_METHOD=helm`, with the release name and namespace).
- **Kubernetes without Helm** and **containers:** the server recognizes a Pod or a container from its environment.
- **DEB package:** Kubron checks that the file belongs to the installed `kubron` package.
- If the automatic detection is wrong, for example when an image is started by your own orchestrator, set `KUBRON_INSTALL_METHOD` to `helm`, `kubernetes`, `docker` or `binary`.

## Turning the check off

| Edition | How |
|---|---|
| **Desktop** | In the dialog, clear **Check for updates automatically once a day**. **Check for updates** still works on demand. The choice is saved in `updates.json` in the [data folder](/guide/desktop#where-kubron-keeps-its-data) |
| **Web** | Set `KUBRON_UPDATE_CHECK=off`, or `kubron.updateCheck: false` in the Helm chart. The server then makes no requests to GitHub, and the dialog says that the administrator turned the check off. Any value other than `on` and `off` stops the server from starting, so a typo cannot leave the check enabled by accident |

Turn it off in air-gapped networks. In the Web edition, the user interface cannot change this setting.

## Network access {#network-access}

The check needs HTTPS to `api.github.com`. In the Helm chart the default egress rule for API servers allows port 443 to any address, so GitHub is reachable. If you narrowed `networkPolicy.egress.clusterApi.cidrs`, add a rule for `api.github.com` to `networkPolicy.egress.extra`, or turn the check off. Otherwise the dialog shows a network error. See [Docker and Kubernetes deployment](/server/deploy#update-check).

## Messages

| Message | Meaning |
|---|---|
| **Could not reach GitHub. Check the network connection.** | No network path to `api.github.com`: proxy, firewall or `NetworkPolicy` |
| **GitHub request limit exceeded. Try again later.** | GitHub's limit for your address is used up. Requests are unauthenticated and limited per address, and the daily check rarely hits it |
| **No releases have been published yet.** | The repository has no stable release |
| **GitHub returned an unexpected response.** | The answer did not look like a release |
| **Update checks are turned off by the administrator of this installation.** | `KUBRON_UPDATE_CHECK=off` |
| **This is a development build: update checks are disabled.** | A build without a release version |
| **Details about updates are available to administrators.** | You are not an administrator |
