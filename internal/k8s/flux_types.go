// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package k8s

// Контракты Flux CD для UI (ADR-0023). Объекты читаются как unstructured через dynamic client, модули fluxcd/* не
// подключаются: DTO — единственная граница между чужими версиями CRD и интерфейсом. Парсеры защитные:
// отсутствующее или неожиданное поле даёт нулевое значение, а не панику.
//
// Во все DTO НЕ попадают (раздел 4 ADR): spec.values и содержимое valuesFrom, значения spec.postBuild.substitute,
// секреты kubeConfig и decryption (допустимо только имя), userinfo в URL, аннотация
// kubectl.kubernetes.io/last-applied-configuration, путь и query в spec.address у Provider, status.webhookPath
// у Receiver. Тексты сообщений контроллеров проходят redactFluxMessage. Новое поле DTO добавляется только вместе
// с проверкой в TestFluxNoSecretLeak.
//
// Зеркало на фронтенде — frontend/src/types/flux.ts (имена полей совпадают с JSON).

const (
	// EventFluxChanged — событие фронтенду: объект Flux создан, изменён или удалён.
	// Отдельное от cluster:changed, чтобы списки Flux не перезагружались от любых изменений в кластере.
	EventFluxChanged = "flux:changed"
)

// FluxState — итоговое состояние объекта, вычисляется в Go (FluxStateOf); фронтенд conditions для статуса не разбирает.
type FluxState string

// Порядок констант совпадает с приоритетом: Suspended > Stalled > Failed > Reconciling > Ready > Unknown.
const (
	FluxStateSuspended   FluxState = "Suspended"   // spec.suspend: true
	FluxStateStalled     FluxState = "Stalled"     // condition Stalled=True: контроллер не будет повторять попытку сам
	FluxStateFailed      FluxState = "Failed"      // Ready=False и объект не в процессе reconcile
	FluxStateReconciling FluxState = "Reconciling" // condition Reconciling=True
	FluxStateReady       FluxState = "Ready"       // Ready=True
	FluxStateUnknown     FluxState = "Unknown"     // условий нет или статус неопределён
)

// Значения FluxStatus.State.
const (
	FluxStatusOK                 = "ok"
	FluxStatusUnsupportedVersion = "unsupportedVersion" // CRD есть, но обслуживаются только устаревшие версии API
)

// FluxControllers — какие контроллеры Flux присутствуют в кластере. Flux можно поставить частично (--components),
// поэтому вкладки без своего контроллера скрываются.
type FluxControllers struct {
	Kustomize    bool `json:"kustomize"`
	Helm         bool `json:"helm"`
	Source       bool `json:"source"`
	Notification bool `json:"notification"`
	Image        bool `json:"image"`
}

// FluxStatus — результат обнаружения Flux в активном кластере (один вызов discovery, короткий TTL).
type FluxStatus struct {
	// Installed — раздел Flux показывается: есть контроллер kustomize или helm.
	Installed bool `json:"installed"`
	// State — FluxStatusOK или FluxStatusUnsupportedVersion; пусто, если Flux не установлен.
	State string `json:"state"`
	// Controllers — присутствие контроллеров.
	Controllers FluxControllers `json:"controllers"`
	// Versions — обслуживаемая версия API по виду: «Kustomization» → «v1». Только для найденных видов.
	Versions map[string]string `json:"versions"`
}

// FluxRef — ссылка на объект Flux. У dependsOn вид не указывается (там только имя и namespace): Kind пуст.
type FluxRef struct {
	Kind      string `json:"kind"`
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
}

// FluxActionResult — результат действия над одним объектом. Одна ошибка не отменяет остальные (как у Argo).
type FluxActionResult struct {
	Kind      string `json:"kind"`
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	Error     string `json:"error"` // пусто — успех
}

// FluxObjectInfo — общая часть всех объектов Flux. Встраивается в DTO видов; в JSON поля идут плоско.
type FluxObjectInfo struct {
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
	Age       string `json:"age"`

	Interval  string    `json:"interval"`  // spec.interval
	Suspended bool      `json:"suspended"` // spec.suspend
	State     FluxState `json:"state"`     // см. FluxStateOf

	SourceRef  *FluxRef        `json:"sourceRef"`  // spec.sourceRef / spec.chartRef; nil — нет
	DependsOn  []FluxRef       `json:"dependsOn"`  // spec.dependsOn (вид пуст)
	Conditions []ConditionInfo `json:"conditions"` // сообщения проходят redactFluxMessage

	Revision              string `json:"revision"`              // status.lastAppliedRevision; у источников — ревизия артефакта
	LastAttemptedRevision string `json:"lastAttemptedRevision"` // status.lastAttemptedRevision
	LastReconcileAt       string `json:"lastReconcileAt"`       // время последнего reconcile, RFC3339; пусто — не было
	Message               string `json:"message"`               // сообщение условия Ready, после redactFluxMessage

	// Labels и Annotations — без kubectl.kubernetes.io/last-applied-configuration.
	Labels      map[string]string `json:"labels"`
	Annotations map[string]string `json:"annotations"`
	Created     string            `json:"created"`
}

// FluxKustomizationInfo — Kustomization (kustomize.toolkit.fluxcd.io/v1).
// Не отдаются: spec.postBuild.substitute и substituteFrom, секреты spec.kubeConfig и spec.decryption (только имя).
type FluxKustomizationInfo struct {
	FluxObjectInfo
	Path            string `json:"path"`
	Prune           bool   `json:"prune"`
	Wait            bool   `json:"wait"`
	TargetNamespace string `json:"targetNamespace"`
	ServiceAccount  string `json:"serviceAccount"`
	// Remote — задан spec.kubeConfig: ресурсы inventory находятся в другом кластере.
	Remote bool `json:"remote"`
	// InventoryCount — число записей status.inventory.entries (для подтверждения удаления с prune).
	InventoryCount int `json:"inventoryCount"`
}

// FluxHelmHistoryEntry — запись status.history[] у HelmRelease (одна ревизия Helm).
type FluxHelmHistoryEntry struct {
	Version       int64  `json:"version"`
	Status        string `json:"status"`
	ChartName     string `json:"chartName"`
	ChartVersion  string `json:"chartVersion"`
	AppVersion    string `json:"appVersion"`
	FirstDeployed string `json:"firstDeployed"`
	LastDeployed  string `json:"lastDeployed"`
	Digest        string `json:"digest"`
}

// FluxHelmReleaseInfo — HelmRelease (helm.toolkit.fluxcd.io/v2).
// Не отдаются: spec.values, содержимое spec.valuesFrom, секреты spec.kubeConfig.
type FluxHelmReleaseInfo struct {
	FluxObjectInfo
	// Chart и ChartVersion — из spec.chart.spec (chart, version); при spec.chartRef берутся из ссылки и статуса.
	Chart        string `json:"chart"`
	ChartVersion string `json:"chartVersion"`
	// ChartRef — spec.chartRef (OCIRepository или HelmChart); nil, если чарт задан через spec.chart.spec.
	ChartRef         *FluxRef `json:"chartRef"`
	ReleaseName      string   `json:"releaseName"`
	TargetNamespace  string   `json:"targetNamespace"`
	StorageNamespace string   `json:"storageNamespace"`
	// Remote — задан spec.kubeConfig: ресурсы релиза находятся в другом кластере.
	Remote bool `json:"remote"`
	// HasInventory — у объекта есть status.inventory (Flux 2.8+); без него вкладка ресурсов недоступна.
	HasInventory   bool                   `json:"hasInventory"`
	InventoryCount int                    `json:"inventoryCount"`
	History        []FluxHelmHistoryEntry `json:"history"` // новые записи первыми
}

// FluxSourceInfo — источник: GitRepository, OCIRepository, HelmRepository, HelmChart или Bucket (вид — в Kind).
// Не отдаются: userinfo в URL, содержимое секретов (из spec.secretRef только имя).
type FluxSourceInfo struct {
	FluxObjectInfo
	URL      string `json:"url"`      // spec.url (у Bucket — spec.endpoint) без userinfo
	RefType  string `json:"refType"`  // branch | tag | semver | name | commit | digest; пусто — не задано
	RefValue string `json:"refValue"` // значение ссылки (ветка, тег, semver-диапазон, digest)
	Digest   string `json:"digest"`   // status.artifact.digest
	// SecretRef — имя секрета из spec.secretRef; значение секрета никогда не читается.
	SecretRef    string `json:"secretRef"`
	Chart        string `json:"chart"`        // HelmChart: spec.chart
	ChartVersion string `json:"chartVersion"` // HelmChart: spec.version
	BucketName   string `json:"bucketName"`   // Bucket: spec.bucketName
}

// FluxProviderInfo — Provider (notification.toolkit.fluxcd.io).
// Адрес отдаётся только как схема и хост: в путях и query адресов вебхуков бывают токены.
type FluxProviderInfo struct {
	FluxObjectInfo
	Type      string `json:"type"`
	Channel   string `json:"channel"`
	Address   string `json:"address"`   // схема и хост, без пути, query и userinfo
	SecretRef string `json:"secretRef"` // только имя
}

// FluxAlertInfo — Alert (notification.toolkit.fluxcd.io).
type FluxAlertInfo struct {
	FluxObjectInfo
	ProviderRef   string    `json:"providerRef"`   // spec.providerRef.name
	EventSeverity string    `json:"eventSeverity"` // info | error
	EventSources  []FluxRef `json:"eventSources"`  // имя может быть «*»
}

// FluxReceiverInfo — Receiver (notification.toolkit.fluxcd.io).
// status.webhookPath и путь вебхука не отдаются: сам путь служит секретом (зная его, можно запустить reconcile).
type FluxReceiverInfo struct {
	FluxObjectInfo
	Type      string    `json:"type"`
	Resources []FluxRef `json:"resources"`
	SecretRef string    `json:"secretRef"` // только имя
}

// FluxNotifications — ответ GetFluxNotifications.
type FluxNotifications struct {
	Providers []FluxProviderInfo `json:"providers"`
	Alerts    []FluxAlertInfo    `json:"alerts"`
	Receivers []FluxReceiverInfo `json:"receivers"`
}

// FluxImageRepositoryInfo — ImageRepository (image.toolkit.fluxcd.io). Реестр образов из Kubron не вызывается.
type FluxImageRepositoryInfo struct {
	FluxObjectInfo
	Image      string `json:"image"`      // spec.image
	TagCount   int    `json:"tagCount"`   // status.lastScanResult.tagCount
	LastScanAt string `json:"lastScanAt"` // status.lastScanResult.scanTime
}

// FluxImagePolicyInfo — ImagePolicy (image.toolkit.fluxcd.io).
type FluxImagePolicyInfo struct {
	FluxObjectInfo
	ImageRepository string `json:"imageRepository"` // spec.imageRepositoryRef.name
	Policy          string `json:"policy"`          // правило: «semver: >=1.0.0», «numerical: asc» и т. п.
	LatestImage     string `json:"latestImage"`     // status.latestRef или latestImage
}

// FluxImageUpdateAutomationInfo — ImageUpdateAutomation (image.toolkit.fluxcd.io); источник — SourceRef общей части.
type FluxImageUpdateAutomationInfo struct {
	FluxObjectInfo
	Branch         string `json:"branch"`         // spec.git.push.branch (или checkout.ref.branch)
	LastRunAt      string `json:"lastRunAt"`      // status.lastAutomationRunTime
	LastPushCommit string `json:"lastPushCommit"` // status.lastPushCommit
}

// FluxImageAutomation — ответ GetFluxImageAutomation.
type FluxImageAutomation struct {
	Repositories []FluxImageRepositoryInfo       `json:"repositories"`
	Policies     []FluxImagePolicyInfo           `json:"policies"`
	Automations  []FluxImageUpdateAutomationInfo `json:"automations"`
}

// Роли вершин FluxGraph.
const (
	FluxRoleSource   = "source"   // источник (GitRepository, OCIRepository, ...)
	FluxRoleFlux     = "flux"     // Kustomization или HelmRelease
	FluxRoleResource = "resource" // ресурс из inventory и найденное по связям
)

// Типы рёбер FluxGraph. owns, selects и routes строит общий построитель (graphbuild.go), остальные — Flux.
const (
	FluxEdgeOwns      = graphEdgeOwns    // владелец → дочерний объект
	FluxEdgeSelects   = graphEdgeSelects // Service → Pod по селектору
	FluxEdgeRoutes    = graphEdgeRoutes  // Ingress → Service
	FluxEdgeSourceRef = "sourceRef"      // источник → Kustomization/HelmRelease
	FluxEdgeDependsOn = "dependsOn"      // зависимость → зависимый объект
	FluxEdgeInventory = "inventory"      // Kustomization/HelmRelease → ресурс из inventory
)

// FluxGraphNode — вершина графа цепочки «источник → объект → ресурсы».
type FluxGraphNode struct {
	ID        string `json:"id"` // group/kind/namespace/name, как у graphID
	Kind      string `json:"kind"`
	Group     string `json:"group"`
	Version   string `json:"version"`
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	Role      string `json:"role"` // FluxRoleSource | FluxRoleFlux | FluxRoleResource
	// State — FluxState для вершин source и flux; для ресурсов — health (Healthy, Degraded, Progressing) или пусто.
	State string `json:"state"`
}

// FluxGraphEdge — связь между вершинами.
type FluxGraphEdge struct {
	From string `json:"from"`
	To   string `json:"to"`
	Type string `json:"type"` // FluxEdge*
}

// FluxGraph — граф цепочки объекта Flux. Warnings — то, что не удалось прочитать: частичный граф лучше ошибки.
type FluxGraph struct {
	// Available — граф можно построить: у HelmRelease на Flux < 2.8 нет status.inventory, ответ «недоступно».
	Available bool `json:"available"`
	// Remote — ресурсы inventory находятся в другом кластере и локально не разрешаются.
	Remote    bool            `json:"remote"`
	Nodes     []FluxGraphNode `json:"nodes"`
	Edges     []FluxGraphEdge `json:"edges"`
	Truncated bool            `json:"truncated"` // достигнут предел graphMaxNodes
	Warnings  []string        `json:"warnings"`
}
