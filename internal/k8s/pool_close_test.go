// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package k8s

import (
	"fmt"
	"math/rand/v2"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Тесты инварианта пула: closeFn (остановка informers) не вызывается под p.mu, поэтому медленный
// Close одного кластера не блокирует операции пула с остальными. Запускать с -race.

// closeRecorder — closeFn с управляемой длительностью: подключение slow останавливается, пока не
// вызван unblock; остальные — мгновенно. Считает вызовы по имени подключения.
type closeRecorder struct {
	slow     string
	started  chan struct{} // получает значение, когда началась остановка slow
	gate     chan struct{}
	once     sync.Once
	mu       sync.Mutex
	calls    map[string]int
	finished atomic.Int32 // сколько остановок slow дошло до конца
}

func newCloseRecorder(t *testing.T, slow string) *closeRecorder {
	t.Helper()
	r := &closeRecorder{
		slow:    slow,
		started: make(chan struct{}, 16),
		gate:    make(chan struct{}),
		calls:   map[string]int{},
	}
	t.Cleanup(r.unblock) // при падении теста не оставляем зависшие горутины
	return r
}

func (r *closeRecorder) fn(c *ClusterConnection) {
	r.mu.Lock()
	r.calls[c.Name]++
	r.mu.Unlock()
	if c.Name != r.slow {
		return
	}
	r.started <- struct{}{}
	<-r.gate
	r.finished.Add(1)
}

func (r *closeRecorder) unblock() { r.once.Do(func() { close(r.gate) }) }

func (r *closeRecorder) count(name string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls[name]
}

func (r *closeRecorder) waitStarted(t *testing.T) {
	t.Helper()
	select {
	case <-r.started:
	case <-time.After(2 * time.Second):
		t.Fatal("closeFn was not called")
	}
}

func newTestPool(rec *closeRecorder, ids ...string) *connPool {
	p := newConnPool()
	p.closeFn = rec.fn
	for _, id := range ids {
		p.put(id, &ClusterConnection{Name: id})
	}
	return p
}

func mustAcquire(t *testing.T, p *connPool, id string) {
	t.Helper()
	if err := p.acquire(id, nil); err != nil {
		t.Fatalf("acquire %s: %v", id, err)
	}
}

// goDone запускает f в горутине и возвращает канал, закрываемый по её завершении.
func goDone(f func()) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		f()
	}()
	return done
}

func waitDone(t *testing.T, done <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatalf("%s did not finish in time", what)
	}
}

func assertPending(t *testing.T, done <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-done:
		t.Fatalf("%s finished before the slow closeFn completed", what)
	case <-time.After(100 * time.Millisecond):
	}
}

// assertPoolResponsive проверяет, что пока идёт медленная остановка, операции пула с другим
// кластером выполняются сразу — то есть p.mu не удерживается.
func assertPoolResponsive(t *testing.T, p *connPool, other string) {
	t.Helper()
	done := goDone(func() {
		if _, ok := p.get(other); !ok {
			t.Errorf("get(%s) found nothing", other)
		}
		if !p.has(other) {
			t.Errorf("has(%s) is false", other)
		}
		if err := p.acquire(other, nil); err != nil {
			t.Errorf("acquire(%s): %v", other, err)
		}
		_ = p.holdersOf(other)
		fresh := "fresh-" + other
		if _, err := p.getOrCreate(fresh, func() (*ClusterConnection, error) {
			return &ClusterConnection{Name: fresh}, nil
		}); err != nil {
			t.Errorf("getOrCreate(%s): %v", fresh, err)
		}
	})
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("pool operations are blocked while closeFn is running: p.mu is held during the stop")
	}
}

func TestSlowCloseOnReleaseDoesNotBlockPool(t *testing.T) {
	rec := newCloseRecorder(t, "a")
	p := newTestPool(rec, "a", "b")
	mustAcquire(t, p, "a")

	released := goDone(func() { p.release("a", nil) })
	rec.waitStarted(t)

	assertPoolResponsive(t, p, "b")
	assertPending(t, released, "release") // release остаётся синхронным для вызывающего

	rec.unblock()
	waitDone(t, released, "release")

	if n := rec.count("a"); n != 1 {
		t.Errorf("a closed %d times, expected 1", n)
	}
	if !p.has("a") {
		t.Error("connection a must stay in the pool for reuse")
	}
}

func TestSlowCloseFromIdleTimerDoesNotBlockPool(t *testing.T) {
	rec := newCloseRecorder(t, "a")
	p := newTestPool(rec, "a", "b")
	p.idleTTL = 5 * time.Millisecond
	mustAcquire(t, p, "a")

	p.release("a", nil) // ставит таймер и возвращается сразу
	rec.waitStarted(t)  // таймер сработал, остановка идёт

	assertPoolResponsive(t, p, "b")

	rec.unblock()
	// acquire дожидается конца остановки, поэтому после него остановка гарантированно завершена.
	waitDone(t, goDone(func() { mustAcquire(t, p, "a") }), "acquire after the idle stop")
	if got := rec.finished.Load(); got != 1 {
		t.Errorf("finished stops: %d, expected 1", got)
	}
	if n := rec.count("a"); n != 1 {
		t.Errorf("a closed %d times, expected 1", n)
	}
}

func TestSlowCloseOnRemoveDoesNotBlockPool(t *testing.T) {
	removers := map[string]func(*connPool){
		"removeCluster": func(p *connPool) { p.removeCluster("a") },
		"removeIf": func(p *connPool) {
			p.removeIf(func(c *ClusterConnection) bool { return c.Name == "a" })
		},
	}
	for name, remove := range removers {
		t.Run(name, func(t *testing.T) {
			rec := newCloseRecorder(t, "a")
			p := newTestPool(rec, "a", "b")

			removed := goDone(func() { remove(p) })
			rec.waitStarted(t)

			assertPoolResponsive(t, p, "b")
			// Из реестра подключение исчезает до закрытия: новые операции его уже не видят.
			if p.has("a") {
				t.Error("a must be out of the registry while it is being closed")
			}
			if err := p.acquire("a", nil); err == nil {
				t.Error("acquire of a removed connection must fail")
			}
			assertPending(t, removed, name) // к возврату закрытие завершено

			rec.unblock()
			waitDone(t, removed, name)
			if n := rec.count("a"); n != 1 {
				t.Errorf("a closed %d times, expected 1", n)
			}
			if n := rec.count("b"); n != 0 {
				t.Errorf("b was closed %d times, it does not match", n)
			}
		})
	}
}

// Повторная активация не должна стартовать informers параллельно с их остановкой: иначе запоздавший
// Close снёс бы только что поднятые informers.
func TestAcquireWaitsForInFlightClose(t *testing.T) {
	rec := newCloseRecorder(t, "a")
	p := newTestPool(rec, "a")
	mustAcquire(t, p, "a")

	released := goDone(func() { p.release("a", nil) })
	rec.waitStarted(t)

	var stopsFinishedAtAcquire atomic.Int32
	acquired := make(chan error, 1)
	go func() {
		err := p.acquire("a", nil)
		stopsFinishedAtAcquire.Store(rec.finished.Load())
		acquired <- err
	}()
	select {
	case <-acquired:
		t.Fatal("acquire returned while the stop was still running")
	case <-time.After(100 * time.Millisecond):
	}

	rec.unblock()
	waitDone(t, released, "release")
	select {
	case err := <-acquired:
		if err != nil {
			t.Fatalf("acquire: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("acquire did not wake up after the stop finished")
	}
	if stopsFinishedAtAcquire.Load() != 1 {
		t.Error("acquire returned before the stop finished")
	}
	if n := rec.count("a"); n != 1 {
		t.Errorf("a closed %d times, expected 1: the re-activation was closed again", n)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if e := p.entries["a"]; e == nil || e.refs != 1 || e.closing != nil {
		t.Errorf("unexpected entry state after the re-activation: %+v", e)
	}
}

// Удаление во время идущей остановки её дожидается, но не запускает closeFn второй раз.
func TestRemoveDuringInFlightCloseClosesOnce(t *testing.T) {
	rec := newCloseRecorder(t, "a")
	p := newTestPool(rec, "a")
	mustAcquire(t, p, "a")

	released := goDone(func() { p.release("a", nil) })
	rec.waitStarted(t)

	removed := goDone(func() { p.removeCluster("a") })
	assertPending(t, removed, "removeCluster") // удаление ждёт уже идущую остановку

	rec.unblock()
	waitDone(t, released, "release")
	waitDone(t, removed, "removeCluster")

	if n := rec.count("a"); n != 1 {
		t.Errorf("a closed %d times, expected 1", n)
	}
	if p.has("a") {
		t.Error("a must be removed")
	}
}

// acquire, ждавший остановки подключения, которое за это время удалили, получает ошибку, а не виснет.
func TestAcquireWaitingOnClosingConnectionFailsAfterRemove(t *testing.T) {
	rec := newCloseRecorder(t, "a")
	p := newTestPool(rec, "a")
	mustAcquire(t, p, "a")

	released := goDone(func() { p.release("a", nil) })
	rec.waitStarted(t)

	acquired := make(chan error, 1)
	go func() { acquired <- p.acquire("a", nil) }()
	removed := goDone(func() { p.removeCluster("a") })
	time.Sleep(50 * time.Millisecond)

	rec.unblock()
	waitDone(t, released, "release")
	waitDone(t, removed, "removeCluster")
	select {
	case err := <-acquired:
		if err == nil {
			t.Fatal("acquire of a removed connection must fail")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("acquire hung on a removed connection")
	}
	if n := rec.count("a"); n != 1 {
		t.Errorf("a closed %d times, expected 1", n)
	}
}

// Паника closeFn не должна оставлять ожидающих acquire навсегда.
func TestClosePanicDoesNotHangAcquire(t *testing.T) {
	p := newConnPool()
	p.closeFn = func(*ClusterConnection) { panic("close boom") }
	p.put("a", &ClusterConnection{Name: "a"})
	mustAcquire(t, p, "a")

	func() {
		defer func() {
			if recover() == nil {
				t.Error("the closeFn panic must propagate to the caller as before")
			}
		}()
		p.release("a", nil)
	}()

	waitDone(t, goDone(func() {
		if err := p.acquire("a", nil); err != nil {
			t.Errorf("acquire after a panicking close: %v", err)
		}
	}), "acquire after a panicking close")
}

// Паника одного closeFn не оставляет остальные подключения пачки незакрытыми.
func TestRemovePanicStillClosesOthers(t *testing.T) {
	var closedB atomic.Int32
	p := newConnPool()
	p.closeFn = func(c *ClusterConnection) {
		if c.Name == "a" {
			panic("close boom")
		}
		closedB.Add(1)
	}
	p.put("a", &ClusterConnection{Name: "a"})
	p.put("b", &ClusterConnection{Name: "b"})

	func() {
		defer func() {
			if recover() == nil {
				t.Error("the panic must be re-raised after the whole batch is processed")
			}
		}()
		p.removeIf(func(*ClusterConnection) bool { return true })
	}()

	if closedB.Load() != 1 {
		t.Errorf("b closed %d times, expected 1 despite the panic on a", closedB.Load())
	}
	if p.has("a") || p.has("b") {
		t.Error("both connections must be removed from the registry")
	}
}

// Стресс (-race): параллельные acquire/release и удаление/создание подключений.
// Инварианты: closeFn одного подключения никогда не идёт одновременно в двух горутинах;
// без удалений подключение, которое держит вид, не останавливается; ничто не виснет.
func TestConcurrentOperationsWithSlowClose(t *testing.T) {
	const (
		clusters = 4
		workers  = 16
		rounds   = 150
	)
	for _, tc := range []struct {
		name    string
		idleTTL time.Duration
		removes bool
	}{
		{"release only, immediate stop", 0, false},
		{"release only, idle TTL", time.Millisecond, false},
		{"with removals, immediate stop", 0, true},
		{"with removals, idle TTL", time.Millisecond, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var inFlight sync.Map // *ClusterConnection -> *atomic.Int32: идущие остановки
			counter := func(c *ClusterConnection) *atomic.Int32 {
				v, _ := inFlight.LoadOrStore(c, new(atomic.Int32))
				return v.(*atomic.Int32)
			}
			var overlaps, closedWhileHeld atomic.Int32

			p := newConnPool()
			p.idleTTL = tc.idleTTL
			p.closeFn = func(c *ClusterConnection) {
				n := counter(c)
				if n.Add(1) != 1 {
					overlaps.Add(1) // вторая остановка того же подключения поверх идущей
				}
				time.Sleep(200 * time.Microsecond)
				n.Add(-1)
			}
			ids := make([]string, clusters)
			for i := range ids {
				ids[i] = fmt.Sprintf("k%d", i)
				p.put(ids[i], &ClusterConnection{Name: ids[i]})
			}

			var wg sync.WaitGroup
			for w := 0; w < workers; w++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					for i := 0; i < rounds; i++ {
						id := ids[rand.IntN(clusters)]
						if tc.removes {
							switch rand.IntN(10) {
							case 0:
								p.removeCluster(id)
								p.put(id, &ClusterConnection{Name: id})
								continue
							case 1:
								p.removeIf(func(c *ClusterConnection) bool { return c.Name == id })
								p.put(id, &ClusterConnection{Name: id})
								continue
							}
						}
						if err := p.acquire(id, nil); err != nil {
							p.put(id, &ClusterConnection{Name: id}) // успели удалить: создаём заново
							continue
						}
						if !tc.removes {
							c, _ := p.get(id)
							if counter(c).Load() != 0 {
								closedWhileHeld.Add(1) // подключение держим, а его останавливают
							}
							time.Sleep(50 * time.Microsecond)
							if counter(c).Load() != 0 {
								closedWhileHeld.Add(1)
							}
						}
						p.release(id, nil)
					}
				}()
			}

			finished := make(chan struct{})
			go func() { wg.Wait(); close(finished) }()
			select {
			case <-finished:
			case <-time.After(30 * time.Second):
				t.Fatal("deadlock: operations did not finish")
			}

			if n := overlaps.Load(); n != 0 {
				t.Errorf("the same connection was stopped concurrently %d times", n)
			}
			if n := closedWhileHeld.Load(); n != 0 {
				t.Errorf("a held connection was stopped %d times", n)
			}
			p.mu.Lock()
			defer p.mu.Unlock()
			for id, e := range p.entries {
				if e.refs != 0 {
					t.Errorf("%s: refs=%d after every worker finished", id, e.refs)
				}
			}
		})
	}
}
