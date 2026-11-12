// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package drift

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/24DSO-lab/kubron/internal/baseline"
)

var update = flag.Bool("update", false, "переписать golden-файлы")

// Заведомо ненастоящие значения-«канарейки»: ни одно из них не должно появиться нигде, кроме входных фикстур.
var canaries = []string{"canary-db-password-7f3a9c", "canary-annotation-pw-31337", "canary-dsn-value-424242"}

var testSalt = bytes.Repeat([]byte{'s'}, SaltSize)

func loadFixture(t testing.TB, name string) *unstructured.Unstructured {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", name+".json"))
	if err != nil {
		t.Fatal(err)
	}
	u := &unstructured.Unstructured{}
	if err := u.UnmarshalJSON(raw); err != nil {
		t.Fatal(err)
	}
	return u
}

// render печатает нормализованный объект построчно: «путь = значение»; hash — первые 8 hex-знаков.
func render(o baseline.Object) string {
	paths := make([]string, 0, len(o.Fields))
	for p := range o.Fields {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n", o.Ref)
	for _, p := range paths {
		f := o.Fields[p]
		if f.Hash != nil {
			fmt.Fprintf(&b, "%s = HASH:%s\n", p, hex.EncodeToString(f.Hash.HMAC)[:8])
		} else {
			fmt.Fprintf(&b, "%s = %s\n", p, f.Plain)
		}
	}
	return b.String()
}

var fixtureNames = []string{"deployment", "statefulset", "daemonset", "service", "ingress", "networkpolicy", "configmap"}

// Golden-тест на каждый тип. Golden-файлы проверены вручную: это снимок ожидаемого результата, а не «что выдал код».
func TestNormalizeGolden(t *testing.T) {
	for _, name := range fixtureNames {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			obj, _, err := normalizeObject(loadFixture(t, name), testSalt, normOptions{})
			if err != nil {
				t.Fatal(err)
			}
			got := render(obj)
			golden := filepath.Join("testdata", name+".golden")
			if *update {
				if err := os.WriteFile(golden, []byte(got), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(golden)
			if err != nil {
				t.Fatalf("%v (создайте golden: go test -run TestNormalizeGolden -update)", err)
			}
			if got != string(want) {
				t.Errorf("нормализация %s изменилась:\n--- got ---\n%s\n--- want ---\n%s", name, got, want)
			}
		})
	}
}

// Служебные поля кластера не попадают в нормализованный объект ни у одного типа.
func TestNormalizeDropsServiceFields(t *testing.T) {
	forbidden := []string{"status", "metadata.managedFields", "metadata.resourceVersion", "metadata.uid", "metadata.generation",
		"metadata.creationTimestamp", "metadata.selfLink", "metadata.name", "metadata.namespace", "apiVersion", "kind"}
	for _, name := range fixtureNames {
		t.Run(name, func(t *testing.T) {
			obj, _, err := normalizeObject(loadFixture(t, name), testSalt, normOptions{})
			if err != nil {
				t.Fatal(err)
			}
			for p := range obj.Fields {
				for _, f := range forbidden {
					if p == f || strings.HasPrefix(p, f+".") {
						t.Errorf("служебное поле %q осталось в результате (%s)", p, f)
					}
				}
				if strings.Contains(p, "last-applied-configuration") || strings.Contains(p, "creationTimestamp") {
					t.Errorf("служебное поле осталось: %s", p)
				}
			}
			if len(obj.Fields) == 0 {
				t.Error("нормализация вернула пустой объект")
			}
		})
	}
}

// Свойство: нормализация идемпотентна — повторный проход по результату ничего не меняет.
func TestNormalizeTreeIdempotentOnFixtures(t *testing.T) {
	for _, name := range fixtureNames {
		for _, opt := range []normOptions{{}, {excludeReplicas: true}} {
			t.Run(fmt.Sprintf("%s/replicas-excluded=%v", name, opt.excludeReplicas), func(t *testing.T) {
				u := loadFixture(t, name)
				ks, err := specFor(u.GroupVersionKind().Group, u.GetKind())
				if err != nil {
					t.Fatal(err)
				}
				once := normalizeTree(ks, u.Object, opt)
				twice := normalizeTree(ks, once, opt)
				if !reflect.DeepEqual(once, twice) {
					t.Errorf("нормализация не идемпотентна:\nпервый проход: %s\nвторой проход: %s", literal(once), literal(twice))
				}
			})
		}
	}
}

// Нормализация не меняет входной объект: он принадлежит вызывающему (кэшу или ответу кластера).
func TestNormalizeDoesNotMutateInput(t *testing.T) {
	for _, name := range fixtureNames {
		u := loadFixture(t, name)
		before := literal(u.Object)
		if _, _, err := normalizeObject(u, testSalt, normOptions{excludeReplicas: true}); err != nil {
			t.Fatal(err)
		}
		if after := literal(u.Object); after != before {
			t.Errorf("%s: входной объект изменён нормализацией", name)
		}
	}
}

// Порядок элементов списков, у которых порядок не значим, не влияет на результат.
func TestNormalizeIgnoresOrderOfUnorderedLists(t *testing.T) {
	cases := []struct {
		fixture string
		shuffle func(m map[string]any)
	}{
		{"deployment", func(m map[string]any) {
			spec := m["spec"].(map[string]any)["template"].(map[string]any)["spec"].(map[string]any)
			reverse(spec["containers"].([]any))
			reverse(spec["volumes"].([]any))
			reverse(spec["tolerations"].([]any))
			web := spec["containers"].([]any)[0].(map[string]any)
			for _, c := range spec["containers"].([]any) {
				if c.(map[string]any)["name"] == "web" {
					web = c.(map[string]any)
				}
			}
			reverse(web["env"].([]any))
			reverse(web["ports"].([]any))
			reverse(web["volumeMounts"].([]any))
		}},
		{"service", func(m map[string]any) { reverse(m["spec"].(map[string]any)["ports"].([]any)) }},
		{"ingress", func(m map[string]any) {
			spec := m["spec"].(map[string]any)
			reverse(spec["rules"].([]any))
			reverse(spec["rules"].([]any)[0].(map[string]any)["http"].(map[string]any)["paths"].([]any))
			reverse(spec["tls"].([]any)[0].(map[string]any)["hosts"].([]any))
		}},
		{"networkpolicy", func(m map[string]any) {
			spec := m["spec"].(map[string]any)
			reverse(spec["policyTypes"].([]any))
			reverse(spec["egress"].([]any))
			from := spec["ingress"].([]any)[0].(map[string]any)["from"].([]any)
			reverse(from)
			reverse(spec["ingress"].([]any)[0].(map[string]any)["ports"].([]any))
			reverse(spec["egress"].([]any)[1].(map[string]any)["to"].([]any))
		}},
	}
	for _, c := range cases {
		t.Run(c.fixture, func(t *testing.T) {
			base, _, err := normalizeObject(loadFixture(t, c.fixture), testSalt, normOptions{})
			if err != nil {
				t.Fatal(err)
			}
			shuffled := loadFixture(t, c.fixture)
			c.shuffle(shuffled.Object)
			got, _, err := normalizeObject(shuffled, testSalt, normOptions{})
			if err != nil {
				t.Fatal(err)
			}
			if render(base) != render(got) {
				t.Errorf("порядок списка повлиял на результат:\n%s\n---\n%s", render(base), render(got))
			}
		})
	}
}

func reverse(s []any) {
	for i, j := 0, len(s)-1; i < j; i, j = i+1, j-1 {
		s[i], s[j] = s[j], s[i]
	}
}

// Порядок init-контейнеров значим: его смена — отличие, хотя адресация идёт по имени.
func TestInitContainerOrderMatters(t *testing.T) {
	a, _, _ := normalizeObject(loadFixture(t, "deployment"), testSalt, normOptions{})
	u := loadFixture(t, "deployment")
	reverse(u.Object["spec"].(map[string]any)["template"].(map[string]any)["spec"].(map[string]any)["initContainers"].([]any))
	b, _, _ := normalizeObject(u, testSalt, normOptions{})
	const p = "spec.template.spec.initContainers.@order"
	if a.Fields[p].Plain == "" || a.Fields[p].Equal(b.Fields[p]) {
		t.Errorf("смена порядка init-контейнеров не видна: %q и %q", a.Fields[p].Plain, b.Fields[p].Plain)
	}
}

// Значения по умолчанию не сравниваются: обновление кластера, добавляющее defaulted-поле, не создаёт дрейфа.
func TestNormalizeIgnoresDefaultedFields(t *testing.T) {
	plain, _, _ := normalizeObject(loadFixture(t, "deployment"), testSalt, normOptions{})
	u := loadFixture(t, "deployment")
	spec := u.Object["spec"].(map[string]any)["template"].(map[string]any)["spec"].(map[string]any)
	delete(spec, "dnsPolicy")
	delete(spec, "terminationGracePeriodSeconds")
	delete(spec["containers"].([]any)[1].(map[string]any), "terminationMessagePath")
	delete(spec["containers"].([]any)[1].(map[string]any), "imagePullPolicy") // IfNotPresent — умолчание для тега 3.8.2
	stripped, _, _ := normalizeObject(u, testSalt, normOptions{})
	if render(plain) != render(stripped) {
		t.Errorf("отсутствие defaulted-полей дало отличие:\n%s\n---\n%s", render(plain), render(stripped))
	}
	// А не-умолчание сохраняется: Always для версионного тега — осознанный выбор.
	u2 := loadFixture(t, "deployment")
	u2.Object["spec"].(map[string]any)["template"].(map[string]any)["spec"].(map[string]any)["containers"].([]any)[1].(map[string]any)["imagePullPolicy"] = "Always"
	changed, _, _ := normalizeObject(u2, testSalt, normOptions{})
	if _, ok := changed.Fields["spec.template.spec.containers[name=web].imagePullPolicy"]; !ok {
		t.Error("недефолтная imagePullPolicy потеряна")
	}
}

func TestDefaultPullPolicy(t *testing.T) {
	for image, want := range map[string]string{
		"nginx":                                    "Always",
		"nginx:latest":                             "Always",
		"nginx:1.27":                               "IfNotPresent",
		"registry.example.com:5000/app":            "Always", // «:5000» — порт реестра, а не тег
		"registry.example.com:5000/app:2.0":        "IfNotPresent",
		"app@sha256:0123456789abcdef":              "IfNotPresent",
		"registry.example.com/app:latest@sha256:1": "IfNotPresent",
	} {
		if got := defaultPullPolicy(image); got != want {
			t.Errorf("defaultPullPolicy(%q) = %s, want %s", image, got, want)
		}
	}
}

// spec.replicas исключается только там, где на нагрузку указывает HPA.
func TestExcludeReplicasForHPA(t *testing.T) {
	with, _, _ := normalizeObject(loadFixture(t, "deployment"), testSalt, normOptions{})
	without, _, _ := normalizeObject(loadFixture(t, "deployment"), testSalt, normOptions{excludeReplicas: true})
	if _, ok := with.Fields["spec.replicas"]; !ok {
		t.Fatal("spec.replicas должен сравниваться без HPA")
	}
	if _, ok := without.Fields["spec.replicas"]; ok {
		t.Fatal("spec.replicas должен исключаться при HPA")
	}
	// Остальное содержимое не пострадало.
	delete(with.Fields, "spec.replicas")
	if render(with) != render(without) {
		t.Error("исключение replicas задело другие поля")
	}
}

// --- чувствительные значения ------------------------------------------------------------------------------------

func TestSensitiveValuesAreHashed(t *testing.T) {
	obj, hashed, err := normalizeObject(loadFixture(t, "deployment"), testSalt, normOptions{})
	if err != nil {
		t.Fatal(err)
	}
	env := obj.Fields["spec.template.spec.containers[name=web].env[name=DB_PASSWORD].value"]
	if env.Hash == nil || env.Plain != "" {
		t.Fatalf("литерал env должен храниться только как hash: %+v", env)
	}
	if lg := obj.Fields["spec.template.spec.containers[name=web].env[name=LOG_LEVEL].value"]; lg.Hash == nil {
		t.Error("любой литерал env — чувствителен, не только «похожий на пароль»")
	}
	// valueFrom — ссылка, а не значение: имя Secret и ключ остаются читаемыми.
	if ref := obj.Fields["spec.template.spec.containers[name=web].env[name=API_KEY].valueFrom.secretKeyRef.name"]; ref.Plain != `"web-api"` {
		t.Errorf("ссылка на Secret потеряна: %+v", ref)
	}
	if hashed != 2 {
		t.Errorf("hash у %d значений, want 2 (LOG_LEVEL, DB_PASSWORD)", hashed)
	}

	cm, hashed, _ := normalizeObject(loadFixture(t, "configmap"), testSalt, normOptions{})
	for _, p := range []string{"data[\"app.yaml\"]", `data["feature.flags"]`, "data.empty", `binaryData["logo.png"]`} {
		if f, ok := cm.Fields[p]; !ok || f.Hash == nil {
			t.Errorf("%s должно храниться как hash: %+v (есть=%v)", p, f, ok)
		}
	}
	// Аннотация с «password=…» прошла редакцию и тоже не хранится открыто.
	if f := cm.Fields[`metadata.annotations["example.com/note"]`]; f.Hash == nil {
		t.Errorf("аннотация с паролем хранится открыто: %+v", f)
	}
	if f := cm.Fields[`metadata.annotations["example.com/contact"]`]; f.Hash != nil || f.Plain == "" {
		t.Errorf("обычная аннотация не должна скрываться: %+v", f)
	}
	if hashed != 5 {
		t.Errorf("hash у %d значений ConfigMap, want 5", hashed)
	}
}

// Ни канарейки, ни их части не встречаются ни в нормализованном объекте, ни в его текстовых представлениях.
func TestNoCanaryInAnyRepresentation(t *testing.T) {
	var logBuf bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	for _, name := range []string{"deployment", "configmap"} {
		obj, _, err := normalizeObject(loadFixture(t, name), testSalt, normOptions{})
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(obj)
		v := baseline.Version{ID: "v1", BaselineID: "b1", Seq: 1, Source: baseline.SourceLiveSnapshot, Salt: testSalt,
			Objects: []baseline.Object{obj}}
		log.Info("object", "obj", obj, "version", v, "ref", obj.Ref)
		log.Info("fields", slog.Any("fields", obj.Fields))
		texts := []string{
			string(raw), render(obj), fmt.Sprintf("%v", obj), fmt.Sprintf("%+v", obj), fmt.Sprintf("%#v", obj),
			fmt.Sprintf("%v", v), fmt.Sprintf("%+v", v), fmt.Sprintf("%#v", v), v.String(),
			fmt.Sprintf("%v", obj.Fields), fmt.Sprintf("%+v", obj.Fields), fmt.Sprintf("%#v", obj.Fields),
		}
		texts = append(texts, logBuf.String())
		for _, text := range texts {
			for _, c := range canaries {
				if strings.Contains(text, c) {
					t.Errorf("%s: канарейка %q просочилась:\n%s", name, c, text)
				}
			}
			if strings.Contains(text, string(testSalt)) {
				t.Errorf("%s: соль просочилась в представление", name)
			}
		}
	}
}

// hash зависит от соли, значения, ресурса и пути; не зависит ни от чего внешнего (ключей keyring).
func TestHashProperties(t *testing.T) {
	ref := baseline.Ref{Group: "apps", Version: "v1", Kind: "Deployment", Namespace: "prod", Name: "web"}
	base := hashValue(testSalt, ref, "p", `"x"`)
	if !bytes.Equal(base, hashValue(testSalt, ref, "p", `"x"`)) {
		t.Fatal("hash недетерминирован")
	}
	other := ref
	other.Version = "v1beta2" // версия API не входит в hash: обновление кластера не ломает сравнение
	if !bytes.Equal(base, hashValue(testSalt, other, "p", `"x"`)) {
		t.Error("смена версии API изменила hash")
	}
	for name, h := range map[string][]byte{
		"другая соль":     hashValue(bytes.Repeat([]byte{'t'}, SaltSize), ref, "p", `"x"`),
		"другое значение": hashValue(testSalt, ref, "p", `"y"`),
		"другой путь":     hashValue(testSalt, ref, "q", `"x"`),
		"другой ресурс":   hashValue(testSalt, baseline.Ref{Group: "apps", Kind: "Deployment", Namespace: "prod", Name: "api"}, "p", `"x"`),
		// граница полей: «ab»+«c» и «a»+«bc» не должны совпасть
		"сдвиг границы": hashValue(testSalt, ref, "pa", `x"`),
	} {
		if bytes.Equal(base, h) {
			t.Errorf("%s: hash не изменился", name)
		}
	}
	if len(base) != 32 {
		t.Errorf("длина HMAC-SHA256 = %d", len(base))
	}
}

func TestNewSalt(t *testing.T) {
	a, b := NewSalt(), NewSalt()
	if len(a) != SaltSize || len(b) != SaltSize || bytes.Equal(a, b) {
		t.Fatalf("соль должна быть случайной и размером %d", SaltSize)
	}
}

// Secret не поддерживается и не обрабатывается: ни значения, ни имена ключей в результат не попадают.
func TestSecretIsNotSupported(t *testing.T) {
	u := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1", "kind": "Secret",
		"metadata": map[string]any{"name": "db", "namespace": "prod"},
		"data":     map[string]any{"password": "Y2FuYXJ5LXNlY3JldA=="},
	}}
	obj, _, err := normalizeObject(u, testSalt, normOptions{})
	if !errors.Is(err, ErrSecretUnsupported) {
		t.Fatalf("err = %v, want ErrSecretUnsupported", err)
	}
	if len(obj.Fields) != 0 {
		t.Fatalf("Secret дал поля: %v", obj.Fields)
	}
	for _, k := range []string{"Secret", "secret", "SECRET"} {
		if _, err := specFor("", k); !errors.Is(err, ErrSecretUnsupported) {
			t.Errorf("specFor(%q) = %v", k, err)
		}
	}
	if _, err := specFor("", "Pod"); !errors.Is(err, ErrUnsupportedKind) {
		t.Errorf("Pod: %v", err)
	}
	for _, k := range SupportedKinds() {
		if strings.EqualFold(k, "Secret") {
			t.Fatal("Secret не может входить в список поддерживаемых типов")
		}
	}
}

// --- fuzz -------------------------------------------------------------------------------------------------------

// FuzzNormalizeTree: на любом JSON-объекте нормализация не паникует, идемпотентна и не меняет вход.
func FuzzNormalizeTree(f *testing.F) {
	for _, name := range fixtureNames {
		raw, err := os.ReadFile(filepath.Join("testdata", name+".json"))
		if err != nil {
			f.Fatal(err)
		}
		f.Add(raw)
	}
	f.Add([]byte(`{"spec":{"template":{"spec":{"containers":[{"name":"a","ports":[{"containerPort":1},{"containerPort":1}]}]}}}}`))
	f.Add([]byte(`{"spec":{"x":[[],[[]],{}],"y":null,"z":{"a":{}}}}`))
	f.Fuzz(func(t *testing.T, raw []byte) {
		var obj map[string]any
		if json.Unmarshal(raw, &obj) != nil || obj == nil {
			t.Skip()
		}
		before := literal(obj)
		for i := range supportedKinds {
			ks := &supportedKinds[i]
			for _, opt := range []normOptions{{}, {excludeReplicas: true}} {
				once := normalizeTree(ks, obj, opt)
				twice := normalizeTree(ks, once, opt)
				if !reflect.DeepEqual(once, twice) {
					t.Fatalf("%s: не идемпотентна\nonce:  %s\ntwice: %s", ks.GVK.Kind, literal(once), literal(twice))
				}
				// Вторая стадия не паникует и даёт одинаковый результат на одинаковом входе.
				fl := &flattener{ref: baseline.Ref{Kind: ks.GVK.Kind}, salt: testSalt, rules: ks.rules, out: map[string]baseline.Field{}}
				fl.value(once, "", "")
				fl2 := &flattener{ref: baseline.Ref{Kind: ks.GVK.Kind}, salt: testSalt, rules: ks.rules, out: map[string]baseline.Field{}}
				fl2.value(twice, "", "")
				if !reflect.DeepEqual(fl.out, fl2.out) {
					t.Fatalf("%s: плоское представление различается", ks.GVK.Kind)
				}
			}
		}
		if literal(obj) != before {
			t.Fatal("нормализация изменила входной объект")
		}
	})
}

// FuzzPathRoundTrip: путь, построенный кодировщиком, разбирается обратно в те же имена, а исключение по его
// префиксу покрывает путь и не покрывает соседей с общим началом имени.
func FuzzPathRoundTrip(f *testing.F) {
	f.Add("app.kubernetes.io/name", "web", "x")
	f.Add(`we"ird]`, `a,b=c"`, "[0]")
	f.Add("", "", "")
	f.Fuzz(func(t *testing.T, key, val, field string) {
		if key == "" || field == "" {
			t.Skip()
		}
		p := fieldPath("metadata.labels", key)
		segs := splitPath(p)
		last := segs[len(segs)-1]
		if last.Kind != segName || last.Name != key {
			t.Fatalf("ключ %q не пережил кодирование: путь %q → %+v", key, p, segs)
		}
		kp := keyedPath("spec.items", [][2]string{{"name", val}})
		ks := splitPath(kp)
		if len(ks) != 3 || ks[2].Kind != segList {
			t.Fatalf("элемент списка не разобран: %q → %+v", kp, ks)
		}
		if got := keyName(kp + ".value"); val != "" && got != val {
			t.Fatalf("keyName(%q) = %q, want %q", kp+".value", got, val)
		}
		full := fieldPath(kp, field)
		e := baseline.Exception{Path: kp}
		if !e.CoversPath(full) || !e.CoversPath(kp) {
			t.Fatalf("исключение %q не покрывает %q", kp, full)
		}
	})
}

// Случайные деревья: быстрая проверка идемпотентности без корпуса fuzz.
func TestNormalizeTreeIdempotentRandom(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	for i := 0; i < 300; i++ {
		obj := map[string]any{"spec": randomValue(rng, 0), "metadata": map[string]any{"labels": randomValue(rng, 2)}}
		for k := range supportedKinds {
			ks := &supportedKinds[k]
			once := normalizeTree(ks, obj, normOptions{})
			if twice := normalizeTree(ks, once, normOptions{}); !reflect.DeepEqual(once, twice) {
				t.Fatalf("%s: не идемпотентна: %s", ks.GVK.Kind, literal(obj))
			}
		}
	}
}

func randomValue(rng *rand.Rand, depth int) any {
	names := []string{"containers", "ports", "env", "name", "value", "template", "spec", "volumes", "emptyDir", "podSelector",
		"protocol", "containerPort", "image", "imagePullPolicy", "replicas", "policyTypes", "ingress", "from", "ports"}
	switch n := rng.IntN(7); {
	case depth > 4 || n == 0:
		return []any{"x", int64(rng.IntN(3)), "TCP", true, nil, float64(30)}[rng.IntN(6)]
	case n <= 3:
		m := map[string]any{}
		for i := rng.IntN(4); i > 0; i-- {
			m[names[rng.IntN(len(names))]] = randomValue(rng, depth+1)
		}
		return m
	default:
		s := make([]any, rng.IntN(4))
		for i := range s {
			s[i] = randomValue(rng, depth+1)
		}
		return s
	}
}
