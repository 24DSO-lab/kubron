// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

// Package baseline — модель и хранилище baseline конфигурации (ADR-0027).
//
// Baseline — данные Kubron, а не объект кластера: набор неизменяемых версий, каждая из которых — нормализованный
// снимок живого состояния ресурсов. Пакет описывает типы и узкий интерфейс хранилища Backend (ADR-0026 §4) с двумя
// реализациями (файловая — здесь, PostgreSQL — в internal/pgstore). Сравнение и нормализацию делает internal/drift.
//
// Правила пакета:
//   - значения, способные нести секрет, хранятся только как keyed hash (HashedValue); Salt и Fields не попадают в журнал
//     (LogValue, String), а в аудит и результаты проверок содержимое не входит;
//   - пакет не импортирует платформу операционных модулей и клиентов Kubernetes: tenant.Scope приходит параметром.
package baseline

import (
	"crypto/hmac"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/24DSO-lab/kubron/internal/tenant"
)

// SourceLiveSnapshot — источник версии в MVP: снимок живого состояния по явному действию пользователя.
const SourceLiveSnapshot = "live-snapshot"

// Ref — ссылка на ресурс: group/version/kind, namespace и имя. Namespace пуст у кластерных ресурсов.
type Ref struct {
	Group     string `json:"group,omitempty"`
	Version   string `json:"version"`
	Kind      string `json:"kind"`
	Namespace string `json:"namespace,omitempty"`
	Name      string `json:"name"`
}

// Key — идентичность ресурса без версии API: версия при обновлении кластера меняется, а ресурс остаётся тем же.
func (r Ref) Key() string {
	return r.Group + "/" + r.Kind + "/" + r.Namespace + "/" + r.Name
}

// String — читаемая запись «apps/v1 Deployment ns/name» (для сообщений; содержимого объектов не несёт).
func (r Ref) String() string {
	gv := r.Version
	if r.Group != "" {
		gv = r.Group + "/" + r.Version
	}
	name := r.Name
	if r.Namespace != "" {
		name = r.Namespace + "/" + r.Name
	}
	return gv + " " + r.Kind + " " + name
}

// Validate проверяет, что заполнены вид и имя.
func (r Ref) Validate() error {
	if r.Version == "" || r.Kind == "" || r.Name == "" {
		return fmt.Errorf("%w: a resource reference needs version, kind and name", ErrInvalid)
	}
	return nil
}

// Baseline — документ baseline: область, набор ресурсов и указатель на активную версию. Версии лежат отдельно и не
// меняются; менять документ допустимо (смена активной версии).
type Baseline struct {
	ID    string       `json:"id"`
	Scope tenant.Scope `json:"scope"`
	Name  string       `json:"name"`
	// Область: пространства имён и селектор меток, по которым выбираются ресурсы, и допустимые типы (пусто — все
	// типы MVP). Refs — разрешённый набор ресурсов, который снимается в новую версию.
	Namespaces      []string  `json:"namespaces,omitempty"`
	Selector        string    `json:"selector,omitempty"`
	Kinds           []string  `json:"kinds,omitempty"`
	Refs            []Ref     `json:"refs"`
	ActiveVersionID string    `json:"activeVersionId"`
	CreatedBy       string    `json:"createdBy"`
	CreatedAt       time.Time `json:"createdAt"`
}

// Validate проверяет обязательные поля документа.
func (b Baseline) Validate() error {
	if err := ValidateID(b.ID); err != nil {
		return err
	}
	if !b.Scope.Valid() {
		return ErrInvalidScope
	}
	if strings.TrimSpace(b.Name) == "" {
		return fmt.Errorf("%w: a baseline needs a name", ErrInvalid)
	}
	if b.ActiveVersionID != "" {
		if err := ValidateID(b.ActiveVersionID); err != nil {
			return err
		}
	}
	for _, r := range b.Refs {
		if err := r.Validate(); err != nil {
			return err
		}
	}
	return nil
}

// Version — неизменяемая версия baseline. Salt и Objects чувствительны: в журнал и ответы API они не попадают.
type Version struct {
	ID           string `json:"id"`
	BaselineID   string `json:"baselineId"`
	Seq          int    `json:"seq"`
	RulesVersion int    `json:"rulesVersion"`
	Source       string `json:"source"`
	Author       string `json:"author"`
	Comment      string `json:"comment"`
	// CreatedAt хранится с точностью до микросекунды в UTC: так одинаково в файлах и в PostgreSQL.
	CreatedAt time.Time `json:"createdAt"`
	// Salt — 32 случайных байта, ключ HMAC значений этой версии. Хранится вместе с версией (в server — зашифрованно).
	Salt    []byte   `json:"salt"`
	Objects []Object `json:"objects"`
}

// Validate проверяет версию перед записью.
func (v Version) Validate() error {
	if err := ValidateID(v.ID); err != nil {
		return err
	}
	if err := ValidateID(v.BaselineID); err != nil {
		return err
	}
	if v.Seq < 1 {
		return fmt.Errorf("%w: the version number must be positive", ErrInvalid)
	}
	if v.Source != SourceLiveSnapshot {
		return fmt.Errorf("%w: unsupported version source", ErrInvalid)
	}
	if len(v.Salt) == 0 {
		return fmt.Errorf("%w: a version needs a salt", ErrInvalid)
	}
	for _, o := range v.Objects {
		if err := o.Ref.Validate(); err != nil {
			return err
		}
	}
	return nil
}

// Summary возвращает метаданные версии без Salt и Objects.
func (v Version) Summary() VersionSummary {
	return VersionSummary{
		ID: v.ID, BaselineID: v.BaselineID, Seq: v.Seq, RulesVersion: v.RulesVersion, Source: v.Source,
		Author: v.Author, Comment: v.Comment, CreatedAt: v.CreatedAt, ObjectCount: len(v.Objects),
	}
}

// String и GoString не печатают соль и поля: версия может случайно попасть в %v или в сообщение об ошибке.
func (v Version) String() string {
	return fmt.Sprintf("baseline version %s (baseline %s, #%d, %d objects)", v.ID, v.BaselineID, v.Seq, len(v.Objects))
}

// GoString — то же для %#v.
func (v Version) GoString() string { return v.String() }

// LogValue — запись для slog: идентификаторы и счётчики, без Salt и Objects.
func (v Version) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("id", v.ID), slog.String("baseline_id", v.BaselineID), slog.Int("seq", v.Seq),
		slog.Int("rules_version", v.RulesVersion), slog.Int("objects", len(v.Objects)),
		slog.String("salt", "[REDACTED]"),
	)
}

// VersionSummary — метаданные версии для списков и истории (без соли и содержимого).
type VersionSummary struct {
	ID           string    `json:"id"`
	BaselineID   string    `json:"baselineId"`
	Seq          int       `json:"seq"`
	RulesVersion int       `json:"rulesVersion"`
	Source       string    `json:"source"`
	Author       string    `json:"author"`
	Comment      string    `json:"comment"`
	CreatedAt    time.Time `json:"createdAt"`
	ObjectCount  int       `json:"objectCount"`
}

// Object — нормализованный объект версии: путь поля → значение (плоская карта, пути строит internal/drift).
type Object struct {
	Ref    Ref              `json:"ref"`
	Fields map[string]Field `json:"fields"`
}

// String и GoString не печатают значения полей.
func (o Object) String() string {
	return fmt.Sprintf("baseline object %s (%d fields)", o.Ref, len(o.Fields))
}

// GoString — то же для %#v.
func (o Object) GoString() string { return o.String() }

// LogValue — запись для slog без значений полей.
func (o Object) LogValue() slog.Value {
	return slog.GroupValue(slog.String("ref", o.Ref.String()), slog.Int("fields", len(o.Fields)))
}

// Field — значение листа нормализованного объекта: либо JSON-литерал (Plain), либо keyed hash (Hash).
// Пустой Plain означает, что значение хранится только как hash.
type Field struct {
	// Plain — нормализованное значение как JSON-литерал: "nginx:1.25", 3, true, null, {} или [].
	Plain string `json:"p,omitempty"`
	// Hash — HMAC-SHA256 значения с солью версии (ADR-0027 §4); содержимое не восстанавливается.
	Hash *HashedValue `json:"h,omitempty"`
}

// Sensitive сообщает, что значение хранится только как hash.
func (f Field) Sensitive() bool { return f.Hash != nil }

// Equal сравнивает значения: литералы — как строки, hash — через hmac.Equal (постоянное время).
// Литерал и hash не равны никогда.
func (f Field) Equal(g Field) bool {
	switch {
	case f.Hash != nil && g.Hash != nil:
		return hmac.Equal(f.Hash.HMAC, g.Hash.HMAC)
	case f.Hash == nil && g.Hash == nil:
		return f.Plain == g.Plain
	default:
		return false
	}
}

// String и GoString не печатают ни значение, ни hash.
func (f Field) String() string {
	if f.Hash != nil {
		return "[HASHED]"
	}
	return f.Plain
}

// GoString — то же для %#v.
func (f Field) GoString() string { return "baseline.Field{" + f.String() + "}" }

// LogValue — запись для slog: hash скрыт, литерал показан (он уже прошёл редакцию при нормализации).
func (f Field) LogValue() slog.Value {
	if f.Hash != nil {
		return slog.StringValue("[HASHED]")
	}
	return slog.StringValue(f.Plain)
}

// HashedValue — keyed hash значения.
type HashedValue struct {
	HMAC []byte `json:"hmac"`
}

// String и GoString не печатают байты hash.
func (HashedValue) String() string { return "[HASHED]" }

// GoString — то же для %#v.
func (HashedValue) GoString() string { return "baseline.HashedValue{[HASHED]}" }

// LogValue — запись для slog.
func (HashedValue) LogValue() slog.Value { return slog.StringValue("[HASHED]") }

// ExceptionScope — к чему относится исключение.
type ExceptionScope string

const (
	// ExceptionResource — игнорировать путь для одного ресурса (group, kind, namespace, name).
	ExceptionResource ExceptionScope = "resource"
	// ExceptionKind — игнорировать путь для типа (group, kind) в namespace; пустой namespace — во всех.
	ExceptionKind ExceptionScope = "kind"
)

// Exception — пользовательское исключение из сравнения (ADR-0027 §3). Комментарий обязателен, срок необязателен;
// просроченное исключение не применяется. Хранится отдельным документом, привязанным к baseline и версии.
type Exception struct {
	ID    string         `json:"id"`
	Scope ExceptionScope `json:"scope"`
	// Ref задаёт цель: для ExceptionResource — ресурс целиком, для ExceptionKind — Group, Kind и Namespace
	// (Version и Name игнорируются).
	Ref Ref `json:"ref"`
	// Path — путь, который не сравнивается: совпадающий путь и всё, что лежит под ним.
	Path      string     `json:"path"`
	Comment   string     `json:"comment"`
	ExpiresAt *time.Time `json:"expiresAt,omitempty"`
	CreatedBy string     `json:"createdBy"`
	CreatedAt time.Time  `json:"createdAt"`
}

// MaxPathLength — предел длины пути исключения и комментария по размеру защищает хранилище от мусора.
const (
	MaxPathLength    = 512
	MaxCommentLength = 2000
)

// Validate проверяет исключение перед записью.
func (e Exception) Validate() error {
	if err := ValidateID(e.ID); err != nil {
		return err
	}
	switch e.Scope {
	case ExceptionResource:
		if e.Ref.Kind == "" || e.Ref.Name == "" {
			return fmt.Errorf("%w: a resource exception needs kind and name", ErrInvalid)
		}
	case ExceptionKind:
		if e.Ref.Kind == "" {
			return fmt.Errorf("%w: a kind exception needs a kind", ErrInvalid)
		}
	default:
		return fmt.Errorf("%w: unknown exception scope", ErrInvalid)
	}
	if e.Path == "" || len(e.Path) > MaxPathLength || hasControl(e.Path) {
		return fmt.Errorf("%w: the exception path is empty, too long or has control characters", ErrInvalid)
	}
	if strings.TrimSpace(e.Comment) == "" {
		return fmt.Errorf("%w: an exception needs a comment", ErrInvalid)
	}
	if len(e.Comment) > MaxCommentLength {
		return fmt.Errorf("%w: the exception comment is too long", ErrInvalid)
	}
	return nil
}

// Expired сообщает, что срок исключения к моменту now истёк (граница не включается: в момент ExpiresAt уже не действует).
func (e Exception) Expired(now time.Time) bool {
	return e.ExpiresAt != nil && !now.Before(*e.ExpiresAt)
}

// Matches сообщает, относится ли исключение к ресурсу ref (без учёта пути).
func (e Exception) Matches(ref Ref) bool {
	if e.Ref.Group != ref.Group || e.Ref.Kind != ref.Kind {
		return false
	}
	switch e.Scope {
	case ExceptionResource:
		return e.Ref.Namespace == ref.Namespace && e.Ref.Name == ref.Name
	case ExceptionKind:
		return e.Ref.Namespace == "" || e.Ref.Namespace == ref.Namespace
	default:
		return false
	}
}

// CoversPath сообщает, что путь исключения совпадает с path или является его предком (по границе сегмента).
func (e Exception) CoversPath(path string) bool {
	if path == e.Path {
		return true
	}
	if !strings.HasPrefix(path, e.Path) {
		return false
	}
	switch path[len(e.Path)] { // длина проверена: path длиннее e.Path
	case '.', '[':
		return true
	}
	return false
}

func hasControl(s string) bool {
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return true
		}
	}
	return false
}
