# Установка Desktop-версии

## Готовые сборки

Скачайте подходящий пакет со страницы [GitHub Releases](https://github.com/24DSO-labs/kubron/releases):

| Платформа | Пакет |
|---|---|
| Windows (x64) | `Kubron_<version>_windows-amd64.zip` |
| Linux (x64) | `Kubron_<version>_linux-amd64.tar.gz` |
| Linux (Debian/Ubuntu) | `kubron_<version>_amd64.deb` |
| macOS (Apple Silicon + Intel) | `Kubron_<version>_macos-universal.zip` |

Контрольные суммы находятся в `SHA256SUMS.txt`.

## Зависимости Linux

Desktop-приложению нужны GTK 3 и WebKitGTK 4.1:

```bash
# Debian / Ubuntu 24.04
sudo apt install libgtk-3-0 libwebkit2gtk-4.1-0

# Fedora
sudo dnf install gtk3 webkit2gtk4.1
```

Пакет Debian автоматически устанавливает runtime-зависимости.

## Сборка из исходников

Требуются [Go](https://go.dev/dl/) (версия из `go.mod`), [Node.js](https://nodejs.org/) 22+, [Wails CLI v2](https://wails.io/docs/gettingstarted/installation) и development-пакеты для Linux:

```bash
sudo apt install libgtk-3-dev libwebkit2gtk-4.1-dev
```

Склонируйте репозиторий и соберите приложение:

```bash
git clone https://github.com/24DSO-labs/kubron.git
cd kubron
wails doctor
wails dev
wails build
```

В Linux добавьте тег WebKitGTK 4.1:

```bash
wails dev -tags webkit2_41
wails build -tags webkit2_41
```

Вернуться к [инструкции по запуску Desktop](/ru/guide/desktop).
