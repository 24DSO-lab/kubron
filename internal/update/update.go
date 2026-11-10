// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

// Package update проверяет, вышла ли новая версия Kubron, определяет способ установки
// (десктоп: .deb, архив, macOS-приложение; веб: Helm, Kubernetes, Docker, бинарник)
// и говорит, как именно обновиться (ADR-0028).
//
// Пакет ничего не устанавливает и не заменяет сам: сервер в контейнере не должен менять
// собственный образ, а подмена исполняемого файла десктопа без проверки подписи — риск
// цепочки поставки. Результат — версия, ссылка на релиз и готовые команды или файл для
// способа установки, который определён.
//
// Единственный внешний запрос — GET /repos/{owner}/{repo}/releases/latest к GitHub API.
// Адрес не принимает пользовательских значений, поэтому защита от SSRF (ADR-0015) здесь не нужна.
package update

// DevVersion — версия сборки из исходников (go run, go build без -ldflags): проверка отключена,
// потому что сравнивать не с чем.
const DevVersion = "dev"

// Error — константная ошибка пакета. Её значение — стабильный код, который интерфейс переводит
// сам (update.error.<код>); текст сетевых и HTTP-ошибок наружу не уходит.
type Error string

func (e Error) Error() string { return string(e) }

const (
	// ErrNetwork — не удалось связаться с GitHub (нет сети, DNS, TLS, таймаут, запрет исходящих соединений).
	ErrNetwork Error = "network"
	// ErrRateLimited — исчерпан лимит запросов GitHub API для этого адреса.
	ErrRateLimited Error = "rate_limited"
	// ErrNotFound — у репозитория нет опубликованных релизов.
	ErrNotFound Error = "not_found"
	// ErrUnexpected — ответ не похож на описание релиза или не прошёл проверку.
	ErrUnexpected Error = "unexpected_response"
	// ErrLocked — проверка обновлений запрещена администратором (KUBRON_UPDATE_CHECK=off), менять её нельзя.
	ErrLocked Error = "locked"
	// ErrUnavailable — проверка обновлений в этой оболочке не подключена.
	ErrUnavailable Error = "unavailable"
)

// Target — оболочка, в которой запущено ядро.
type Target string

const (
	TargetDesktop Target = "desktop"
	TargetWeb     Target = "web"
)

// Method — способ установки.
type Method string

const (
	// MethodUnknown — определить не удалось: показываем только ссылку на релиз.
	MethodUnknown Method = "unknown"

	// Веб-сервер.

	MethodHelm       Method = "helm"       // чарт Helm (oci://ghcr.io/<владелец>/charts/kubron)
	MethodKubernetes Method = "kubernetes" // под в Kubernetes без Helm: манифесты, Kustomize, GitOps
	MethodDocker     Method = "docker"     // контейнер вне Kubernetes: docker run, Compose, Podman
	MethodBinary     Method = "binary"     // исполняемый файл на хосте (сборка из исходников)

	// Десктоп.

	MethodDeb     Method = "deb"       // пакет kubron_<версия>_amd64.deb (Debian, Ubuntu)
	MethodArchive Method = "archive"   // распакованный архив .zip или .tar.gz
	MethodMacApp  Method = "macos-app" // Kubron.app из архива для macOS
)

// Install — как и где установлена эта копия Kubron.
type Install struct {
	Target Target `json:"target"`
	Method Method `json:"method"`
	OS     string `json:"os"`
	Arch   string `json:"arch"`
	// Для Helm: имя релиза и пространство имён из значений чарта; пусто, если неизвестны.
	HelmRelease   string `json:"helmRelease,omitempty"`
	HelmNamespace string `json:"helmNamespace,omitempty"`
}

// Command — готовая команда или значение для способа установки. ID выбирает подпись в интерфейсе
// (update.command.<id>), Text копируется как есть. В Text попадают только проверенные значения:
// версия разобрана как SemVer, имена релиза, пространства имён и файла — по белому списку символов.
type Command struct {
	ID   string `json:"id"`
	Text string `json:"text"`
}

// Download — файл релиза для этой ОС, архитектуры и способа установки.
type Download struct {
	Name         string `json:"name"`
	URL          string `json:"url"`
	Size         int64  `json:"size,omitempty"`
	ChecksumsURL string `json:"checksumsUrl,omitempty"` // SHA256SUMS.txt того же релиза
}

// Version — версия сборки без обращения к сети. Её видит любой вошедший пользователь.
type Version struct {
	Version string `json:"version"`
	Target  Target `json:"target"`
}

// Info — состояние проверки обновлений. Подробности установки и команды видит только администратор
// (см. app.GetUpdateStatus).
type Info struct {
	Current string  `json:"current"`
	Install Install `json:"install"`

	// Locked — проверка запрещена администратором (KUBRON_UPDATE_CHECK=off): сервер не обращается к сети
	// совсем, ручная проверка недоступна, переключатель в интерфейсе скрыт.
	Locked bool `json:"locked"`
	// AutoCheck — фоновая проверка включена (на десктопе её переключает пользователь).
	AutoCheck bool `json:"autoCheck"`

	Latest      string `json:"latest,omitempty"`
	Available   bool   `json:"available"`
	ReleaseURL  string `json:"releaseUrl,omitempty"`
	PublishedAt string `json:"publishedAt,omitempty"` // RFC 3339
	CheckedAt   string `json:"checkedAt,omitempty"`   // последняя удачная проверка, RFC 3339
	// Error — код последней неудачной попытки (ErrNetwork и т. п.); пусто, если всё хорошо.
	Error string `json:"error,omitempty"`

	Download *Download `json:"download,omitempty"`
	Commands []Command `json:"commands,omitempty"`
}
