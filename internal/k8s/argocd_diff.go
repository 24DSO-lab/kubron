// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package k8s

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"

	"sigs.k8s.io/yaml"
)

const (
	ArgoDiffInSync   = "InSync"
	ArgoDiffModified = "Modified" // есть и в git, и в кластере, но различаются
	ArgoDiffMissing  = "Missing"  // есть в git, нет в кластере (будет создан)
	ArgoDiffExtra    = "Extra"    // есть в кластере, нет в git (будет удалён при prune)

	argoDiffContext       = 3
	argoDiffMaxResources  = 1000
	argoDiffMaxLines      = 2000    // предел размера манифеста для построчного сравнения (LCS квадратичен)
	argoDiffMaxTextBytes  = 200_000 // предел текста diff одного ресурса
	argoSecretRedacted    = "REDACTED"
	argoLastAppliedAnnKey = "kubectl.kubernetes.io/last-applied-configuration"
)

// ArgoResourceDiff — различие одного управляемого ресурса между git (желаемое) и кластером (текущее).
type ArgoResourceDiff struct {
	Group     string `json:"group"`
	Kind      string `json:"kind"`
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	Hook      bool   `json:"hook"`
	State     string `json:"state"`     // InSync | Modified | Missing | Extra
	Diff      string `json:"diff"`      // unified diff (текущее → желаемое); пусто для InSync
	Truncated bool   `json:"truncated"` // diff обрезан или не построен из-за размера
}

// ArgoDiff — результат сравнения всех управляемых ресурсов приложения.
type ArgoDiff struct {
	Resources []ArgoResourceDiff `json:"resources"`
	Total     int                `json:"total"`
	Changed   int                `json:"changed"`
	Truncated bool               `json:"truncated"` // список ресурсов обрезан до argoDiffMaxResources
}

// GetArgoApplicationDiff сравнивает желаемое (git) и текущее состояние ресурсов приложения через Argo CD API.
// Поднимает (или переиспользует) туннель к argocd-server и использует токен, сохранённый для активного кластера.
func (pfm *PortForwardManager) GetArgoApplicationDiff(namespace, name string) (*ArgoDiff, error) {
	token, err := loadArgoToken(pfm.k8sMgr.GetActiveContext())
	if err != nil {
		return nil, err // в том числе ErrArgoTokenNotSet: фронтенд предлагает задать токен
	}
	client, release, err := pfm.argoClient(token)
	if err != nil {
		return nil, err
	}
	defer release()
	ctx, cancel := context.WithTimeout(context.Background(), argoAPITimeout)
	defer cancel()
	items, err := client.managedResources(ctx, namespace, name)
	if err != nil {
		return nil, err
	}
	return buildArgoDiff(items), nil
}

func buildArgoDiff(items []argoManagedResource) *ArgoDiff {
	out := &ArgoDiff{Resources: make([]ArgoResourceDiff, 0, len(items))}
	for _, it := range items {
		d, ok := diffResource(it)
		if !ok {
			continue
		}
		out.Total++
		if d.State != ArgoDiffInSync {
			out.Changed++
		}
		out.Resources = append(out.Resources, d)
	}
	rank := func(s string) int {
		if s == ArgoDiffInSync {
			return 1
		}
		return 0
	}
	sort.SliceStable(out.Resources, func(i, j int) bool {
		a, b := out.Resources[i], out.Resources[j]
		if rank(a.State) != rank(b.State) {
			return rank(a.State) < rank(b.State) // различающиеся сверху
		}
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		if a.Namespace != b.Namespace {
			return a.Namespace < b.Namespace
		}
		return a.Name < b.Name
	})
	if len(out.Resources) > argoDiffMaxResources {
		out.Resources = out.Resources[:argoDiffMaxResources]
		out.Truncated = true
	}
	return out
}

// parseState разбирает JSON-манифест из поля *State; "", "null" и непарсящееся значение — «состояния нет».
func parseState(s string) map[string]any {
	s = strings.TrimSpace(s)
	if s == "" || s == "null" {
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(s), &m); err != nil || len(m) == 0 {
		return nil
	}
	return m
}

func diffResource(it argoManagedResource) (ArgoResourceDiff, bool) {
	// Argo сравнивает нормализованное текущее состояние с предсказанным («как было бы после apply»).
	liveRaw, desiredRaw := it.NormalizedLiveState, it.PredictedLiveState
	if strings.TrimSpace(liveRaw) == "" {
		liveRaw = it.LiveState
	}
	if strings.TrimSpace(desiredRaw) == "" {
		desiredRaw = it.TargetState
	}
	live, desired := parseState(liveRaw), parseState(desiredRaw)
	if live == nil && desired == nil {
		return ArgoResourceDiff{}, false
	}

	res := ArgoResourceDiff{Group: it.Group, Kind: it.Kind, Namespace: it.Namespace, Name: it.Name, Hook: it.Hook}
	cleanForDiff(live, desired)
	if it.Group == "" && it.Kind == "Secret" {
		redactSecret(live, desired)
	}

	liveText, desiredText := manifestLines(live), manifestLines(desired)
	switch {
	case live == nil:
		res.State = ArgoDiffMissing
	case desired == nil:
		res.State = ArgoDiffExtra
	case reflect.DeepEqual(liveText, desiredText):
		res.State = ArgoDiffInSync
		return res, true
	default:
		res.State = ArgoDiffModified
	}

	if len(liveText) > argoDiffMaxLines || len(desiredText) > argoDiffMaxLines {
		res.Truncated = true
		res.Diff = "(manifest is too large for a line-by-line diff)"
		return res, true
	}
	res.Diff = unifiedDiff(liveText, desiredText, argoDiffContext)
	if len(res.Diff) > argoDiffMaxTextBytes {
		res.Diff = res.Diff[:argoDiffMaxTextBytes] + "\n… (diff truncated)"
		res.Truncated = true
	}
	return res, true
}

// cleanForDiff убирает шум, который не отражает намерение: managedFields и status (если желаемое состояние его не содержит).
func cleanForDiff(live, desired map[string]any) {
	for _, m := range []map[string]any{live, desired} {
		if md, ok := m["metadata"].(map[string]any); ok {
			delete(md, "managedFields")
		}
	}
	if live != nil && desired != nil {
		if _, has := desired["status"]; !has {
			delete(live, "status")
		}
	}
}

// redactSecret скрывает значения data/stringData и копию данных в last-applied-configuration.
// Ключи остаются видимыми (добавление и удаление ключа видно в diff), а изменение значения помечается
// разными метками current/desired: сам факт различия виден, значения — нет.
func redactSecret(live, desired map[string]any) {
	for _, m := range []map[string]any{live, desired} {
		if md, ok := m["metadata"].(map[string]any); ok {
			if ann, ok := md["annotations"].(map[string]any); ok {
				delete(ann, argoLastAppliedAnnKey)
				if len(ann) == 0 {
					delete(md, "annotations")
				}
			}
		}
	}
	for _, field := range []string{"data", "stringData"} {
		lm, _ := live[field].(map[string]any)
		dm, _ := desired[field].(map[string]any)
		keys := map[string]bool{}
		for k := range lm {
			keys[k] = true
		}
		for k := range dm {
			keys[k] = true
		}
		for k := range keys {
			lv, lok := lm[k]
			dv, dok := dm[k]
			switch {
			case lok && dok && !reflect.DeepEqual(lv, dv):
				lm[k] = argoSecretRedacted + " (current)"
				dm[k] = argoSecretRedacted + " (desired)"
			default:
				if lok {
					lm[k] = argoSecretRedacted
				}
				if dok {
					dm[k] = argoSecretRedacted
				}
			}
		}
	}
}

// manifestLines — YAML манифеста построчно; nil → nil.
func manifestLines(m map[string]any) []string {
	if m == nil {
		return nil
	}
	b, err := yaml.Marshal(m)
	if err != nil {
		return []string{"(failed to serialize manifest)"}
	}
	return strings.Split(strings.TrimRight(string(b), "\n"), "\n")
}

type diffOp struct {
	kind byte // ' ', '-', '+'
	line string
}

// lineDiff — построчный diff через наибольшую общую подпоследовательность.
func lineDiff(a, b []string) []diffOp {
	n, m := len(a), len(b)
	dp := make([][]int32, n+1)
	for i := range dp {
		dp[i] = make([]int32, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			switch {
			case a[i] == b[j]:
				dp[i][j] = dp[i+1][j+1] + 1
			case dp[i+1][j] >= dp[i][j+1]:
				dp[i][j] = dp[i+1][j]
			default:
				dp[i][j] = dp[i][j+1]
			}
		}
	}
	ops := make([]diffOp, 0, n+m)
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case a[i] == b[j]:
			ops = append(ops, diffOp{' ', a[i]})
			i++
			j++
		case dp[i+1][j] >= dp[i][j+1]:
			ops = append(ops, diffOp{'-', a[i]})
			i++
		default:
			ops = append(ops, diffOp{'+', b[j]})
			j++
		}
	}
	for ; i < n; i++ {
		ops = append(ops, diffOp{'-', a[i]})
	}
	for ; j < m; j++ {
		ops = append(ops, diffOp{'+', b[j]})
	}
	return ops
}

// unifiedDiff форматирует различия a → b в стиле unified diff с ctx строками контекста.
func unifiedDiff(a, b []string, ctx int) string {
	ops := lineDiff(a, b)

	// Номера строк (1-based) каждой операции в a и b.
	aLine := make([]int, len(ops))
	bLine := make([]int, len(ops))
	al, bl := 1, 1
	for i, op := range ops {
		aLine[i], bLine[i] = al, bl
		if op.kind != '+' {
			al++
		}
		if op.kind != '-' {
			bl++
		}
	}

	// В hunk попадают изменения и ctx строк вокруг них.
	include := make([]bool, len(ops))
	for i, op := range ops {
		if op.kind == ' ' {
			continue
		}
		lo, hi := i-ctx, i+ctx
		if lo < 0 {
			lo = 0
		}
		if hi >= len(ops) {
			hi = len(ops) - 1
		}
		for k := lo; k <= hi; k++ {
			include[k] = true
		}
	}

	var sb strings.Builder
	for i := 0; i < len(ops); {
		if !include[i] {
			i++
			continue
		}
		start := i
		aLen, bLen := 0, 0
		for i < len(ops) && include[i] {
			if ops[i].kind != '+' {
				aLen++
			}
			if ops[i].kind != '-' {
				bLen++
			}
			i++
		}
		aStart, bStart := aLine[start], bLine[start]
		// В unified diff при пустой стороне номер указывает на строку перед hunk.
		if aLen == 0 {
			aStart--
		}
		if bLen == 0 {
			bStart--
		}
		fmt.Fprintf(&sb, "@@ -%d,%d +%d,%d @@\n", aStart, aLen, bStart, bLen)
		for k := start; k < i; k++ {
			sb.WriteByte(ops[k].kind)
			sb.WriteString(ops[k].line)
			sb.WriteByte('\n')
		}
	}
	return strings.TrimRight(sb.String(), "\n")
}
