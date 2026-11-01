// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package app

// RuntimeStats — обезличенные числа для метрик: без имён пользователей, кластеров и идентификаторов.
type RuntimeStats struct {
	LogStreams      int
	PortForwards    int
	ClusterStates   map[string]int // состояние проверки → число кластеров
	HealthLeader    bool           // эта реплика проверяет кластеры
	LoginBlocked    int64
	LockoutsStarted int64
}

// CollectStats собирает числа по корневому виду и видам веб-сессий (sessions.Views()). Это функция
// пакета, а не метод App: она не должна попадать в привязки Wails и RPC.
func CollectStats(root *App, views []any) RuntimeStats {
	st := RuntimeStats{ClusterStates: map[string]int{}}
	if root == nil {
		return st
	}
	for _, v := range views {
		a, ok := v.(*App)
		if !ok || a == nil {
			continue
		}
		st.LogStreams += a.k8sMgr.ActiveLogStreams()
		st.PortForwards += a.pfMgr.Len()
	}
	if root.k8sMgr != nil {
		if b := root.k8sMgr.Health(); b != nil {
			for state, n := range b.Counts() {
				st.ClusterStates[string(state)] = n
			}
		}
	}
	if root.health != nil {
		st.HealthLeader = root.health.IsLeader()
	}
	if root.auth != nil {
		a := root.auth.Stats()
		st.LoginBlocked, st.LockoutsStarted = a.LoginBlocked, a.LockoutsStarted
	}
	return st
}
