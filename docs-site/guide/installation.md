# Desktop installation

## Pre-built binaries

See [Desktop edition: download and launch](/guide/desktop).

### Linux dependencies

On Linux the desktop app needs GTK 3 and WebKitGTK 4.1:

```bash
# Debian / Ubuntu 24.04
sudo apt install libgtk-3-0 libwebkit2gtk-4.1-0

# Fedora
sudo dnf install gtk3 webkit2gtk4.1
```

Debian package installs the runtime dependencies automatically.

## Build from source

### Requirements

- [Go](https://go.dev/dl/) (version from `go.mod`)
- [Node.js](https://nodejs.org/) 22+
- [Wails CLI v2](https://wails.io/docs/gettingstarted/installation)
- On Linux: development packages

```bash
# Debian / Ubuntu
sudo apt install libgtk-3-dev libwebkit2gtk-4.1-dev
```

### Steps

```bash
git clone https://github.com/24DSO-labs/kubron.git
cd kubron

wails doctor                  # verify environment
wails dev                     # hot-reload development
wails build                   # production binary → build/bin
```

On Linux add the WebKit 4.1 tag (same as CI and releases):

```bash
wails dev -tags webkit2_41
wails build -tags webkit2_41
```
