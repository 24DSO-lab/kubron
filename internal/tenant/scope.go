// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

// Package tenant — общий для операционных модулей (ADR-0026) тип области доступа «организация → команда → кластер»
// (ADR-0012). Область берётся из каталога Kubron, а не из параметров клиента.
package tenant

// Scope — область доступа одной записи или одной операции. Входит в ключ каждой доменной записи и в каждый
// запрос к хранилищу: запись чужой области недоступна и отвечает как отсутствующая (ErrNotFound домена).
type Scope struct {
	OrgID     string
	TeamID    string
	ClusterID string
}

// Valid сообщает, что заполнены все три части области. Пустая часть означает «область не определена»,
// и хранилища отказывают такой области (fail closed), а не трактуют пустоту как «любая».
func (s Scope) Valid() bool {
	return s.OrgID != "" && s.TeamID != "" && s.ClusterID != ""
}
