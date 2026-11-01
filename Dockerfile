# syntax=docker/dockerfile:1.7
#
# Образ веб-сервера Kubron (cmd/kubron-server). Описание и запуск: docs/deploy.md.
#
#   docker build -t kubron:dev --build-arg VERSION=0.3.0 .
#
# Стадии:
#   bindings — генерирует frontend/wailsjs (`wails generate module`). Wails компилирует корневой
#              пакет, а он линкуется с GTK/WebKit, поэтому стадии нужны libgtk-3-dev и
#              libwebkit2gtk-4.1-dev. Это ровно то, что делает job `e2e` в .github/workflows/ci.yml.
#   frontend — `npm ci && npm run build:web` (vue-tsc требует биндинги из предыдущей стадии).
#   build    — `CGO_ENABLED=0 go build`. cmd/kubron-server не импортирует Wails (он нужен только
#              корневому main.go и internal/platform/wails), поэтому ни GTK, ни тег webkit2_41
#              серверу не нужны, а бинарник получается статическим.
#   final    — distroless static, non-root: в образе только бинарник и лицензии.

ARG GO_VERSION=1.27.3
ARG NODE_VERSION=22

# ── bindings ───────────────────────────────────────────────────────────────
FROM golang:${GO_VERSION}-bookworm AS bindings
# Версию Go задаёт go.mod: молча скачивать другой тулчейн нельзя, сборка должна воспроизводиться
ENV GOTOOLCHAIN=local
RUN apt-get update \
 && apt-get install -y --no-install-recommends libgtk-3-dev libwebkit2gtk-4.1-dev \
 && rm -rf /var/lib/apt/lists/*

WORKDIR /src
# Модули скачиваются отдельным слоем: он не сбрасывается правками исходников.
# pkg/ext — отдельный модуль, подключённый через replace в go.mod.
COPY go.mod go.sum ./
COPY pkg/ext/go.mod ./pkg/ext/
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

# Версия Wails CLI берётся из go.mod, как в CI: CLI и библиотека не должны расходиться
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go install github.com/wailsapp/wails/v2/cmd/wails@"$(go list -m -f '{{.Version}}' github.com/wailsapp/wails/v2)"

COPY . .
# main.go встраивает frontend/dist (//go:embed): для генерации достаточно заглушки
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    mkdir -p frontend/dist && touch frontend/dist/.keep \
 && /go/bin/wails generate module -tags webkit2_41 \
 && test -f frontend/wailsjs/go/models.ts

# ── frontend ───────────────────────────────────────────────────────────────
FROM node:${NODE_VERSION}-bookworm-slim AS frontend
WORKDIR /src/frontend
COPY frontend/package.json frontend/package-lock.json ./
RUN --mount=type=cache,target=/root/.npm \
    npm ci
COPY frontend/ ./
COPY --from=bindings /src/frontend/wailsjs ./wailsjs
RUN npm run build:web \
 && grep -q 'name="kubron-target" content="web"' dist/index.html

# ── build ──────────────────────────────────────────────────────────────────
FROM golang:${GO_VERSION}-bookworm AS build
ENV GOTOOLCHAIN=local \
    CGO_ENABLED=0
WORKDIR /src
COPY go.mod go.sum ./
COPY pkg/ext/go.mod ./pkg/ext/
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

COPY . .
COPY --from=frontend /src/frontend/dist ./frontend/dist
ARG VERSION=dev
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/kubron-server ./cmd/kubron-server

# ── final ──────────────────────────────────────────────────────────────────
FROM gcr.io/distroless/static-debian12:nonroot

ARG VERSION=dev
ARG REVISION=unknown
LABEL org.opencontainers.image.title="kubron-server" \
      org.opencontainers.image.description="Kubron web server: Kubernetes management in the browser" \
      org.opencontainers.image.source="https://github.com/24DSO-labs/kubron" \
      org.opencontainers.image.licenses="Apache-2.0" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.revision="${REVISION}"

COPY --from=build /out/kubron-server /kubron-server
COPY --from=build /src/LICENSE /src/NOTICE /licenses/

# Helm и клиентские библиотеки Kubernetes пишут кэш и конфигурацию в HOME и XDG_*, а Helm ещё и во
# временные файлы. При readOnlyRootFilesystem эти каталоги монтируются как emptyDir/tmpfs
# (docs/deploy.md): /home/nonroot и /tmp. Пути заданы явно, чтобы монтировать нужно было ровно их.
ENV HOME=/home/nonroot \
    XDG_CONFIG_HOME=/home/nonroot/.config \
    XDG_CACHE_HOME=/home/nonroot/.cache \
    XDG_DATA_HOME=/home/nonroot/.local/share \
    TMPDIR=/tmp \
    KUBRON_LISTEN=0.0.0.0:8080

# Не loopback-адрес сервер примет только с KUBRON_ALLOW_REMOTE=1 (internal/server/config.go):
# сам он TLS не терминирует, и согласие на сетевой доступ должно быть явным, поэтому в образ
# оно не вшито. Перед сервером нужен обратный прокси с TLS.
EXPOSE 8080
USER 65532:65532
ENTRYPOINT ["/kubron-server"]
