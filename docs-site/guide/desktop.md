# Desktop edition

The Desktop edition runs Kubron directly on your computer. It is the quickest way to explore Kubernetes with Kubron without deploying or maintaining a server. This guide takes you from a downloaded file to a running Kubron on **Windows**, **macOS** or **Linux**.

Check the [system requirements](/guide/installation#system-requirements) first, and [verify your download](/guide/installation#verify-your-download) before you run it.

[[toc]]

## Download

Get the latest release from [GitHub Releases](https://github.com/24DSO-labs/kubron/releases):

| Platform | Package |
|---|---|
| Windows (x64) | `Kubron_v<version>_windows-amd64.zip` |
| macOS (Apple Silicon + Intel) | `Kubron_v<version>_macos-universal.zip` |
| Linux (x64) | `Kubron_v<version>_linux-amd64.tar.gz` |
| Linux (Debian/Ubuntu) | `kubron_<version>_amd64.deb` |

Checksums are published in `SHA256SUMS.txt`, and release files include build provenance. See [Verify your download](/guide/installation#verify-your-download).

## Windows

### Requirements

- Windows 10 or 11, 64-bit.
- Microsoft Edge WebView2 Runtime. It ships with Windows 11 and with up-to-date Windows 10. If it is missing, the app will not be able to draw its window; install the *Evergreen Runtime* from [Microsoft](https://developer.microsoft.com/microsoft-edge/webview2/) and start Kubron again.

### Steps

1. Download `Kubron_v<version>_windows-amd64.zip` from the [Releases page](https://github.com/24DSO-labs/kubron/releases).
2. *(Recommended)* Verify the file: [checksum and provenance](/guide/installation#verify-your-download).
3. Right-click the zip, open **Properties**, and tick **Unblock** if the option is shown. Or run in PowerShell:

   ```powershell
   Unblock-File .\Kubron_v1.0.0_windows-amd64.zip
   ```

4. Extract the archive. It contains a single file, `Kubron.exe`. Put it in a folder you control, for example `C:\Tools\Kubron\` or `%LOCALAPPDATA%\Programs\Kubron\`.
5. Double-click `Kubron.exe`.

   If **Windows protected your PC** (SmartScreen) appears, choose **More info → Run anyway**. This is expected for builds that are not code-signed yet.

6. *(Optional)* Right-click `Kubron.exe` → **Show more options → Pin to Start** or **Send to → Desktop (create shortcut)**.

There is no installer and nothing is written to the registry. Kubron stores its data in `%USERPROFILE%\.kubron`.

## macOS

### Requirements

- A Mac with Apple Silicon or an Intel processor. The download is one universal build.

### Steps

1. Download `Kubron_v<version>_macos-universal.zip`.
2. *(Recommended)* Verify the file: [checksum and provenance](/guide/installation#verify-your-download).
3. Double-click the zip to extract `Kubron.app`, then drag it into **Applications**.
4. The first launch will be blocked because the app is not notarized yet. Do **one** of the following:

   - In Finder, **right-click** (or Control-click) `Kubron.app` → **Open** → **Open** in the dialog.
   - Or remove the quarantine flag in Terminal:

     ```bash
     xattr -dr com.apple.quarantine /Applications/Kubron.app
     ```

   - Or, if macOS says the app *"cannot be opened"*, go to **System Settings → Privacy & Security**, scroll to the message about Kubron and click **Open Anyway**.

5. After the first successful launch, macOS remembers your choice and Kubron opens normally.

Kubron stores its data in `~/.kubron`.

## Linux

Kubron needs **GTK 3** and **WebKitGTK 4.1**. Pick the install method for your distribution.

### Debian, Ubuntu and derivatives (`.deb`)

The package declares its dependencies (`libgtk-3-0`, `libwebkit2gtk-4.1-0`), so `apt` installs everything.

```bash
sudo apt install ./kubron_1.0.0_amd64.deb
```

Use the path to the file you downloaded. Installing with `apt install ./file.deb` (not `dpkg -i`) resolves dependencies for you.

The package installs:

| Path | Purpose |
|---|---|
| `/usr/bin/kubron` | The application |
| `/usr/share/applications/kubron.desktop` | Application-menu entry (category *Development*) |
| `/usr/share/icons/hicolor/512x512/apps/kubron.png` | Icon |
| `/usr/share/doc/kubron/` | `LICENSE` and `NOTICE` |

Start it from your application menu or by running `kubron` in a terminal.

To update, install the newer `.deb` the same way. Your data in `~/.kubron` is untouched. To remove:

```bash
sudo apt remove kubron
```

### Other distributions (`.tar.gz`)

1. Install the runtime libraries:

   ::: code-group

   ```bash [Fedora / RHEL]
   sudo dnf install gtk3 webkit2gtk4.1
   ```

   ```bash [Arch / Manjaro]
   sudo pacman -S gtk3 webkit2gtk-4.1
   ```

   ```bash [openSUSE]
   sudo zypper install libgtk-3-0 libwebkit2gtk-4_1-0
   ```

   ```bash [Debian / Ubuntu]
   sudo apt install libgtk-3-0 libwebkit2gtk-4.1-0
   ```

   :::

   Package names differ slightly between distributions and versions. If a name is not found, search your package manager for `webkit2gtk` and pick the **4.1** variant.

2. Extract the archive. It contains one executable, `Kubron`:

   ```bash
   tar -xzf Kubron_v1.0.0_linux-amd64.tar.gz
   chmod +x Kubron
   ./Kubron
   ```

3. *(Optional)* Install it for your user so it is on your `PATH`:

   ```bash
   mkdir -p ~/.local/bin
   install -m 0755 Kubron ~/.local/bin/kubron
   ```

4. *(Optional)* Add a menu entry. Create `~/.local/share/applications/kubron.desktop`:

   ```ini
   [Desktop Entry]
   Type=Application
   Name=Kubron
   GenericName=Kubernetes Operations Workspace
   Comment=Workspace for Kubernetes operations across clusters
   Exec=%h/.local/bin/kubron
   Terminal=false
   Categories=Development;System;
   Keywords=kubernetes;k8s;cluster;kubectl;
   StartupWMClass=Kubron
   ```

   Some desktop environments do not expand `%h`. If the entry does not launch, write the absolute path instead, for example `/home/alex/.local/bin/kubron`.

To update, replace the executable with the new one. To uninstall, delete it and the `.desktop` file.

## First launch

On the first start Kubron has no users yet, so it asks you to create the local administrator account.

1. The **Welcome to Kubron** screen opens. Choose the interface language in the corner if you like. By default it follows your system language.
2. Enter a **username**: 3 to 32 characters, using Latin letters, digits, `.`, `-` and `_`.
3. Enter a **password** of at least 8 characters and confirm it.
4. Click **Create administrator**. You are signed in.

Afterwards Kubron shows a sign-in form when it starts. The account lives only on your computer; it is not an online account and nothing is sent anywhere.

::: tip Auto-logout
For safety, Kubron signs you out after a period of inactivity (15 minutes by default; an administrator can change it or turn it off). When it happens, open terminals, port-forwards and log streams are closed too.
:::

Next, [add your first cluster](/guide/first-cluster).

## Where Kubron keeps its data

Everything lives in one folder in your home directory:

| OS | Location |
|---|---|
| Linux, macOS | `~/.kubron` |
| Windows | `%USERPROFILE%\.kubron` |

| Item | Notes |
|---|---|
| `config.json` | Interface preferences such as theme and last active cluster |
| `users.json` | Local accounts |
| `clusters/` | Kubeconfigs of the clusters you added, plus their names, colours and tags |

Kubron creates the folder so only your user can enter it (`0700`) and writes its configuration with owner-only permissions. Your original kubeconfig (for example `~/.kube/config`) is not modified.

**Backup:** copy the whole folder while Kubron is closed. **Reset:** close Kubron and delete the folder; the next start behaves like a fresh install.

## Update and uninstall

| | Windows | macOS | Linux `.deb` | Linux `.tar.gz` |
|---|---|---|---|---|
| **Update** | Replace `Kubron.exe` | Replace `Kubron.app` in Applications | `sudo apt install ./new.deb` | Replace the `Kubron` file |
| **Uninstall** | Delete `Kubron.exe` | Drag `Kubron.app` to the Trash | `sudo apt remove kubron` | Delete the file |

Your clusters and account stay in `~/.kubron` through updates. Delete that folder only if you also want to remove your data. See the [changelog](https://github.com/24DSO-labs/kubron/blob/master/CHANGELOG.md) before upgrading across major versions.

## Troubleshooting

### Windows: "Windows protected your PC"
SmartScreen warns about apps without a code-signing certificate. Click **More info → Run anyway**. Verify the file first with the [checksum and provenance](/guide/installation#verify-your-download) steps if you want certainty.

### Windows: the window stays blank or the app closes immediately
Install or repair the **WebView2 Runtime** (see [Requirements](#requirements)). Also make sure the file was extracted from the zip; running `Kubron.exe` from inside the zip viewer can fail.

### macOS: "Kubron is damaged and can't be opened" or "cannot be opened because the developer cannot be verified"
This is Gatekeeper reacting to the quarantine flag. Use the right-click **Open** method or run `xattr -dr com.apple.quarantine /Applications/Kubron.app` as shown above.

### Linux: error about `libwebkit2gtk-4.1.so.0` or `libgtk-3.so.0`
A runtime library is missing. Install the packages from the steps above, then check what is still missing:

```bash
ldd ./Kubron | grep "not found"
```

If only `libwebkit2gtk-4.0` is available on your system (older distributions), your distribution is too old for the current builds. Use a newer release of the distribution or [build from source](/guide/build-from-source).

### Linux: the window opens but is blank or glitchy
Some GPU and Wayland combinations break WebKitGTK rendering. Try starting Kubron with the DMA-BUF renderer disabled:

```bash
WEBKIT_DISABLE_DMABUF_RENDERER=1 kubron
```

If that helps, add it to the `Exec=` line of your `.desktop` file as `env WEBKIT_DISABLE_DMABUF_RENDERER=1 kubron`.

### A cluster does not connect and the kubeconfig uses `exec`
Kubron signs in to Azure AKS (`kubelogin`), AWS EKS and Google GKE on its own. For any other `exec` credential plugin, Kubron shows the command, arguments and environment variable names and asks you to confirm before running it locally. The plugin must be installed and found on `PATH` by the app. Applications started from the macOS Dock or a Linux menu do not always inherit the `PATH` of your shell: start Kubron from a terminal to confirm, or give the kubeconfig the full path to the plugin command. Choosing **Always for this kubeconfig** is remembered in `exec-trust.json` in the [data folder](#where-kubron-keeps-its-data); delete that file to forget every decision. See [Connecting to Kubernetes](/features/cluster-auth).

### "Sign-in unavailable. The users file could not be read"
Kubron could not read `~/.kubron/users.json`, so it keeps the app closed rather than risk your data. Restore the file from a backup. See [Where Kubron keeps its data](#where-kubron-keeps-its-data).

### Still stuck?
Search or open an issue on [GitHub](https://github.com/24DSO-labs/kubron/issues/new/choose). Include your OS version, the Kubron version and what you tried.

## Connect a cluster

After the first launch, add a cluster from **Settings → Clusters** or the cluster switcher. Follow [Add your first cluster](/guide/first-cluster).

## Continue exploring

- [Product capabilities](/features/)
- [Keyboard shortcuts](/guide/shortcuts)
- [Choose another edition](/guide/getting-started)
