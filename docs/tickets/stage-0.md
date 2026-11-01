# Этап 0. Подготовка и разделение ядра

Цель: десктоп работает как раньше, но бизнес-логика не знает о Wails. Оценка ≈ 22 ч-д на одного разработчика (≈ 2 недели для двоих-троих; 0.3 уточнена после сверки с кодом: 3 → 2).
Порядок: 0.1 → 0.2 → (0.3, 0.4, 0.8 параллельно) ; 0.5–0.7 фронт параллельно с Go после 0.2; 0.9 независимо; 0.10 после 0.3 и 0.8.

Общее определение готовности: `gofmt`, `go vet`, `go test -tags $GO_TAGS ./...`, `npm test`, `wails build` зелёные; поведение десктопа не изменилось (ручной smoke: добавить кластер, список pod, YAML, логи, терминал).

---

## 0.1 ADR-0001…0013 в репозитории — 1 ч-д
- Файлы уже в `docs/adr/` (черновики). Сверить каждый с кодом ветки `refactor`, поправить, поменять статус на «принято».
- Приёмка: ADR утверждены тимлидом (ревью PR); README индекс актуален.
- Зависимости: нет.

## 0.2 Перенос `app*.go` в `internal/app` — 2 ч-д
- Переместить `app.go`, `app_auth.go` и остальные `app*.go` (+ тесты, например `app_audit_test.go`) в `internal/app`, пакет `app`. В `main.go` оставить `wails.Run` и `Bind: app`.
- Экспортируемые методы и их имена не менять (иначе сломаются биндинги `wailsjs`).
- Приёмка: `wails generate module` не меняет `wailsjs` (кроме путей), `wails build`, `go test ./...` зелёные.
- Риск: `go:embed` frontend/dist остаётся в `main.go`.

## 0.3 Интерфейс Platform — 2 ч-д
- Интерфейс `Platform { Emit(event string, data ...any); PickFile(opts) (string, error); OpenURL(url string); Capabilities() Capabilities }` в `internal/app`; реализация Wails — в `main` (или `internal/platform/wails`).
- Убрать из ядра: `a.ctx` и `EventsEmit` (`startup`, `app.go:~40`), `OpenFileDialog` (`~69`), `BrowserOpenURL` (`~1097`). `GetFreePort` не зависит от Wails, но остаётся только в десктопе (флаг capability).
- Использовать `Manager.SetNotifier`, который уже есть.
- Приёмка: в `internal/app` нет импортов `github.com/wailsapp/wails/v2` — проверка depguard в golangci-lint или тест на импорты.

## 0.4 `policy_test.go` под новый путь — 1 ч-д
- `internal/auth/policy_test.go` (строки ~126, ~136) читает `../../app*.go` через go/ast; обновить путь на `../app/*.go`.
- Приёмка: временно убранный `authz` у метода валит тест; публичные методы (7 шт.) остаются в явном списке исключений.

## 0.5 Фронт: прямые импорты биндингов → `platform/api.ts` — 3 ч-д
- 16 файлов импортируют `wailsjs/go/main/App` (`useK8s`, `useControlledPods`, `actions`, `useTabbedPanel`, `YamlView`, `TerminalView`, `LogsView`, `ResourceEventsTable`, `EndpointSlicesTab`, `PortForwardControl`, `useClusters`, `useAppSettings` и др.; точный список — `grep -rl "wailsjs/go/main/App" frontend/src`). Свести к `platform/api.ts` поверх существующего `callApp` (`frontend/src/resources/wails.ts`).
- Приёмка: прямых импортов нет; ESLint `no-restricted-imports` включён в CI; `npm test` зелёный.

## 0.6 Фронт: `wailsjs/runtime` → `platform/events.ts`, `platform/files.ts` — 2 ч-д
- 7 файлов используют `wailsjs/runtime` (события, открытие URL). Вынести подписку/отписку на события, открытие ссылки и выбор файла.
- Приёмка: то же ESLint-правило; десктопный smoke зелёный.

## 0.7 `GetCapabilities()` и `useCapabilities` — 1 ч-д
- Флаги: `fileDialog`, `portForward`, `nodeShell`, `localKubeconfig`, `sso`. Метод в `App` (с `authz`/в списке публичных — решить), хук на фронте.
- Приёмка: в десктопе все флаги `true`, поведение не изменилось; тест хука.

## 0.8 Интерфейс Store поверх файловых реализаций — 2 ч-д
- Интерфейсы для users, токенов Argo, аудита, kubeconfig в `internal/store`; текущая работа с файлами (`~/.kubron`, `GetKubronDir`) — реализация `filestore`.
- Приёмка: тесты `internal/auth` и `internal/audit` проходят без изменения логики.

## 0.9 Лицензия и CI-проверки — 2 ч-д
- `LICENSE` (Apache-2.0), `NOTICE`, заголовки в исходниках (по политике команды), `CONTRIBUTING.md` с DCO (`Signed-off-by`), проверка DCO в CI.
- Шаги CI: `go-licenses check` (Go), `license-checker` (npm) со списком разрешённых лицензий.
- В README коротко и честно: ядро Apache-2.0, SSO/OIDC планируется как платный модуль; локальные пользователи, роли и аудит остаются в ядре.
- Приёмка: CI падает на зависимости с несовместимой лицензией; файлы лежат в репозитории. До публичного релиза — показать лицензию и ключ юристу.

## 0.10 Публичный пакет расширений `pkg/ext` — 3 ч-д
- Интерфейсы `Authenticator`, `Store`, `Platform` и реестр реализаций (код в `internal/` из другого Go-модуля не импортируется).
- Приёмка: тестовый модуль в отдельном Go-модуле регистрирует свой `Authenticator` и собирается без правок в ядре. Интерфейс `Authenticator` помечен как «нестабильный» до этапа 6.
- Сделано: `pkg/ext` — отдельный Go-модуль `github.com/24DSO-labs/kubron/pkg/ext` только на stdlib (подключён `replace` в корневом `go.mod`): `Authenticator` (пароль / перенаправление), `Platform`, `Store` (`UserBackend`, `AuditBackend`, `TokenStore`), `Registry` и `Module` с проверкой `APIVersion`. Внутренние `app.Platform`, `audit.Backend`, `k8s.TokenStore` — псевдонимы публичных интерфейсов; `auth.FromExt` и `store.FromExt` адаптируют внешние хранилища. Пример внешнего модуля — `examples/ext-authenticator` (собирается и тестируется в CI отдельно).

---

Итого: 1+2+2+1+3+2+1+2+2+3 = 19 ч-д плюс запас на ревью и регрессии (≈ 22).
