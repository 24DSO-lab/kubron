// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"runtime"
	"strings"
	"time"

	"github.com/24DSO-labs/kubron/internal/audit"
	"github.com/24DSO-labs/kubron/internal/auth"
	"github.com/24DSO-labs/kubron/internal/k8s"
	"github.com/24DSO-labs/kubron/internal/store"
)

// initAuth открывает хранилище пользователей. Если файл повреждён, приложение остаётся
// закрытым (authz отказывает всем), а причина показывается на экране входа.
func (a *App) initAuth(dir string) {
	a.initAuthWith(store.NewFile(dir))
}

// initAuthWith подключает хранилища: пользователи, журнал аудита, токены Argo CD.
func (a *App) initAuthWith(st store.Store) {
	a.store = st
	k8s.SetTokenStore(st.ArgoTokens())
	k8s.SetClusterStore(st.Clusters())
	// Состояние Helm в сервисе — в БД по командам (ADR-0015); без этого метода Helm остаётся на файлах.
	if hp, ok := st.(interface{ HelmState() k8s.HelmStateStore }); ok {
		k8s.SetHelmStateStore(hp.HelmState())
	}
	a.audit = audit.New(st.Audit())
	svc, err := auth.NewServiceWithBackend(st.Users())
	if err != nil {
		a.authErr = err
		return
	}
	svc.SetAuditor(a.audit)
	// Серверное хранилище задаёт права по членству в командах; файловое — нет (глобальная роль, как раньше).
	if ap, ok := st.(interface{ Access() auth.Access }); ok {
		svc.SetAccess(ap.Access())
	}
	if dp, ok := st.(interface{ Directory() auth.Directory }); ok {
		a.dir = dp.Directory()
	}
	// При выходе из учётной записи ничего из сессии не должно продолжать работать.
	svc.SetOnLogout(a.endSessionResources)
	a.auth = svc
}

// endSessionResources останавливает всё, что запущено в сессии: port-forward, терминалы, потоки логов.
// Пока менеджеры кластеров общие (этап 2, задача 2.3), очищается всё; позже — только ресурсы сессии.
func (a *App) endSessionResources() {
	a.pfMgr.StopAll()
	a.k8sMgr.CloseAllTerminals()
	a.k8sMgr.StopAllLogStreams()
}

// NewSessionView создаёт вид ядра для одной веб-сессии: общие менеджеры и хранилища, но своя
// сессия пользователя (вход, права, простой). source — адрес клиента для блокировки перебора паролей.
// Это функция пакета, а не метод App: она не должна попадать в привязки Wails и RPC.
func NewSessionView(root *App, source string) *App {
	v := *root
	// Свой вид менеджеров: активный кластер, терминалы, логи и port-forward принадлежат сессии;
	// пул подключений (informers) общий.
	v.k8sMgr = root.k8sMgr.ForSession()
	v.conn = newConnectState() // входы принадлежат сессии
	v.k8sMgr.SetNotifier(nil)  // события пойдут владельцу после BindView; до входа адресата нет
	v.pfMgr = k8s.NewSessionPortForwardManager(v.k8sMgr)
	// Своя граница отмены: операции вида кончаются с его освобождением (ReleaseView) или остановкой ядра.
	v.base, v.cancel = context.WithCancel(root.baseContext())
	v.configureTerminals()
	v.configurePortForward() // владелец потоков пока неизвестен: режим proxy включается в BindView
	if root.auth == nil {
		return &v
	}
	v.auth = root.auth.NewSession(source)
	v.auth.SetOnLogout(v.endSessionResources)
	return &v
}

// sessionEmitter — платформа, умеющая адресовать событие одной сессии (веб).
type sessionEmitter interface {
	EmitTo(sessionID, event string, data ...any)
}

// BindView привязывает вид к веб-сессии: события его менеджера уходят только её соединениям.
func BindView(v *App, sessionID string) {
	v.sessionID = sessionID
	v.configurePortForward()
	if em, ok := v.platform.(sessionEmitter); ok {
		v.k8sMgr.SetNotifier(func(event string, data ...any) { em.EmitTo(sessionID, event, data...) })
	}
}

// SetResourceLimits задаёт лимиты ресурсов на пользователя для видов, создаваемых после вызова.
func SetResourceLimits(root *App, l k8s.Limits) { root.k8sMgr.SetLimits(l) }

// SetClusterIdleTTL задаёт задержку остановки informers кластера после ухода последнего пользователя.
func SetClusterIdleTTL(root *App, d time.Duration) { root.k8sMgr.SetIdleTTL(d) }

// StartUserRefresh запускает фоновое перечитывание пользователей, если они лежат в общем хранилище (PostgreSQL,
// несколько реплик): роль, блокировка и новые пользователи, изменённые на другой реплике, видны здесь не позже
// чем через every. Для файлового хранилища ничего не делает.
func StartUserRefresh(ctx context.Context, root *App, every time.Duration) {
	if root == nil || root.auth == nil || !root.auth.Shared() {
		return
	}
	go root.auth.RunRefresher(ctx, every)
}

// RefreshUsers немедленно перечитывает пользователей из общего хранилища: другая реплика сообщила, что изменила
// пользователей или права. Не ждёт периодического перечитывания (StartUserRefresh).
func RefreshUsers(root *App) {
	if root != nil && root.auth != nil {
		root.auth.RefreshNow()
	}
}

// ResumeView открывает сессию пользователя на виде, построенном заново для уже существующей веб-сессии
// (другая реплика, перезапуск): пароль не нужен, веб-сессию проверило общее хранилище сессий.
func ResumeView(v *App, userID string, lastActive time.Time) error {
	if v == nil || v.auth == nil {
		return auth.ErrNotAuthenticated
	}
	return v.auth.Resume(userID, lastActive)
}

// SyncViewActivity переносит в вид время последнего действия пользователя, записанное другой репликой.
func SyncViewActivity(v *App, at time.Time) {
	if v != nil && v.auth != nil {
		v.auth.SyncActivity(at)
	}
}

// ViewActiveCluster — идентификатор активного кластера вида ("" — не выбран). Без проверки прав: сервер
// только запоминает его в веб-сессии, чтобы другая реплика подключила кластер заново.
func ViewActiveCluster(v *App) string {
	if v == nil || v.k8sMgr == nil {
		return ""
	}
	return v.k8sMgr.GetActiveContext()
}

// RestoreActiveCluster подключает вид к кластеру, который был активен в веб-сессии на другой реплике.
// Права проверяются как при обычном подключении: доступ мог измениться.
func RestoreActiveCluster(v *App, clusterID string) error {
	if v == nil || v.k8sMgr == nil || clusterID == "" {
		return nil
	}
	if err := v.authzCluster(auth.PermRead, clusterID); err != nil {
		return err
	}
	v.bindAuthSession()
	return v.k8sMgr.ConnectToCluster(clusterID)
}

// ViewActive — у вида есть действующая сессия пользователя. Сервер закрывает веб-сессии, чьи виды её потеряли.
func ViewActive(v *App) bool { return v != nil && v.auth != nil && v.auth.Active() }

// ReleaseView освобождает вид, созданный NewSessionView: останавливает ресурсы сессии,
// снимает отметку активного кластера в общем пуле и закрывает сессию пользователя.
func ReleaseView(v *App) {
	if v == nil {
		return
	}
	if v.cancel != nil {
		v.cancel() // незавершённые обращения вида к провайдерам входа не должны пережить сессию
	}
	v.k8sMgr.Close() // и запросы вида к API кластеров
	v.endSessionResources()
	v.k8sMgr.Detach()
	if v.auth != nil {
		v.auth.Release()
	}
}

// auditedPermissions — права, использование которых попадает в журнал при разрешении.
// Чтение не журналируется (слишком шумно и безопасно), управление пользователями пишет
// сам auth.Service — вместе с результатом.
var auditedPermissions = map[auth.Permission]bool{
	auth.PermOperate:       true,
	auth.PermWrite:         true,
	auth.PermExec:          true,
	auth.PermPortForward:   true,
	auth.PermClusterManage: true,
	auth.PermSecretsRead:   true,
}

// quietMethods — вызываются на каждое нажатие клавиши/опрос терминала: в журнал не пишутся
// (начало и конец терминальной сессии записываются методами StartTerminalSession / CloseTerminal).
var quietMethods = map[string]bool{
	"WriteTerminal":        true,
	"GetCertificateExpiry": true, // фоновое обновление карточки; сами сертификаты не раскрываются
	"ResizeTerminal":       true,
}

// authz — единственная точка проверки прав в Wails-методах.
// Каждый экспортируемый метод App обязан начинаться с её вызова; это проверяет
// тест internal/auth/policy_test.go.
//
// Необязательные аргументы — пары «ключ, значение» для журнала аудита (цель действия):
// a.authz(auth.PermWrite, "namespace", ns, "name", name). Секреты и содержимое (пароли,
// YAML, ввод терминала) сюда передавать нельзя.
func (a *App) authz(p auth.Permission, kv ...any) error {
	// Права кластера определяются по активному кластеру вида: сигнатуры методов не меняются (этап 3, решение 1).
	id := ""
	if auth.ClusterScoped(p) && a.k8sMgr != nil {
		id = a.k8sMgr.GetActiveContext()
	}
	return a.authzAt(p, id, kv, 1)
}

// authzCluster — authz для метода, который получает идентификатор кластера явно (подключение, правка, удаление).
// Чужой и несуществующий кластер дают одну и ту же ошибку auth.ErrClusterNotFound.
func (a *App) authzCluster(p auth.Permission, clusterID string, kv ...any) error {
	return a.authzAt(p, clusterID, kv, 1)
}

// authzTeam — authz для действия над командой (состав, кластеры). Без сервисного режима команд нет, и teamID
// пуст: действует глобальная роль, как в десктопе.
func (a *App) authzTeam(p auth.Permission, teamID string, kv ...any) error {
	if a.auth == nil {
		return a.authUnavailable()
	}
	actor, err := a.auth.CheckTeam(p, teamID)
	switch {
	case err == nil:
		if auditedPermissions[p] {
			a.auditCall(actor, audit.ResultOK, p, kv, 0)
		}
	case errors.Is(err, auth.ErrForbidden):
		a.auditCall(actor, audit.ResultDenied, p, kv, 0)
	}
	return err
}

// authzAny — authz, который проходит при любом из двух прав (например, «читаю свои команды» или «администрирую организацию»).
// Отказ записывается только по последнему праву.
func (a *App) authzAny(first, second auth.Permission, kv ...any) error {
	if a.auth != nil {
		if err := a.authzAt(first, "", nil, 0); err == nil {
			return nil
		} else if !errors.Is(err, auth.ErrForbidden) {
			return err
		}
	}
	return a.authzAt(second, "", kv, 1)
}

// defaultTeam — команда по умолчанию в сервисном режиме, иначе пусто.
func (a *App) defaultTeam() string {
	if a.dir == nil {
		return ""
	}
	return a.dir.DefaultTeam()
}

// checkKubeconfigSource не пускает через источник kubeconfig туда, куда пользователя не пускает авторизация:
// путь к файлу сервера — только там, где ядро читает локальные файлы (десктоп), а сохранённый кластер — только
// тот, которым пользователь может управлять.
func (a *App) checkKubeconfigSource(src k8s.KubeconfigSource) error {
	if src.Path != "" && !a.platform.Capabilities().LocalKubeconfig {
		return errors.New("reading a kubeconfig from a file path is available only on desktop")
	}
	if src.BaseClusterID != "" {
		// Скрытые секреты берутся из этого кластера: нужно право управлять им.
		if err := a.authzCluster(auth.PermClusterManage, src.BaseClusterID, "source", "secrets"); err != nil {
			return err
		}
	}
	if src.ClusterID != "" {
		return a.authzCluster(auth.PermClusterManage, src.ClusterID, "source", "cluster")
	}
	return nil
}

// authzAt — общая часть; depth — число служебных кадров между ней и методом App (для журнала).
func (a *App) authzAt(p auth.Permission, clusterID string, kv []any, depth int) error {
	if a.auth == nil {
		return a.authUnavailable()
	}
	actor, err := a.auth.CheckCluster(p, clusterID)
	switch {
	case err == nil:
		if auditedPermissions[p] {
			a.auditCall(actor, audit.ResultOK, p, kv, depth)
		}
	case errors.Is(err, auth.ErrForbidden):
		a.auditCall(actor, audit.ResultDenied, p, kv, depth)
	}
	return err
}

// auditCall записывает вызов метода App. Имя метода берётся из стека, чтобы его не дублировать
// в каждом вызове authz.
func (a *App) auditCall(actor auth.Actor, result string, p auth.Permission, kv []any, depth int) {
	if a.audit == nil {
		return
	}
	action := callerMethod(3 + depth)
	if quietMethods[action] {
		return
	}
	var parts []string
	for i := 0; i+1 < len(kv); i += 2 {
		parts = append(parts, fmt.Sprintf("%v=%v", kv[i], kv[i+1]))
	}
	if result == audit.ResultDenied {
		parts = append(parts, "need="+string(p))
	}
	a.audit.Record(audit.Event{
		User: actor.Username, Role: string(actor.Role),
		Action: action, Result: result, Detail: strings.Join(parts, " "), Source: actor.Source,
		TeamID: actor.TeamID, ClusterID: actor.ClusterID,
	})
}

// callerMethod возвращает имя метода на skip кадров выше вызывающего callerMethod:
// «main.(*App).DeletePod» → «DeletePod». CallersFrames корректно раскрывает инлайн-вызовы.
func callerMethod(skip int) string {
	pcs := make([]uintptr, 1)
	if runtime.Callers(skip+1, pcs) == 0 {
		return "unknown"
	}
	frame, _ := runtime.CallersFrames(pcs).Next()
	if frame.Function == "" {
		return "unknown"
	}
	return frame.Function[strings.LastIndex(frame.Function, ".")+1:]
}

func (a *App) authUnavailable() error {
	if a.authErr != nil {
		return fmt.Errorf("authorization subsystem is unavailable: %w", a.authErr)
	}
	return errors.New("authorization subsystem is not initialized")
}

// ─── Методы, доступные без права доступа ───────────────────
// Это единственные экспортируемые методы, которые не вызывают authz (см. policy_test.go).

// AuthStatus — стартовое состояние: нужна ли первичная настройка и кто вошёл.
func (a *App) AuthStatus() (*auth.Status, error) {
	if a.auth == nil {
		return nil, a.authUnavailable()
	}
	st := a.auth.Status()
	return &st, nil
}

// SetupAdmin создаёт первого администратора (только при пустом списке пользователей).
func (a *App) SetupAdmin(username, password string) (*auth.UserInfo, error) {
	if a.auth == nil {
		return nil, a.authUnavailable()
	}
	return a.auth.Setup(username, password)
}

// Login открывает сессию по логину и паролю.
func (a *App) Login(username, password string) (*auth.UserInfo, error) {
	if a.auth == nil {
		return nil, a.authUnavailable()
	}
	return a.auth.Login(username, password)
}

// Logout закрывает сессию.
func (a *App) Logout() {
	if a.auth != nil {
		// Отзыв до выхода: журнал ещё знает пользователя. Сбой отзыва не мешает выйти.
		_ = a.revokePersonal(a.currentUserID(), "", "logout")
		a.auth.Logout()
	}
}

// CurrentUser возвращает вошедшего пользователя.
func (a *App) CurrentUser() (*auth.UserInfo, error) {
	if a.auth == nil {
		return nil, a.authUnavailable()
	}
	return a.auth.Current()
}

// ReportActivity вызывается интерфейсом при действиях пользователя (мышь, клавиатура)
// и продлевает сессию. Не требует права: достаточно действующей сессии.
func (a *App) ReportActivity() error {
	if a.auth == nil {
		return a.authUnavailable()
	}
	return a.auth.ReportActivity()
}

// ChangePassword меняет пароль вошедшего пользователя (в том числе когда смена обязательна).
func (a *App) ChangePassword(oldPassword, newPassword string) error {
	if a.auth == nil {
		return a.authUnavailable()
	}
	uid := a.currentUserID()
	err := a.auth.ChangePassword(oldPassword, newPassword)
	if err == nil {
		err = a.revokePersonal(uid, "", "password-change")
	}
	return err
}

// ─── Управление пользователями (users.manage) ──────────────

// ListRoles возвращает роли и их права.
func (a *App) ListRoles() ([]auth.RoleInfo, error) {
	if err := a.authz(auth.PermUsersManage); err != nil {
		return nil, err
	}
	return auth.Roles(), nil
}

func (a *App) ListUsers() ([]auth.UserInfo, error) {
	if err := a.authz(auth.PermUsersManage); err != nil {
		return nil, err
	}
	return a.auth.ListUsers()
}

func (a *App) CreateUser(username, password, role string) (*auth.UserInfo, error) {
	if err := a.authz(auth.PermUsersManage); err != nil {
		return nil, err
	}
	return a.auth.CreateUser(username, password, auth.Role(strings.TrimSpace(role)))
}

func (a *App) UpdateUserRole(id, role string) (*auth.UserInfo, error) {
	if err := a.authz(auth.PermUsersManage); err != nil {
		return nil, err
	}
	return a.auth.UpdateRole(id, auth.Role(strings.TrimSpace(role)))
}

func (a *App) SetUserDisabled(id string, disabled bool) (*auth.UserInfo, error) {
	if err := a.authz(auth.PermUsersManage); err != nil {
		return nil, err
	}
	u, err := a.auth.SetDisabled(id, disabled)
	if err == nil && disabled {
		err = a.revokePersonal(id, "", "disabled")
	}
	return u, err
}

func (a *App) ResetUserPassword(id, newPassword string) (*auth.UserInfo, error) {
	if err := a.authz(auth.PermUsersManage); err != nil {
		return nil, err
	}
	u, err := a.auth.ResetPassword(id, newPassword)
	if err == nil {
		err = a.revokePersonal(id, "", "password-reset")
	}
	return u, err
}

func (a *App) DeleteUser(id string) error {
	if err := a.authz(auth.PermUsersManage); err != nil {
		return err
	}
	err := a.auth.DeleteUser(id)
	if err == nil {
		err = a.revokePersonal(id, "", "user-deleted")
	}
	return err
}

// GetWebOIDCClient returns the public OIDC client of the web sign-in and the issuer it is registered at.
// Only organization administrators may read it.
func (a *App) GetWebOIDCClient() (store.WebOIDC, error) {
	if err := a.authz(auth.PermUsersManage); err != nil {
		return store.WebOIDC{}, err
	}
	if a.store == nil {
		return store.WebOIDC{}, nil
	}
	return a.store.WebOIDC()
}

// SetWebOIDCClient configures the public OIDC client used by the browser sign-in of the web server and the issuer
// it is registered at: the client is applied only to clusters of that issuer that sign in with the authorization
// code. Both values are required together; both empty clear the setting.
func (a *App) SetWebOIDCClient(clientID, issuerURL string) error {
	if err := a.authz(auth.PermUsersManage); err != nil {
		return err
	}
	clientID, issuerURL = strings.TrimSpace(clientID), strings.TrimSpace(issuerURL)
	if len(clientID) > 256 || strings.IndexFunc(clientID, func(r rune) bool { return r < 0x20 || r == 0x7f }) >= 0 {
		return errors.New("web OIDC client id must be at most 256 bytes and contain no control characters")
	}
	if (clientID == "") != (issuerURL == "") {
		return errors.New("web OIDC client id and issuer URL must be set together")
	}
	if issuerURL != "" && !validWebOIDCIssuer(issuerURL) {
		return errors.New("web OIDC issuer URL must be an https URL without query, fragment or user information")
	}
	if a.store == nil {
		return errors.New("application settings store is unavailable")
	}
	return a.store.SetWebOIDC(store.WebOIDC{ClientID: clientID, IssuerURL: issuerURL})
}

// validWebOIDCIssuer accepts an https URL of at most 512 bytes: an issuer has no query, fragment or user
// information, and the web server talks to identity providers over https only.
func validWebOIDCIssuer(s string) bool {
	if len(s) > 512 || strings.IndexFunc(s, func(r rune) bool { return r < 0x20 || r == 0x7f }) >= 0 {
		return false
	}
	u, err := url.Parse(s)
	return err == nil && u.Scheme == "https" && u.Host != "" && u.User == nil && u.RawQuery == "" && !u.ForceQuery && u.Fragment == ""
}

// ─── Журнал аудита (audit.read) ────────────────────────────

// auditScope готовит фильтр журнала для текущего пользователя: администратор организации видит всё, администратор
// команды — только события своих команд. Ограничение задаётся здесь, а не приходит от клиента.
func (a *App) auditScope(f audit.Filter) audit.Filter {
	f.Scoped, f.Scope = false, nil
	if a.auth.Authorize(auth.PermAuditRead) != nil {
		f.Scoped, f.Scope = true, a.auth.TeamsWith(auth.PermClusterManage)
	}
	return f
}

// GetAuditLog возвращает записи журнала от новых к старым с учётом фильтра.
func (a *App) GetAuditLog(filter audit.Filter) (*audit.Page, error) {
	if err := a.authzAny(auth.PermAuditRead, auth.PermClusterManage); err != nil {
		return nil, err
	}
	return a.audit.Query(a.auditScope(filter))
}

// AuditExport — выгрузка журнала: содержимое файла и число записей.
type AuditExport struct {
	Data      string `json:"data"`
	Count     int    `json:"count"`
	Truncated bool   `json:"truncated"`
}

// ExportAuditLog выгружает записи журнала по фильтру в CSV или JSON (до audit.ExportLimit записей).
// Сама выгрузка тоже попадает в журнал.
func (a *App) ExportAuditLog(filter audit.Filter, format string) (*AuditExport, error) {
	if err := a.authzAny(auth.PermAuditRead, auth.PermClusterManage); err != nil {
		return nil, err
	}
	data, n, truncated, err := a.audit.Export(a.auditScope(filter), format)
	if err != nil {
		return nil, err
	}
	a.recordTeam("ExportAuditLog", audit.ResultOK, "", fmt.Sprintf("format=%s rows=%d", format, n))
	return &AuditExport{Data: string(data), Count: n, Truncated: truncated}, nil
}

// SetIdleTimeout задаёт время автовыхода по бездействию в минутах (0 — выключить).
func (a *App) SetIdleTimeout(minutes int) (int, error) {
	if err := a.authz(auth.PermUsersManage); err != nil {
		return 0, err
	}
	return a.auth.SetIdleTimeout(minutes)
}
