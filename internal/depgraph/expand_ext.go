// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package depgraph

import "context"

// expandExtensions — расширения этапа 2 (ADR-0031 §6). Читаются только по флагам Options, выключенным по умолчанию.
// Сейчас это каркас: при включённом флаге граф честно сообщает, что расширение не реализовано, и ничего не читает.
func (b *builder) expandExtensions(_ context.Context) {
	if b.o.ExpandIngress {
		b.g.AddWarning("not_implemented:ingress")
	}
	if b.o.ExpandHPA {
		b.g.AddWarning("not_implemented:hpa")
	}
}
