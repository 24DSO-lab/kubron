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
// Инвариант: closeFn никогда не вызывается под mu. Под мьютексом подключение только помечается
// (poolEntry.closing) или снимается с учёта (removeMatching), а остановка informers выполняется
// после Unlock — медленный Close одного кластера не блокирует операции пула с остальными.
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
	// closing не nil, пока informers останавливаются вне p.mu; канал закрывается по завершении.
	// Пока он открыт, acquire ждёт (вне p.mu), чтобы остановка не снесла заново поднятые informers,
	// а повторный release и удаление не запускают второй closeFn.
	closing chan struct{}
}

// detached — подключение, снятое с учёта под p.mu и ожидающее закрытия уже вне блокировки.
type detached struct {
	conn *ClusterConnection
	// wait не nil, если остановка этого подключения уже идёт (idle-закрытие): её нужно дождаться,
	// а не повторять closeFn.
	wait <-chan struct{}
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

// acquire отмечает, что ещё один вид держит кластер активным. Если informers этого подключения
// сейчас останавливаются (release/таймер), ждёт конца остановки — вне p.mu, поэтому другие
// кластеры не блокируются, — и только потом берёт подключение.
func (p *connPool) acquire(id string, holder *Manager) error {
	for {
		p.mu.Lock()
		e, ok := p.entries[id]
		if !ok {
			p.mu.Unlock()
			return fmt.Errorf("cluster %s is not connected", id)
		}
		if done := e.closing; done != nil {
			p.mu.Unlock()
			<-done
			continue // за время ожидания подключение могли удалить: проверим заново
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
		p.mu.Unlock()
		return nil
	}
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
//
// Остановка (closeFn) может быть долгой, поэтому она идёт вне p.mu: под мьютексом подключение
// только помечается как закрываемое (e.closing), а сам вызов выполняется после Unlock. Подключение
// остаётся в пуле и переиспользуется; acquire дожидается конца остановки.
func (p *connPool) release(id string, holder *Manager) {
	p.mu.Lock()
	e, ok := p.entries[id]
	if !ok {
		p.mu.Unlock()
		return
	}
	delete(e.holders, holder)
	if e.refs > 0 {
		e.refs--
	}
	if e.refs != 0 || e.closing != nil { // кто-то ещё держит кластер либо остановка уже идёт
		p.mu.Unlock()
		return
	}
	if p.idleTTL > 0 {
		p.scheduleIdleCloseLocked(id, e)
		p.mu.Unlock()
		return
	}
	done := p.beginCloseLocked(e)
	p.mu.Unlock()
	p.finishClose(e, done)
}

// scheduleIdleCloseLocked ставит отложенную остановку informers (вызывать под p.mu, refs == 0).
func (p *connPool) scheduleIdleCloseLocked(id string, e *poolEntry) {
	if e.idleTimer != nil {
		e.idleTimer.Stop()
	}
	var t *time.Timer
	t = time.AfterFunc(p.idleTTL, func() {
		p.mu.Lock()
		// Сравнение по самому таймеру: опоздавший callback отменённого или перезапущенного таймера
		// (acquire → release за время ожидания блокировки) подключение не закрывает.
		if p.entries[id] != e || e.refs != 0 || e.idleTimer != t || e.closing != nil {
			p.mu.Unlock()
			return
		}
		e.idleTimer = nil
		done := p.beginCloseLocked(e)
		p.mu.Unlock()
		p.finishClose(e, done)
	})
	e.idleTimer = t
}

// beginCloseLocked помечает подключение закрываемым (вызывать под p.mu). Вызывающий обязан
// после Unlock вызвать finishClose с возвращённым каналом.
func (p *connPool) beginCloseLocked(e *poolEntry) chan struct{} {
	done := make(chan struct{})
	e.closing = done
	return done
}

// finishClose останавливает informers вне p.mu и будит ожидающих acquire. Метка снимается и
// канал закрывается даже при панике closeFn — иначе acquire повис бы навсегда.
func (p *connPool) finishClose(e *poolEntry, done chan struct{}) {
	defer func() {
		p.mu.Lock()
		if e.closing == done {
			e.closing = nil
		}
		p.mu.Unlock()
		close(done)
	}()
	p.closeFn(e.conn)
}

// removeCluster закрывает и забывает подключения кластера для всех субъектов (общее и личные).
func (p *connPool) removeCluster(clusterID string) {
	p.removeMatching(func(c *ClusterConnection) bool { return c.Name == clusterID })
}

// removeIf закрывает и забывает подключения, для которых match возвращает true.
func (p *connPool) removeIf(match func(*ClusterConnection) bool) {
	p.removeMatching(match)
}

// removeMatching снимает подходящие подключения с учёта под p.mu (отменяет таймеры, удаляет из
// реестра) и закрывает их уже вне блокировки: медленная остановка informers не задерживает остальные
// операции пула, а массовое отключение кластеров не копит задержки под мьютексом.
// К возврату все закрытия завершены. Подключение удалено из реестра до закрытия, поэтому его не
// закроет второй removeMatching, а acquire получит «not connected».
func (p *connPool) removeMatching(match func(*ClusterConnection) bool) {
	p.mu.Lock()
	var batch []detached
	for key, e := range p.entries {
		if !match(e.conn) {
			continue
		}
		if e.idleTimer != nil {
			e.idleTimer.Stop()
			e.idleTimer = nil
		}
		batch = append(batch, detached{conn: e.conn, wait: e.closing})
		delete(p.entries, key)
	}
	p.mu.Unlock()
	p.closeDetached(batch)
}

// closeDetached закрывает снятые с учёта подключения. Если остановка уже идёт (wait), её только
// дожидаются — closeFn дважды не вызывается. Паника одного closeFn не оставляет остальные
// открытыми: она перебрасывается после обработки всей пачки.
func (p *connPool) closeDetached(batch []detached) {
	var panicked any
	for _, d := range batch {
		func() {
			defer func() {
				if r := recover(); r != nil && panicked == nil {
					panicked = r
				}
			}()
			if d.wait != nil {
				<-d.wait
				return
			}
			p.closeFn(d.conn)
		}()
	}
	if panicked != nil {
		panic(panicked)
	}
}
