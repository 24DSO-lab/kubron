// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package k8s

import (
	"fmt"
	"sync"
	"time"
)

// connPool — общий для всех сессий пул подключений к кластерам.
//
// Подключение (клиенты client-go, informers) создаётся один раз и используется всеми
// «видами» Manager (см. Manager.ForSession). refs — сколько видов сейчас держат кластер
// активным; когда счётчик падает до нуля, фоновые informers останавливаются (сам клиент
// остаётся в пуле и переиспользуется при следующей активации).
//
// СПАЙК: создание подключения идёт под блокировкой пула; для вызовов, которые ходят в сеть
// (нативный oidc-password), нужен singleflight по ключу — см. ADR-0005.
type connPool struct {
	mu      sync.Mutex
	entries map[string]*poolEntry
	// closeFn вызывается, когда у кластера не осталось активных видов (по умолчанию — Close).
	closeFn func(*ClusterConnection)
	// idleTTL — через сколько после ухода последнего вида останавливаются informers (0 — сразу).
	// Короткая задержка гасит «дребезг»: пользователь переключился на соседний кластер и вернулся.
	idleTTL time.Duration
	// flights — подключения, которые строятся прямо сейчас (singleflight по id кластера).
	flights map[string]*flight
}

// flight — одно построение подключения; ожидающие получают тот же результат.
type flight struct {
	done chan struct{}
	conn *ClusterConnection
	err  error
}

type poolEntry struct {
	conn      *ClusterConnection
	refs      int
	idleTimer *time.Timer // отложенная остановка informers при refs == 0
	// holders — виды, державшие кластер активным: им адресуются события кластера.
	holders map[*Manager]struct{}
}

func newConnPool() *connPool {
	return &connPool{
		entries: make(map[string]*poolEntry),
		closeFn: func(c *ClusterConnection) { c.Close() },
		flights: make(map[string]*flight),
	}
}

func (p *connPool) get(id string) (*ClusterConnection, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	e, ok := p.entries[id]
	if !ok {
		return nil, false
	}
	return e.conn, true
}

// put кладёт готовое подключение (если уже есть — возвращает существующее).
func (p *connPool) put(id string, conn *ClusterConnection) *ClusterConnection {
	p.mu.Lock()
	defer p.mu.Unlock()
	if e, ok := p.entries[id]; ok {
		return e.conn
	}
	p.entries[id] = &poolEntry{conn: conn}
	return conn
}

// getOrCreate возвращает существующее подключение или создаёт его через build.
// Построение идёт вне блокировки пула (оно может ходить в сеть: oidc-password, дискавери), а
// параллельные вызовы для одного кластера ждут один и тот же результат (singleflight): apiserver
// и провайдер входа не получают лавину одинаковых запросов, а медленный кластер не блокирует остальные.
func (p *connPool) getOrCreate(id string, build func() (*ClusterConnection, error)) (*ClusterConnection, error) {
	p.mu.Lock()
	if e, ok := p.entries[id]; ok {
		p.mu.Unlock()
		return e.conn, nil
	}
	if f, ok := p.flights[id]; ok {
		p.mu.Unlock()
		<-f.done
		return f.conn, f.err
	}
	f := &flight{done: make(chan struct{})}
	p.flights[id] = f
	p.mu.Unlock()

	f.conn, f.err = safeBuild(build)

	p.mu.Lock()
	delete(p.flights, id)
	if f.err == nil {
		if e, ok := p.entries[id]; ok { // подключение успели положить через put
			f.conn = e.conn
		} else {
			p.entries[id] = &poolEntry{conn: f.conn}
		}
	}
	p.mu.Unlock()
	close(f.done)
	return f.conn, f.err
}

// safeBuild превращает панику построения в ошибку: иначе ожидающие вызовы повисли бы навсегда.
func safeBuild(build func() (*ClusterConnection, error)) (conn *ClusterConnection, err error) {
	defer func() {
		if r := recover(); r != nil {
			conn, err = nil, fmt.Errorf("cluster connection: %v", r)
		}
	}()
	return build()
}

func (p *connPool) has(id string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	_, ok := p.entries[id]
	return ok
}

// acquire отмечает, что ещё один вид держит кластер активным.
func (p *connPool) acquire(id string, holder *Manager) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	e, ok := p.entries[id]
	if !ok {
		return fmt.Errorf("cluster %s is not connected", id)
	}
	e.refs++
	if e.idleTimer != nil { // кластер снова востребован: отложенная остановка отменяется
		e.idleTimer.Stop()
		e.idleTimer = nil
	}
	if holder != nil {
		if e.holders == nil {
			e.holders = map[*Manager]struct{}{}
		}
		e.holders[holder] = struct{}{}
	}
	return nil
}

// holdersOf — виды, у которых кластер сейчас активен.
func (p *connPool) holdersOf(id string) []*Manager {
	p.mu.Lock()
	defer p.mu.Unlock()
	e, ok := p.entries[id]
	if !ok {
		return nil
	}
	out := make([]*Manager, 0, len(e.holders))
	for h := range e.holders {
		out = append(out, h)
	}
	return out
}

// release снимает отметку; при нуле останавливает informers кластера.
func (p *connPool) release(id string, holder *Manager) {
	p.mu.Lock()
	defer p.mu.Unlock()
	e, ok := p.entries[id]
	if !ok {
		return
	}
	delete(e.holders, holder)
	if e.refs > 0 {
		e.refs--
	}
	if e.refs == 0 {
		if p.idleTTL <= 0 {
			p.closeFn(e.conn)
			return
		}
		if e.idleTimer != nil {
			e.idleTimer.Stop()
		}
		e.idleTimer = time.AfterFunc(p.idleTTL, func() {
			p.mu.Lock()
			defer p.mu.Unlock()
			if cur, ok := p.entries[id]; ok && cur == e && e.refs == 0 && e.idleTimer != nil {
				e.idleTimer = nil
				p.closeFn(e.conn)
			}
		})
	}
}

// remove закрывает и забывает подключение. Виды, державшие его активным, при следующем
// GetActiveConnection получат ошибку «нет активного подключения».
func (p *connPool) remove(id string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if e, ok := p.entries[id]; ok {
		if e.idleTimer != nil {
			e.idleTimer.Stop()
			e.idleTimer = nil
		}
		p.closeFn(e.conn)
		delete(p.entries, id)
	}
}

// removeCluster закрывает и забывает подключения кластера для всех субъектов (общее и личные).
func (p *connPool) removeCluster(clusterID string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for key, e := range p.entries {
		if e.conn.Name != clusterID {
			continue
		}
		if e.idleTimer != nil {
			e.idleTimer.Stop()
			e.idleTimer = nil
		}
		p.closeFn(e.conn)
		delete(p.entries, key)
	}
}

// removeIf закрывает и забывает подключения, для которых match возвращает true.
func (p *connPool) removeIf(match func(*ClusterConnection) bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for key, e := range p.entries {
		if !match(e.conn) {
			continue
		}
		if e.idleTimer != nil {
			e.idleTimer.Stop()
			e.idleTimer = nil
		}
		p.closeFn(e.conn)
		delete(p.entries, key)
	}
}
