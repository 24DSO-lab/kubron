// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package pgstore

import "time"

// PoolStats — снимок пула соединений с БД для метрик. Только числа.
type PoolStats struct {
	AcquiredConns, IdleConns, TotalConns, MaxConns        int32
	AcquireCount, EmptyAcquireCount, CanceledAcquireCount int64
	AcquireDuration                                       time.Duration // суммарное ожидание соединения
}

// PoolStats возвращает текущую статистику пула.
func (s *Store) PoolStats() PoolStats {
	st := s.pool.Stat()
	return PoolStats{
		AcquiredConns: st.AcquiredConns(), IdleConns: st.IdleConns(), TotalConns: st.TotalConns(), MaxConns: st.MaxConns(),
		AcquireCount: st.AcquireCount(), EmptyAcquireCount: st.EmptyAcquireCount(),
		CanceledAcquireCount: st.CanceledAcquireCount(), AcquireDuration: st.AcquireDuration(),
	}
}
