# Build from source

Build Kubron yourself if you want to audit what you run, use a platform without a published build, or contribute. You can build the **Desktop edition** and the **server binary** from the same repository.

[[toc]]

## Prerequisites

| Tool | Version | Notes |
|---|---|---|
| [Go](https://go.dev/dl/) | The version in `go.mod` (currently **1.27.3**) | Check with `go version` |
| [Node.js](https://nodejs.org/) | **22** (with npm) | Used for the Vue frontend |
| [Wails CLI](https://wails.io/docs/gettingstarted/installation) | **v2**, same version as `go.mod` (currently v2.17.0) | Desktop build and bindings |
| Git | Any recent version | |

Install the Wails CLI at exactly the version the project uses:

```bash
go install github.com/wailsapp/wails/v2/cmd/wails@v2.17.0
```

Make sure `$(go env GOPATH)/bin` is on your `PATH`, then run `wails doctor` to see what your system is missing.

### Platform libraries

::: code-group

```bash [Ubuntu / Debian]
sudo apt install build-essential libgtk-3-dev libwebkit2gtk-4.1-dev
```

```bash [Fedora]
sudo dnf install gcc gtk3-devel webkit2gtk4.1-devel
```

```bash [macOS]
xcode-select --install
```

```powershell [Windows]
# Install Go, Node.js and Git (for example with winget).
# WebView2 Runtime is already present on current Windows 10 and 11.
# A C compiler is not required for the desktop build.
```

:::

## Get the source

```bash
git clone https://github.com/24DSO-lab/kubron.git
cd kubron
```

Install the frontend dependencies once:

```bash
cd frontend
npm ci
cd ..
```

## Desktop

### Run in development mode

```bash
wails dev            # Windows, macOS
wails dev -tags webkit2_41   # Linux
```

This starts the app with hot reload for the frontend and rebuilds the Go side when files change.

### Produce a release build

::: code-group

```bash [Linux]
wails build -clean -platform linux/amd64 -tags webkit2_41
```

```bash [macOS]
wails build -clean -platform darwin/universal
```

```powershell [Windows]
wails build -clean -platform windows/amd64
```

:::

The result is written to `build/bin/`:

| Platform | Output |
|---|---|
| Linux | `build/bin/Kubron` |
| macOS | `build/bin/Kubron.app` |
| Windows | `build/bin/Kubron.exe` |

On Linux the `-tags webkit2_41` flag is required: it selects WebKitGTK 4.1, the same library the published builds use.

Wails builds for the platform you run on. To produce all three, build on each OS, as the project's [release workflow](https://github.com/24DSO-lab/kubron/blob/master/.github/workflows/release.yml) does.

### Install what you built

Follow [Desktop edition](/guide/desktop) and use your own file in place of the download. Skip the checksum step.

## Server binary

The web edition is a Go program that serves the same frontend. It does not need the Wails CLI at runtime, but the frontend build needs the generated bindings, so the steps below run `wails generate module` first.

```bash
# 1. Generate the Go-to-TypeScript bindings the frontend imports
wails generate module -tags webkit2_41     # on macOS and Windows omit -tags

# 2. Build the web frontend into frontend/dist
cd frontend
npm ci
npm run build:web
cd ..

# 3. Build the server (the frontend is embedded in the binary)
go build -o kubron-server ./cmd/kubron-server
```

Start it for a local trial:

```bash
./kubron-server --listen 127.0.0.1:8080
```

Then continue with the [server quick start](/server/quick-start). On Windows the output file is `kubron-server.exe`.

### Build the container image

The repository includes a multi-stage `Dockerfile` that performs the steps above and produces a small non-root image:

```bash
docker build -t kubron:local .
```

Use `image.repository=kubron` and `image.tag=local` in the Helm chart, or push the image to your own registry first. See [Production deployment](/server/deploy).

## Run the tests

```bash
go test ./...                       # backend (Linux needs the GTK/WebKit dev packages)
cd frontend && npm test             # frontend unit tests
```

Before sending a change, read the [contribution guide](/development/contributing). Commits need a `Signed-off-by` line (`git commit -s`).

## Troubleshooting a build

| Symptom | Fix |
|---|---|
| `wails: command not found` | Add `$(go env GOPATH)/bin` to `PATH` |
| `Package webkit2gtk-4.0 was not found` (Linux) | Add `-tags webkit2_41` and install `libwebkit2gtk-4.1-dev` |
| `vue-tsc` fails with missing `wailsjs` modules | Run `wails generate module` before `npm run build:web` |
| `go: ... requires go >= 1.27` | Install the Go version from `go.mod` |
| `frontend/dist` pattern matches no files | Build the frontend first (`npm run build:web`) |
