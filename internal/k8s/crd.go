// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package k8s

import (
	"context"
	"encoding/json"

	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/util/jsonpath"
)

const (
	crdPageSize = 500
	crdMaxItems = 10000 // защита от гигантских списков; больше не читаем
)

// CRDColumn — колонка additionalPrinterColumns (как в kubectl get).
type CRDColumn struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	JSONPath    string `json:"jsonPath"`
	Description string `json:"description"`
	Priority    int    `json:"priority"` // 0 — в списке, >0 — только в панели (kubectl -o wide)
}

// CRDInfo — элемент списка и панели CustomResourceDefinition.
type CRDInfo struct {
	Name       string          `json:"name"`
	Group      string          `json:"group"`
	Kind       string          `json:"kind"`
	Plural     string          `json:"plural"`
	Singular   string          `json:"singular"`
	Scope      string          `json:"scope"`    // Namespaced | Cluster
	Version    string          `json:"version"`  // предпочтительная: storage, если обслуживается
	Versions   []string        `json:"versions"` // обслуживаемые
	ShortNames []string        `json:"shortNames"`
	Categories []string        `json:"categories"`
	Columns    []CRDColumn     `json:"columns"` // колонки Version
	Conditions []ConditionInfo `json:"conditions"`

	Age          string            `json:"age"`
	Created      string            `json:"created"`
	Labels       map[string]string `json:"labels"`
	Annotations  map[string]string `json:"annotations"`
	ControlledBy string            `json:"controlledBy"`
	ManagedBy    string            `json:"managedBy"`
}

// CustomResourceInfo — объект custom resource; Cells — значения printer-колонок по имени колонки.
type CustomResourceInfo struct {
	Name       string            `json:"name"`
	Namespace  string            `json:"namespace"`
	APIVersion string            `json:"apiVersion"`
	Kind       string            `json:"kind"`
	Age        string            `json:"age"`
	Created    string            `json:"created"`
	Cells      map[string]string `json:"cells"`
	Conditions []ConditionInfo   `json:"conditions"`
	Finalizers []string          `json:"finalizers"`

	Labels       map[string]string `json:"labels"`
	Annotations  map[string]string `json:"annotations"`
	ControlledBy string            `json:"controlledBy"`
	ManagedBy    string            `json:"managedBy"`
}

var dnsSegment = regexp.MustCompile(`^[a-z0-9]([-a-z0-9.]*[a-z0-9])?$`)

func (m *Manager) GetCRDs() ([]CRDInfo, error) {
	conn, err := m.GetActiveConnection()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(m.baseCtx(), 20*time.Second)
	defer cancel()
	return listCRDs(ctx, conn.DynamicClient)
}

func listCRDs(ctx context.Context, dyn dynamic.Interface) ([]CRDInfo, error) {
	list, err := dyn.Resource(kindTable["customresourcedefinition"].gvr).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list customresourcedefinitions: %w", err)
	}
	out := make([]CRDInfo, 0, len(list.Items))
	for i := range list.Items {
		out = append(out, parseCRD(&list.Items[i]))
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Group != out[j].Group {
			return out[i].Group < out[j].Group
		}
		return out[i].Kind < out[j].Kind
	})
	return out, nil
}

func parseCRD(u *unstructured.Unstructured) CRDInfo {
	obj := u.Object
	info := CRDInfo{
		Name:         u.GetName(),
		Group:        strAt(obj, "spec", "group"),
		Kind:         strAt(obj, "spec", "names", "kind"),
		Plural:       strAt(obj, "spec", "names", "plural"),
		Singular:     strAt(obj, "spec", "names", "singular"),
		Scope:        strAt(obj, "spec", "scope"),
		ShortNames:   stringsAt(obj, "spec", "names", "shortNames"),
		Categories:   stringsAt(obj, "spec", "names", "categories"),
		Versions:     []string{},
		Columns:      []CRDColumn{},
		Conditions:   []ConditionInfo{},
		Labels:       u.GetLabels(),
		Annotations:  u.GetAnnotations(),
		ControlledBy: controlledByOf(u.GetOwnerReferences()),
		ManagedBy:    managedByOf(metav1.ObjectMeta{Labels: u.GetLabels(), Annotations: u.GetAnnotations()}),
	}
	if ts := u.GetCreationTimestamp(); !ts.IsZero() {
		info.Age = formatAge(ts.Time)
		info.Created = ts.Format(time.RFC3339)
	}

	versions, _ := sliceAt(obj, "spec", "versions")
	var preferred, first map[string]interface{}
	for _, it := range versions {
		vm, ok := it.(map[string]interface{})
		if !ok || !boolAt(vm, "served") {
			continue
		}
		info.Versions = append(info.Versions, strAt(vm, "name"))
		if first == nil {
			first = vm
		}
		if boolAt(vm, "storage") {
			preferred = vm
		}
	}
	if preferred == nil {
		preferred = first
	}
	if preferred != nil {
		info.Version = strAt(preferred, "name")
		info.Columns = crdColumns(preferred)
	}

	conds, _ := sliceAt(obj, "status", "conditions")
	for _, it := range conds {
		if cm, ok := it.(map[string]interface{}); ok {
			info.Conditions = append(info.Conditions, conditionFrom(cm))
		}
	}
	return info
}

func conditionFrom(cm map[string]interface{}) ConditionInfo {
	return ConditionInfo{
		Type:               strAt(cm, "type"),
		Status:             strAt(cm, "status"),
		LastTransitionTime: strAt(cm, "lastTransitionTime"),
		Reason:             strAt(cm, "reason"),
		Message:            strAt(cm, "message"),
	}
}

// crdColumns: printer-колонки версии без Name и Age (их таблица рисует сама).
func crdColumns(version map[string]interface{}) []CRDColumn {
	items, _ := sliceAt(version, "additionalPrinterColumns")
	out := make([]CRDColumn, 0, len(items))
	for _, it := range items {
		cm, ok := it.(map[string]interface{})
		if !ok {
			continue
		}
		c := CRDColumn{
			Name:        strAt(cm, "name"),
			Type:        strAt(cm, "type"),
			JSONPath:    strAt(cm, "jsonPath"),
			Description: strAt(cm, "description"),
			Priority:    int(int64At(cm, "priority")),
		}
		if c.Name == "" || c.JSONPath == "" || c.Name == "Name" ||
			(c.JSONPath == ".metadata.creationTimestamp" && strings.EqualFold(c.Name, "Age")) {
			continue
		}
		out = append(out, c)
	}
	return out
}

// customKindFor проверяет, что group/version/plural принадлежат существующему CRD, и возвращает его GVR.
// Это единственный путь к произвольным ресурсам: встроенные типы (secrets и т.п.) CRD не имеют,
// поэтому через браузер CRD ни прочитать, ни изменить их нельзя.
func customKindFor(ctx context.Context, dyn dynamic.Interface, group, version, plural string) (kindInfo, *unstructured.Unstructured, error) {
	for _, s := range []string{group, version, plural} {
		if !dnsSegment.MatchString(s) {
			return kindInfo{}, nil, fmt.Errorf("invalid custom resource reference: %q", group+"/"+version+"/"+plural)
		}
	}
	crd, err := dyn.Resource(kindTable["customresourcedefinition"].gvr).Get(ctx, plural+"."+group, metav1.GetOptions{})
	if err != nil {
		return kindInfo{}, nil, fmt.Errorf("CRD %s.%s: %w", plural, group, err)
	}
	info := parseCRD(crd)
	served := false
	for _, v := range info.Versions {
		if v == version {
			served = true
		}
	}
	if !served {
		return kindInfo{}, nil, fmt.Errorf("version %s is not served by CRD %s", version, crd.GetName())
	}
	return kindInfo{gvr: gvr(group, version, plural), namespaced: info.Scope == "Namespaced"}, crd, nil
}

// customKindInfo разбирает ключ "cr:group/version/plural" (см. resourceClient).
func customKindInfo(conn *ClusterConnection, kind string) (kindInfo, error) {
	parts := strings.Split(strings.TrimPrefix(kind, customKindPrefix), "/")
	if len(parts) != 3 {
		return kindInfo{}, fmt.Errorf("resource type %q is not supported", kind)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ki, _, err := customKindFor(ctx, conn.DynamicClient, parts[0], parts[1], parts[2])
	return ki, err
}

func (m *Manager) GetCustomResources(group, version, plural string, namespaces []string) ([]CustomResourceInfo, error) {
	if len(namespaces) > 1 {
		// Кластерный CRD не зависит от namespace: набор схлопывается в один запрос,
		// иначе один и тот же список читался бы N раз и склеивался с дублями.
		conn, err := m.GetActiveConnection()
		if err != nil {
			return nil, err
		}
		ctx, cancel := context.WithTimeout(m.baseCtx(), 10*time.Second)
		ki, _, err := customKindFor(ctx, conn.DynamicClient, group, version, plural)
		cancel()
		if err != nil {
			return nil, err
		}
		if !ki.namespaced {
			namespaces = nil
		}
	}
	return scopedListTimeout(m, 30*time.Second, "GetCustomResources", group, plural, namespaces,
		func(ctx context.Context, conn *ClusterConnection, ns string) ([]CustomResourceInfo, error) {
			return listCustomResources(ctx, conn.DynamicClient, group, version, plural, ns)
		},
		func(it CustomResourceInfo) string { return it.Namespace })
}

func listCustomResources(ctx context.Context, dyn dynamic.Interface, group, version, plural, namespace string) ([]CustomResourceInfo, error) {
	ki, crd, err := customKindFor(ctx, dyn, group, version, plural)
	if err != nil {
		return nil, err
	}

	// printer-колонки запрошенной версии (у версий они могут отличаться)
	var cols []CRDColumn
	vs, _ := sliceAt(crd.Object, "spec", "versions")
	for _, it := range vs {
		if vm, ok := it.(map[string]interface{}); ok && strAt(vm, "name") == version {
			cols = crdColumns(vm)
		}
	}
	extractors := newCellExtractors(cols)

	var res dynamic.ResourceInterface = dyn.Resource(ki.gvr)
	if ki.namespaced && namespace != "" {
		res = dyn.Resource(ki.gvr).Namespace(namespace)
	}

	var items []unstructured.Unstructured
	opts := metav1.ListOptions{Limit: crdPageSize}
	for {
		l, err := res.List(ctx, opts)
		if err != nil {
			return nil, fmt.Errorf("list %s.%s: %w", plural, group, err)
		}
		items = append(items, l.Items...)
		if l.GetContinue() == "" || len(items) >= crdMaxItems {
			break
		}
		opts.Continue = l.GetContinue()
	}

	out := make([]CustomResourceInfo, 0, len(items))
	for i := range items {
		out = append(out, parseCustomResource(&items[i], extractors))
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Namespace != out[j].Namespace {
			return out[i].Namespace < out[j].Namespace
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

func parseCustomResource(u *unstructured.Unstructured, ex []cellExtractor) CustomResourceInfo {
	info := CustomResourceInfo{
		Name:         u.GetName(),
		Namespace:    u.GetNamespace(),
		APIVersion:   u.GetAPIVersion(),
		Kind:         u.GetKind(),
		Cells:        make(map[string]string, len(ex)),
		Conditions:   []ConditionInfo{},
		Finalizers:   u.GetFinalizers(),
		Labels:       u.GetLabels(),
		Annotations:  u.GetAnnotations(),
		ControlledBy: controlledByOf(u.GetOwnerReferences()),
		ManagedBy:    managedByOf(metav1.ObjectMeta{Labels: u.GetLabels(), Annotations: u.GetAnnotations()}),
	}
	if ts := u.GetCreationTimestamp(); !ts.IsZero() {
		info.Age = formatAge(ts.Time)
		info.Created = ts.Format(time.RFC3339)
	}
	for _, e := range ex {
		info.Cells[e.name] = e.value(u.Object)
	}
	conds, _ := sliceAt(u.Object, "status", "conditions")
	for _, it := range conds {
		if cm, ok := it.(map[string]interface{}); ok {
			info.Conditions = append(info.Conditions, conditionFrom(cm))
		}
	}
	return info
}

// ─── JSONPath-колонки ───

type cellExtractor struct {
	name, typ string
	jp        *jsonpath.JSONPath // nil, если выражение не разобралось
}

func newCellExtractors(cols []CRDColumn) []cellExtractor {
	out := make([]cellExtractor, 0, len(cols))
	for _, c := range cols {
		e := cellExtractor{name: c.Name, typ: c.Type}
		jp := jsonpath.New(c.Name).AllowMissingKeys(true)
		if err := jp.Parse("{" + c.JSONPath + "}"); err == nil {
			e.jp = jp
		}
		out = append(out, e)
	}
	return out
}

func (e cellExtractor) value(obj map[string]interface{}) string {
	if e.jp == nil {
		return ""
	}
	res, err := e.jp.FindResults(obj)
	if err != nil || len(res) == 0 || len(res[0]) == 0 {
		return ""
	}
	vals := make([]string, 0, len(res[0]))
	for _, v := range res[0] {
		if v.IsValid() && v.CanInterface() {
			vals = append(vals, formatCell(v.Interface(), e.typ))
		}
	}
	return strings.Join(vals, ",")
}

func formatCell(x interface{}, typ string) string {
	switch v := x.(type) {
	case nil:
		return ""
	case string:
		if typ == "date" {
			if t, err := time.Parse(time.RFC3339, v); err == nil {
				return formatAge(t)
			}
		}
		return v
	case bool:
		return strconv.FormatBool(v)
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	case int64:
		return strconv.FormatInt(v, 10)
	case int:
		return strconv.Itoa(v)
	}
	b, err := json.Marshal(x)
	if err != nil {
		return ""
	}
	return string(b)
}
