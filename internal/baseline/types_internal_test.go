// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package baseline

import (
	"encoding/base64"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/24DSO-lab/kubron/internal/tenant"
)

var (
	tNow  = time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)
	tFull = tenant.Scope{OrgID: "org", TeamID: "team", ClusterID: "cluster"}
)

func okVersion() Version {
	return Version{
		ID: "v1", BaselineID: "b1", Seq: 1, Source: SourceLiveSnapshot, Salt: []byte("salt"),
		Objects: []Object{{Ref: Ref{Version: "v1", Kind: "ConfigMap", Name: "x"}}},
	}
}

// Исключение подавляет изменения, поэтому границы его действия проверяются таблицей: лишнее совпадение скрыло бы
// настоящее изменение, а пропущенное создало бы ложный drift.
func TestExceptionMatches(t *testing.T) {
	t.Parallel()
	deploy := Ref{Group: "apps", Version: "v1", Kind: "Deployment", Namespace: "prod", Name: "web"}
	tests := []struct {
		name string
		exc  Exception
		ref  Ref
		want bool
	}{
		{"resource: тот же ресурс", Exception{Scope: ExceptionResource, Ref: Ref{Group: "apps", Kind: "Deployment", Namespace: "prod", Name: "web"}}, deploy, true},
		{"resource: другое имя", Exception{Scope: ExceptionResource, Ref: Ref{Group: "apps", Kind: "Deployment", Namespace: "prod", Name: "api"}}, deploy, false},
		{"resource: другое пространство имён", Exception{Scope: ExceptionResource, Ref: Ref{Group: "apps", Kind: "Deployment", Namespace: "stage", Name: "web"}}, deploy, false},
		{"resource: версия API не важна", Exception{Scope: ExceptionResource, Ref: Ref{Group: "apps", Version: "v1beta1", Kind: "Deployment", Namespace: "prod", Name: "web"}}, deploy, true},
		{"resource: другая группа", Exception{Scope: ExceptionResource, Ref: Ref{Group: "extensions", Kind: "Deployment", Namespace: "prod", Name: "web"}}, deploy, false},
		{"resource: другой вид", Exception{Scope: ExceptionResource, Ref: Ref{Group: "apps", Kind: "StatefulSet", Namespace: "prod", Name: "web"}}, deploy, false},
		{"kind: любое пространство имён", Exception{Scope: ExceptionKind, Ref: Ref{Group: "apps", Kind: "Deployment"}}, deploy, true},
		{"kind: своё пространство имён", Exception{Scope: ExceptionKind, Ref: Ref{Group: "apps", Kind: "Deployment", Namespace: "prod"}}, deploy, true},
		{"kind: чужое пространство имён", Exception{Scope: ExceptionKind, Ref: Ref{Group: "apps", Kind: "Deployment", Namespace: "stage"}}, deploy, false},
		{"kind: другой вид", Exception{Scope: ExceptionKind, Ref: Ref{Group: "apps", Kind: "DaemonSet"}}, deploy, false},
		{"неизвестная область не совпадает ни с чем", Exception{Scope: "other", Ref: Ref{Group: "apps", Kind: "Deployment"}}, deploy, false},
		{"пустая область не совпадает ни с чем", Exception{Ref: Ref{Group: "apps", Kind: "Deployment"}}, deploy, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := tc.exc.Matches(tc.ref); got != tc.want {
				t.Fatalf("Matches = %v, want %v", got, tc.want)
			}
		})
	}
}

// Путь исключения покрывает себя и вложенные поля, но не соседей с общим началом имени.
func TestExceptionCoversPath(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, exc, path string
		want            bool
	}{
		{"точное совпадение", "spec.replicas", "spec.replicas", true},
		{"вложенное поле", "spec.template", "spec.template.spec.containers[name=web].image", true},
		{"элемент списка", "spec.containers", "spec.containers[name=web]", true},
		{"сосед с общим префиксом имени", "spec.replica", "spec.replicas", false},
		{"предок уже пути исключения", "spec.template.spec", "spec.template", false},
		{"другое поддерево", "metadata.labels", "spec.replicas", false},
		{"путь короче", "spec.replicas.x", "spec.replicas", false},
		{"пустой путь не совпадает", "spec.replicas", "", false},
		{"разделитель сегмента обязателен", "spec.tem", "spec.template", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			e := Exception{Path: tc.exc}
			if got := e.CoversPath(tc.path); got != tc.want {
				t.Fatalf("CoversPath(%q) with %q = %v, want %v", tc.path, tc.exc, got, tc.want)
			}
		})
	}
}

// Граница срока: в момент ExpiresAt исключение уже не действует, без срока — действует всегда.
func TestExceptionExpired(t *testing.T) {
	t.Parallel()
	at := tNow
	before, after := at.Add(-time.Nanosecond), at.Add(time.Nanosecond)
	tests := []struct {
		name string
		exp  *time.Time
		now  time.Time
		want bool
	}{
		{"без срока", nil, after, false},
		{"до срока", &at, before, false},
		{"ровно в момент срока", &at, at, true},
		{"после срока", &at, after, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := (Exception{ExpiresAt: tc.exp}).Expired(tc.now); got != tc.want {
				t.Fatalf("Expired = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestExceptionValidate(t *testing.T) {
	t.Parallel()
	good := Exception{
		ID: "e1", Scope: ExceptionResource, Ref: Ref{Kind: "Deployment", Name: "web"},
		Path: "spec.replicas", Comment: "managed by the HPA",
	}
	mut := func(f func(*Exception)) Exception { e := good; f(&e); return e }
	tests := []struct {
		name string
		exc  Exception
		ok   bool
	}{
		{"корректное", good, true},
		{"kind-исключение без имени", mut(func(e *Exception) { e.Scope = ExceptionKind; e.Ref.Name = "" }), true},
		{"плохой ID", mut(func(e *Exception) { e.ID = "../x" }), false},
		{"пустой ID", mut(func(e *Exception) { e.ID = "" }), false},
		{"resource без имени", mut(func(e *Exception) { e.Ref.Name = "" }), false},
		{"resource без вида", mut(func(e *Exception) { e.Ref.Kind = "" }), false},
		{"kind без вида", mut(func(e *Exception) { e.Scope = ExceptionKind; e.Ref.Kind = "" }), false},
		{"неизвестная область", mut(func(e *Exception) { e.Scope = "x" }), false},
		{"пустой путь", mut(func(e *Exception) { e.Path = "" }), false},
		{"путь с управляющим символом", mut(func(e *Exception) { e.Path = "spec\n.replicas" }), false},
		{"путь с DEL", mut(func(e *Exception) { e.Path = "spec\x7f" }), false},
		{"путь на пределе", mut(func(e *Exception) { e.Path = strings.Repeat("a", MaxPathLength) }), true},
		{"путь длиннее предела", mut(func(e *Exception) { e.Path = strings.Repeat("a", MaxPathLength+1) }), false},
		{"комментарий из пробелов", mut(func(e *Exception) { e.Comment = " \t " }), false},
		{"комментарий на пределе", mut(func(e *Exception) { e.Comment = strings.Repeat("a", MaxCommentLength) }), true},
		{"комментарий длиннее предела", mut(func(e *Exception) { e.Comment = strings.Repeat("a", MaxCommentLength+1) }), false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := tc.exc.Validate()
			if tc.ok && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !tc.ok && !errors.Is(err, ErrInvalid) {
				t.Fatalf("error = %v, want ErrInvalid", err)
			}
		})
	}
}

func TestRefValidateAndKey(t *testing.T) {
	t.Parallel()
	for name, r := range map[string]Ref{
		"без версии": {Kind: "Pod", Name: "p"}, "без вида": {Version: "v1", Name: "p"}, "без имени": {Version: "v1", Kind: "Pod"},
	} {
		if err := r.Validate(); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: error = %v, want ErrInvalid", name, err)
		}
	}
	if err := (Ref{Version: "v1", Kind: "Pod", Name: "p"}).Validate(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Версия API при обновлении кластера меняется, идентичность ресурса — нет.
	a := Ref{Group: "apps", Version: "v1", Kind: "Deployment", Namespace: "n", Name: "x"}
	b := a
	b.Version = "v1beta1"
	if a.Key() != b.Key() {
		t.Fatalf("Key depends on the API version: %q vs %q", a.Key(), b.Key())
	}
	b.Namespace = "m"
	if a.Key() == b.Key() {
		t.Fatal("Key ignores the namespace")
	}
}

func TestBaselineValidate(t *testing.T) {
	t.Parallel()
	good := Baseline{ID: "b1", Scope: tFull, Name: "web", Refs: []Ref{{Version: "v1", Kind: "Pod", Name: "p"}}}
	mut := func(f func(*Baseline)) Baseline { b := good; f(&b); return b }
	tests := []struct {
		name string
		b    Baseline
		want error
	}{
		{"корректный", good, nil},
		{"без активной версии допустим", mut(func(b *Baseline) { b.ActiveVersionID = "" }), nil},
		{"с активной версией", mut(func(b *Baseline) { b.ActiveVersionID = "v1" }), nil},
		{"плохой ID", mut(func(b *Baseline) { b.ID = "a b" }), ErrInvalid},
		{"неполная область", mut(func(b *Baseline) { b.Scope.TeamID = "" }), ErrInvalidScope},
		{"имя из пробелов", mut(func(b *Baseline) { b.Name = "  " }), ErrInvalid},
		{"плохая активная версия", mut(func(b *Baseline) { b.ActiveVersionID = "../v" }), ErrInvalid},
		{"плохая ссылка на ресурс", mut(func(b *Baseline) { b.Refs = []Ref{{Kind: "Pod"}} }), ErrInvalid},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := tc.b.Validate()
			if !errors.Is(err, tc.want) { // errors.Is(nil, nil) истинно: ожидаемое «без ошибки»
				t.Fatalf("error = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestVersionValidate(t *testing.T) {
	t.Parallel()
	mut := func(f func(*Version)) Version { v := okVersion(); f(&v); return v }
	tests := []struct {
		name string
		v    Version
		ok   bool
	}{
		{"корректная", okVersion(), true},
		{"без объектов допустима", mut(func(v *Version) { v.Objects = nil }), true},
		{"плохой ID", mut(func(v *Version) { v.ID = "" }), false},
		{"плохой baseline", mut(func(v *Version) { v.BaselineID = "x/y" }), false},
		{"номер нуль", mut(func(v *Version) { v.Seq = 0 }), false},
		{"номер отрицательный", mut(func(v *Version) { v.Seq = -1 }), false},
		{"чужой источник", mut(func(v *Version) { v.Source = "git" }), false},
		{"без соли", mut(func(v *Version) { v.Salt = nil }), false},
		{"плохая ссылка объекта", mut(func(v *Version) { v.Objects = []Object{{Ref: Ref{Kind: "Pod"}}} }), false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := tc.v.Validate()
			if tc.ok != (err == nil) {
				t.Fatalf("error = %v, want ok=%v", err, tc.ok)
			}
			if err != nil && !errors.Is(err, ErrInvalid) {
				t.Fatalf("error = %v, want ErrInvalid", err)
			}
		})
	}
}

func TestCheckResultValidate(t *testing.T) {
	t.Parallel()
	good := CheckResult{ID: "r1", BaselineID: "b1", VersionID: "v1", CheckedAt: tNow}
	if err := good.Validate(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for name, f := range map[string]func(*CheckResult){
		"плохой ID":       func(r *CheckResult) { r.ID = "" },
		"плохой baseline": func(r *CheckResult) { r.BaselineID = ".." },
		"плохая версия":   func(r *CheckResult) { r.VersionID = "a/b" },
		"без времени":     func(r *CheckResult) { r.CheckedAt = time.Time{} },
	} {
		r := good
		f(&r)
		if err := r.Validate(); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: error = %v, want ErrInvalid", name, err)
		}
	}
}

// Идентификаторы попадают в имена файлов: всё, что может выйти из каталога или сломать путь, отклоняется.
func TestValidateIDAndScope(t *testing.T) {
	t.Parallel()
	for _, id := range []string{"a", "A-z_0", strings.Repeat("a", 64)} {
		if err := ValidateID(id); err != nil {
			t.Errorf("ValidateID(%q) = %v", id, err)
		}
	}
	for _, id := range []string{"", ".", "..", "a/b", `a\b`, "a b", "a\x00b", "a\n", "é", strings.Repeat("a", 65)} {
		if err := ValidateID(id); !errors.Is(err, ErrInvalid) {
			t.Errorf("ValidateID(%q) = %v, want ErrInvalid", id, err)
		}
	}
	if err := ValidateScope(tFull); err != nil {
		t.Fatalf("ValidateScope: %v", err)
	}
	for name, s := range map[string]tenant.Scope{
		"пустая":       {},
		"без кластера": {OrgID: "o", TeamID: "t"},
		"путь в орге":  {OrgID: "../o", TeamID: "t", ClusterID: "c"},
		"пробел":       {OrgID: "o", TeamID: "t t", ClusterID: "c"},
		"длинный":      {OrgID: "o", TeamID: "t", ClusterID: strings.Repeat("c", 81)},
	} {
		if err := ValidateScope(s); !errors.Is(err, ErrInvalidScope) {
			t.Errorf("%s: ValidateScope = %v, want ErrInvalidScope", name, err)
		}
	}
}

func TestFieldEqual(t *testing.T) {
	t.Parallel()
	h := func(b ...byte) Field { return Field{Hash: &HashedValue{HMAC: b}} }
	tests := []struct {
		name string
		a, b Field
		want bool
	}{
		{"одинаковые литералы", Field{Plain: "3"}, Field{Plain: "3"}, true},
		{"разные литералы", Field{Plain: "3"}, Field{Plain: "4"}, false},
		{"одинаковые hash", h(1, 2), h(1, 2), true},
		{"разные hash", h(1, 2), h(1, 3), false},
		{"литерал и hash никогда не равны", Field{Plain: "x"}, h(1), false},
		{"hash и литерал никогда не равны", h(1), Field{Plain: "x"}, false},
		{"пустой литерал и пустой hash не равны", Field{}, h(), false},
	}
	for _, tc := range tests {
		if got := tc.a.Equal(tc.b); got != tc.want {
			t.Errorf("%s: Equal = %v, want %v", tc.name, got, tc.want)
		}
	}
	// Секрет не должен всплыть ни в одном текстовом представлении.
	secret := Field{Hash: &HashedValue{HMAC: []byte("topsecret")}}
	for _, s := range []string{secret.String(), secret.GoString(), secret.Hash.String(), secret.Hash.GoString()} {
		if strings.Contains(s, "topsecret") || strings.Contains(s, "74 6f") {
			t.Errorf("hash leaked into %q", s)
		}
	}
}

// Normalized возвращает копию: вход не меняется, повторное применение ничего не меняет (иначе чтение и запись
// записей расходились бы между файловым и PostgreSQL хранилищами).
func TestNormalizedIsIdempotentAndDoesNotMutateInput(t *testing.T) {
	t.Parallel()
	exp := tNow.Add(time.Hour + 123*time.Nanosecond)
	v := okVersion()
	v.CreatedAt = tNow.Add(999 * time.Nanosecond)
	v.Objects[0].Fields = map[string]Field{"a": {Plain: "1"}, "b": {Hash: &HashedValue{HMAC: []byte{1, 2}}}}
	e := Exception{ID: "e1", CreatedAt: tNow.Add(7 * time.Nanosecond), ExpiresAt: &exp}
	b := Baseline{ID: "b1", Scope: tFull, Name: "n", CreatedAt: tNow.Add(5 * time.Nanosecond), Namespaces: []string{}, Kinds: []string{}}
	r := CheckResult{ID: "r1", CheckedAt: tNow.Add(3 * time.Nanosecond), Summary: Summary{UncheckedByReason: map[Reason]int{}}}

	eBefore, bBefore := e, b

	nv, ne, nb, nr := v.Normalized(), e.Normalized(), b.Normalized(), r.Normalized()
	if !reflect.DeepEqual(nv, nv.Normalized()) || !reflect.DeepEqual(ne, ne.Normalized()) ||
		!reflect.DeepEqual(nb, nb.Normalized()) || !reflect.DeepEqual(nr, nr.Normalized()) {
		t.Fatal("Normalized is not idempotent")
	}
	// Время обрезано до микросекунды и приведено к UTC.
	if nv.CreatedAt.Nanosecond()%1000 != 0 || ne.CreatedAt.Nanosecond()%1000 != 0 || ne.ExpiresAt.Nanosecond()%1000 != 0 {
		t.Fatal("times are not truncated to microseconds")
	}
	if nb.Namespaces != nil || nb.Kinds != nil || nb.Refs == nil {
		t.Fatalf("empty slices are not canonical: %#v", nb)
	}
	if nr.Summary.UncheckedByReason != nil {
		t.Fatal("an empty reason map must become nil")
	}
	// Вход не изменён.
	if v.CreatedAt.Nanosecond()%1000 == 0 || e.CreatedAt != eBefore.CreatedAt || b.CreatedAt != bBefore.CreatedAt {
		t.Fatal("Normalized mutated its receiver")
	}
	if exp.Nanosecond()%1000 == 0 {
		t.Fatal("Normalized mutated the shared ExpiresAt")
	}
	// Копия глубокая: правка копии не меняет оригинал.
	nv.Salt[0] ^= 0xff
	nv.Objects[0].Fields["b"].Hash.HMAC[0] ^= 0xff
	if v.Salt[0] != 's' || v.Objects[0].Fields["b"].Hash.HMAC[0] != 1 {
		t.Fatal("Normalized shares memory with the original")
	}
	if NormalizedExceptions(nil) != nil || NormalizedExceptions([]Exception{}) != nil {
		t.Fatal("an empty exception list must become nil")
	}
}

func TestSeqCursor(t *testing.T) {
	t.Parallel()
	for _, n := range []int{1, 2, 20, 1 << 30} {
		got, err := decodeSeqCursor(encodeSeqCursor(n))
		if err != nil || got != n {
			t.Errorf("round trip %d = %d, %v", n, got, err)
		}
	}
	enc := func(s string) string { return base64.RawURLEncoding.EncodeToString([]byte(s)) }
	for name, c := range map[string]string{
		"пустой":           "",
		"не base64":        "!!!",
		"чужой префикс":    enc("r:5"),
		"без префикса":     enc("5"),
		"не число":         enc("v:abc"),
		"нуль":             enc("v:0"),
		"отрицательное":    enc("v:-3"),
		"переполнение int": enc("v:99999999999999999999999"),
	} {
		if _, err := decodeSeqCursor(c); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: error = %v, want ErrInvalid", name, err)
		}
	}
}

func TestResultCursorAndFileName(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 11, 12, 10, 0, 0, 123456000, time.UTC)
	name := resultFileName(at, "r1")
	micro, id, ok := parseResultFile(name)
	if !ok || id != "r1" || micro != at.UnixMicro() {
		t.Fatalf("parseResultFile(%q) = %d, %q, %v", name, micro, id, ok)
	}
	got, err := decodeResultCursor(encodeResultCursor(name))
	if err != nil || got != name {
		t.Fatalf("round trip = %q, %v", got, err)
	}
	enc := func(s string) string { return base64.RawURLEncoding.EncodeToString([]byte(s)) }
	for name, c := range map[string]string{
		"пустой":            "",
		"не base64":         "***",
		"чужой префикс":     enc("v:" + name),
		"путь вместо имени": enc("r:../../etc/passwd"),
		"плохое имя":        enc("r:abc.json"),
	} {
		if _, err := decodeResultCursor(c); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: error = %v, want ErrInvalid", name, err)
		}
	}
	for _, bad := range []string{
		"", "x.json", "00000000000000000001.json", "00000000000000000001_.json", "00000000000000000001_r1",
		"0000000000000000001_r1.json", "0000000000000000000a_r1.json", "00000000000000000001_../x.json",
	} {
		if _, _, ok := parseResultFile(bad); ok {
			t.Errorf("parseResultFile(%q) accepted a malformed name", bad)
		}
	}
}

// Курсор приходит от клиента: любой вход даёт либо корректное значение, либо ErrInvalid, без паники.
func FuzzDecodeSeqCursor(f *testing.F) {
	f.Add(encodeSeqCursor(3))
	f.Add("")
	f.Add("djox")
	f.Add("v:1")
	f.Fuzz(func(t *testing.T, c string) {
		n, err := decodeSeqCursor(c)
		if err != nil {
			if !errors.Is(err, ErrInvalid) {
				t.Fatalf("error %v does not wrap ErrInvalid", err)
			}
			return
		}
		if n < 1 {
			t.Fatalf("decoded a non-positive sequence %d from %q", n, c)
		}
		// Принятое значение кодируется обратно в курсор, который декодируется в то же число.
		if back, err := decodeSeqCursor(encodeSeqCursor(n)); err != nil || back != n {
			t.Fatalf("re-encode of %d = %d, %v", n, back, err)
		}
	})
}

func FuzzDecodeResultCursor(f *testing.F) {
	f.Add(encodeResultCursor(resultFileName(tNow, "r1")))
	f.Add("")
	f.Add("cjouLi8uLi9ldGM")
	f.Fuzz(func(t *testing.T, c string) {
		name, err := decodeResultCursor(c)
		if err != nil {
			if !errors.Is(err, ErrInvalid) {
				t.Fatalf("error %v does not wrap ErrInvalid", err)
			}
			return
		}
		// Принятое имя безопасно как элемент пути: без разделителей, с допустимым ID.
		if strings.ContainsAny(name, `/\`) || strings.Contains(name, "..") {
			t.Fatalf("cursor %q yielded an unsafe file name %q", c, name)
		}
		if _, id, ok := parseResultFile(name); !ok || ValidateID(id) != nil {
			t.Fatalf("cursor %q yielded an unparsable name %q", c, name)
		}
	})
}
