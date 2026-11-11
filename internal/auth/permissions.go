// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

// Package auth — локальная аутентификация (логин/пароль) и ролевая модель Kubron.
//
// Модель двухуровневая: роль → набор прав (Permission). Код всегда проверяет ПРАВА, а не роли,
// поэтому новая роль добавляется одной записью в таблицу roles, без правок в местах проверок.
package auth

import "sort"

// Permission — атомарное право на действие.
type Permission string

const (
	// PermRead — просмотр ресурсов кластера, логов, событий, YAML, метрик.
	PermRead Permission = "read"
	// PermSecretsRead — просмотр значений Secret (отдельно от read, чтобы его можно было отозвать).
	PermSecretsRead Permission = "secrets.read"
	// PermOperate — безопасные операции с нагрузками: scale, restart, rollback, повтор Job.
	// Не даёт удалять ресурсы и менять их YAML (для этого нужен PermWrite).
	PermOperate Permission = "operate"
	// PermWrite — изменения ресурсов с повышенным риском: удаление, выселение подов, apply YAML и т.п.
	PermWrite Permission = "write"
	// PermExec — терминал / exec внутри подов.
	PermExec Permission = "exec"
	// PermNodeShell — оболочка на узле: привилегированный под с root-доступом к хосту. Отдельно от exec: терминал
	// в обычном поде и root на узле — разные уровни риска. По умолчанию только у роли admin.
	PermNodeShell Permission = "nodeshell"
	// PermDebug — добавление отладочного (ephemeral) контейнера в работающий под. Отдельно от exec: контейнер
	// запускается из произвольного образа в пространствах имён пода, а в spec пода он остаётся до удаления самого
	// пода (убрать его нельзя). Подключение к такому контейнеру идёт обычным терминалом и требует ещё и exec.
	// По умолчанию только у роли admin.
	PermDebug Permission = "debug"
	// PermPortForward — проброс портов в поды и сервисы.
	PermPortForward Permission = "portforward"
	// PermClusterManage — добавление, изменение и удаление кластеров в приложении.
	PermClusterManage Permission = "clusters.manage"
	// PermUsersManage — управление пользователями и их ролями.
	PermUsersManage Permission = "users.manage"
	// PermAuditRead — просмотр журнала аудита.
	PermAuditRead Permission = "audit.read"
	// PermDiagnosticsLogs — просмотр сохранённых фрагментов логов в истории расследований (ADR-0024, ADR-0026 §5).
	// Живой просмотр логов по-прежнему требует только read: сохранённые фрагменты живут дольше сессии и видны другим
	// пользователям. По умолчанию у admin и operator, у user нет.
	PermDiagnosticsLogs Permission = "diagnostics.logs"
	// PermBaselineManage — создание и принятие baseline конфигурации (ADR-0027, ADR-0026 §5). По умолчанию только
	// у admin; чтение результатов drift требует read.
	PermBaselineManage Permission = "baseline.manage"
)

// allPermissions — полный список прав. Администратор получает все.
var allPermissions = []Permission{
	PermRead,
	PermSecretsRead,
	PermOperate,
	PermWrite,
	PermExec,
	PermNodeShell,
	PermDebug,
	PermPortForward,
	PermClusterManage,
	PermUsersManage,
	PermAuditRead,
	PermDiagnosticsLogs,
	PermBaselineManage,
}

// Role — роль пользователя.
type Role string

const (
	RoleAdmin    Role = "admin"
	RoleOperator Role = "operator"
	RoleUser     Role = "user"
)

type roleDef struct {
	label       string
	permissions []Permission
}

// roles — единственное место, где описаны роли. Чтобы добавить роль, добавьте запись сюда
// (и её идентификатор в roleOrder, чтобы она появилась в интерфейсе).
var roles = map[Role]roleDef{
	// Администратор может всё. Для него Has всегда true, в том числе для будущих прав.
	RoleAdmin: {label: "Administrator", permissions: allPermissions},
	// Оператор: всё, что может пользователь, плюс scale / restart / rollback / повтор Job.
	// Не может удалять ресурсы, менять YAML, открывать терминал, пробрасывать порты и управлять
	// кластерами и пользователями. diagnostics.logs выдан явной записью (ADR-0026 §5); baseline.manage — нет.
	RoleOperator: {label: "Operator", permissions: []Permission{PermRead, PermSecretsRead, PermOperate, PermDiagnosticsLogs}},
	// Пользователь: только чтение. Значения Secret, содержимое релизов Helm (values, манифест, diff ревизий) и сроки
	// сертификатов требуют отдельного права secrets.read, которого у пользователя нет (оно у оператора и администратора).
	RoleUser: {label: "User", permissions: []Permission{PermRead}},
}

var roleOrder = []Role{RoleAdmin, RoleOperator, RoleUser}

// Valid сообщает, известна ли роль.
func (r Role) Valid() bool {
	_, ok := roles[r]
	return ok
}

// Label — отображаемое имя роли.
func (r Role) Label() string {
	if d, ok := roles[r]; ok {
		return d.label
	}
	return string(r)
}

// Has проверяет право. Неизвестная роль не имеет никаких прав (fail closed).
func (r Role) Has(p Permission) bool {
	if r == RoleAdmin {
		return true
	}
	d, ok := roles[r]
	if !ok {
		return false
	}
	for _, rp := range d.permissions {
		if rp == p {
			return true
		}
	}
	return false
}

// Permissions возвращает права роли в стабильном порядке.
func (r Role) Permissions() []Permission {
	d, ok := roles[r]
	if !ok {
		return []Permission{}
	}
	out := make([]Permission, len(d.permissions))
	copy(out, d.permissions)
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// RoleInfo — описание роли для интерфейса.
type RoleInfo struct {
	ID          Role         `json:"id"`
	Label       string       `json:"label"`
	Permissions []Permission `json:"permissions"`
}

// Roles возвращает все роли в порядке отображения.
func Roles() []RoleInfo {
	out := make([]RoleInfo, 0, len(roleOrder))
	for _, r := range roleOrder {
		out = append(out, RoleInfo{ID: r, Label: r.Label(), Permissions: r.Permissions()})
	}
	return out
}

// KnownPermission сообщает, существует ли такое право.
func KnownPermission(p Permission) bool {
	for _, k := range allPermissions {
		if k == p {
			return true
		}
	}
	return false
}
