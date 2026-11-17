// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package diagnose

import (
	"cmp"
	"context"
	"fmt"
	"slices"
)

// Реестры расширения ядра (ADR-0031 §4–§5, ADR-0032 §6, ADR-0034 §3). Расширения (граф — D3, метрики — D1,
// корреляция — D4) подключаются своим файлом пакета через init, а не правкой общих функций: так ядро остаётся
// одним и тем же для всех, а порядок работы определяется числом order, а не порядком файлов.
//
// Регистрация — только из init (до первого вызова Collect/Analyze): реестры не защищены от параллельной записи.
// Повторная регистрация того же имени, пустое имя, nil-функция и недопустимая эпоха — ошибка программиста, поэтому
// panic при старте, а не тихое поведение во время расследования.
//
// Порядок order, зарезервированный контрактом:
//
//	хуки сбора: 100 — граф (D3), 200 — сигналы метрик (D1), 300 и выше — резерв;
//	пост-хуки:  100 — цепочки и Explains (D3), 200 — метрики (D1).

// CollectHook выполняется после базового сбора (и после хуков с меньшим order). Ошибки нет: сбой источника —
// это c.Missing или c.Partial, а не отказ расследования. Паника хука перехватывается и становится MissingContext
// «rule.failed» с именем хука.
type CollectHook func(ctx context.Context, r Reader, c *Context)

type collectHookEntry struct {
	order int
	name  string
	run   CollectHook
}

type postHookEntry struct {
	order int
	name  string
	run   func(b *builder)
}

var (
	collectHooks []collectHookEntry
	postHooks    []postHookEntry
)

// RegisterCollectHook добавляет хук сбора. Хуки идут по возрастанию order, при равном order — в порядке регистрации.
func RegisterCollectHook(order int, name string, h CollectHook) {
	if name == "" || h == nil {
		panic("diagnose: collect hook needs a name and a function")
	}
	if slices.ContainsFunc(collectHooks, func(e collectHookEntry) bool { return e.name == name }) {
		panic(fmt.Sprintf("diagnose: collect hook %q is already registered", name))
	}
	collectHooks = append(collectHooks, collectHookEntry{order, name, h})
	slices.SortStableFunc(collectHooks, func(a, b collectHookEntry) int { return cmp.Compare(a.order, b.order) })
}

// registerPostHook добавляет хук, выполняемый после всех правил, до слияния недостающего контекста.
// Условия те же, что у RegisterCollectHook.
func registerPostHook(order int, name string, h func(b *builder)) {
	if name == "" || h == nil {
		panic("diagnose: post hook needs a name and a function")
	}
	if slices.ContainsFunc(postHooks, func(e postHookEntry) bool { return e.name == name }) {
		panic(fmt.Sprintf("diagnose: post hook %q is already registered", name))
	}
	postHooks = append(postHooks, postHookEntry{order, name, h})
	slices.SortStableFunc(postHooks, func(a, b postHookEntry) int { return cmp.Compare(a.order, b.order) })
}

// registerRule добавляет правило в таблицу. epoch >= 1 и поднимается только при изменении смысла правила
// (условия срабатывания, уверенность, состав гипотез), а не при правке текстов и рефакторинге (ADR-0034 §3).
// Правила базового набора лежат в литерале rules с epoch = 1; правила расширений идут следом в порядке регистрации.
func registerRule(id string, epoch int, apply func(b *builder)) {
	if id == "" || apply == nil {
		panic("diagnose: rule needs an id and a function")
	}
	if epoch < 1 {
		panic(fmt.Sprintf("diagnose: rule %q must have epoch >= 1, got %d", id, epoch))
	}
	if slices.ContainsFunc(rules, func(r rule) bool { return r.id == id }) {
		panic(fmt.Sprintf("diagnose: rule %q is already registered", id))
	}
	rules = append(rules, rule{id: id, epoch: epoch, apply: apply})
}

// runCollectHooks выполняет хуки сбора. Отмена контекста между хуками — ErrUnavailable, как и между шагами
// базового сбора.
func runCollectHooks(ctx context.Context, r Reader, c *Context) error {
	for _, h := range collectHooks {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("%w: %w", ErrUnavailable, err)
		}
		runCollectHook(ctx, r, c, h)
	}
	return nil
}

func runCollectHook(ctx context.Context, r Reader, c *Context, h collectHookEntry) {
	defer func() {
		if p := recover(); p != nil {
			c.addMissing("rule.failed", c.Target, map[string]string{"rule": "collect_hook:" + h.name})
		}
	}()
	h.run(ctx, r, c)
}

// runPostHooks выполняет пост-хуки. Хук не относится ни к одному правилу, поэтому RuleID у записей, созданных
// builder.hypothesis/recommend напрямую из хука, пуст: правила расширений регистрируют через registerRule.
func runPostHooks(b *builder) {
	for _, h := range postHooks {
		b.ruleID, b.ruleEpoch = "", 0
		runPostHook(b, h)
	}
}

func runPostHook(b *builder, h postHookEntry) {
	defer func() {
		if p := recover(); p != nil {
			b.missing("rule.failed", b.c.Target, map[string]string{"rule": "post_hook:" + h.name})
		}
	}()
	h.run(b)
}
