# Сборка из исходников

Собирайте Kubron сами, если хотите проверить то, что запускаете, нужна платформа без готовой сборки или вы участвуете в разработке. Из одного репозитория собирается и **Desktop-версию**, и **серверный бинарник**.

[[toc]]

## Что понадобится

| Инструмент | Версия | Примечания |
|---|---|---|
| [Go](https://go.dev/dl/) | Версия из `go.mod` (сейчас **1.27.3**) | Проверка: `go version` |
| [Node.js](https://nodejs.org/) | **22** (с npm) | Для интерфейса на Vue |
| [Wails CLI](https://wails.io/docs/gettingstarted/installation) | **v2**, та же версия, что в `go.mod` (сейчас v2.17.0) | Десктопная сборка и биндинги |
| Git | Любая актуальная | |

Установите Wails CLI ровно той версии, которую использует проект:

```bash
go install github.com/wailsapp/wails/v2/cmd/wails@v2.17.0
```

Убедитесь, что `$(go env GOPATH)/bin` есть в `PATH`, и выполните `wails doctor`, чтобы увидеть, чего не хватает в системе.

### Библиотеки платформы

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
# Установите Go, Node.js и Git (например, через winget).
# WebView2 Runtime уже есть в актуальной Windows 10 и в Windows 11.
# Компилятор C для десктопной сборки не нужен.
```

:::

## Получение исходников

```bash
git clone https://github.com/24DSO-lab/kubron.git
cd kubron
```

Один раз установите зависимости фронтенда:

```bash
cd frontend
npm ci
cd ..
```

## Desktop

### Запуск в режиме разработки

```bash
wails dev                    # Windows, macOS
wails dev -tags webkit2_41   # Linux
```

Приложение запускается с горячей перезагрузкой фронтенда и пересборкой Go-части при изменении файлов.

### Релизная сборка

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

Результат записывается в `build/bin/`:

| Платформа | Результат |
|---|---|
| Linux | `build/bin/Kubron` |
| macOS | `build/bin/Kubron.app` |
| Windows | `build/bin/Kubron.exe` |

В Linux флаг `-tags webkit2_41` обязателен: он выбирает WebKitGTK 4.1, ту же библиотеку, что и опубликованные сборки.

Wails собирает под ту платформу, на которой запущен. Чтобы получить все три, собирайте на каждой ОС, как это делает [workflow релиза](https://github.com/24DSO-lab/kubron/blob/master/.github/workflows/release.yml) проекта.

### Установка собранного

Следуйте [Desktop-версия](/ru/guide/desktop), подставив свой файл вместо скачанного. Шаг с контрольной суммой пропустите.

## Серверный бинарник {#server-binary}

Веб-редакция — программа на Go, раздающая тот же фронтенд. Wails CLI во время работы ей не нужен, но для сборки фронтенда нужны сгенерированные биндинги, поэтому первым шагом выполняется `wails generate module`.

```bash
# 1. Сгенерировать биндинги Go → TypeScript, которые импортирует фронтенд
wails generate module -tags webkit2_41     # в macOS и Windows без -tags

# 2. Собрать веб-фронтенд в frontend/dist
cd frontend
npm ci
npm run build:web
cd ..

# 3. Собрать сервер (фронтенд встраивается в бинарник)
go build -o kubron-server ./cmd/kubron-server
```

Запуск для локальной пробы:

```bash
./kubron-server --listen 127.0.0.1:8080
```

Дальше — [быстрый запуск сервера](/ru/server/quick-start). В Windows выходной файл называется `kubron-server.exe`.

### Сборка контейнерного образа

В репозитории есть многоэтапный `Dockerfile`, который выполняет шаги выше и даёт небольшой образ, работающий не от root:

```bash
docker build -t kubron:local .
```

В Helm-чарте укажите `image.repository=kubron` и `image.tag=local` либо сначала отправьте образ в свой реестр. См. [Продакшн-развёртывание](/ru/server/deploy).

## Запуск тестов

```bash
go test ./...                       # бэкенд (в Linux нужны dev-пакеты GTK/WebKit)
cd frontend && npm test             # модульные тесты фронтенда
```

Перед отправкой изменений прочитайте [руководство по участию](/ru/development/contributing). Коммитам нужна строка `Signed-off-by` (`git commit -s`).

## Если сборка не удалась

| Симптом | Решение |
|---|---|
| `wails: command not found` | Добавьте `$(go env GOPATH)/bin` в `PATH` |
| `Package webkit2gtk-4.0 was not found` (Linux) | Добавьте `-tags webkit2_41` и установите `libwebkit2gtk-4.1-dev` |
| `vue-tsc` падает из-за отсутствия модулей `wailsjs` | Выполните `wails generate module` перед `npm run build:web` |
| `go: ... requires go >= 1.27` | Установите версию Go из `go.mod` |
| Шаблон `frontend/dist` не находит файлов | Сначала соберите фронтенд (`npm run build:web`) |
