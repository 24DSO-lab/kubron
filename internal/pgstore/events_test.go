// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package pgstore

import (
	"context"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/24DSO-labs/kubron/internal/replicaevents"
)

type inbox struct {
	mu   sync.Mutex
	msgs []string
}

func (b *inbox) add(p string) { b.mu.Lock(); b.msgs = append(b.msgs, p); b.mu.Unlock() }
func (b *inbox) has(p string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, m := range b.msgs {
		if m == p {
			return true
		}
	}
	return false
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timeout: %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// Две «реплики» на одной БД: сообщение одной доходит до другой; после обрыва соединения слушателя он
// переподключается и вызывает сверку (resync).
func TestNotifierDeliversBetweenReplicasAndResyncsAfterReconnect(t *testing.T) {
	s := newTestStore(t, keyString("k1"))
	url := os.Getenv("KUBRON_TEST_DATABASE_URL")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var inA, inB inbox
	var resyncA, resyncB atomic.Int32
	a := s.NewNotifier(url, t.Logf)
	b := s.NewNotifier(url, t.Logf)
	go a.Run(ctx, inA.add, func() { resyncA.Add(1) })
	go b.Run(ctx, inB.add, func() { resyncB.Add(1) })
	waitFor(t, "both listeners", func() bool { return a.Listening() && b.Listening() && resyncA.Load() == 1 && resyncB.Load() == 1 })

	msg, err := replicaevents.Message{Kind: replicaevents.SessionEnded, Session: "s1"}.Encode("replica-a")
	if err != nil {
		t.Fatal(err)
	}
	a.Publish(msg)
	waitFor(t, "delivery to B", func() bool { return inB.has(msg) })
	// на уровне транспорта сообщение приходит и отправителю: свои отбрасывает replicaevents.Dispatcher по origin
	waitFor(t, "delivery to the sender", func() bool { return inA.has(msg) })

	// обрыв соединения слушателя B: переподключение и повторная сверка
	if _, err := s.pool.Exec(ctx, `
		SELECT pg_terminate_backend(pid) FROM pg_stat_activity
		WHERE query LIKE 'LISTEN%' AND pid <> pg_backend_pid() AND datname = current_database()`); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "resync after the reconnect", func() bool { return resyncB.Load() >= 2 && b.Listening() })
	msg2, _ := replicaevents.Message{Kind: replicaevents.AccessChanged}.Encode("replica-a")
	a.Publish(msg2)
	waitFor(t, "delivery after the reconnect", func() bool { return inB.has(msg2) })
}

func TestNotifierPublishNeverBlocks(t *testing.T) {
	s := newTestStore(t, keyString("k1"))
	n := s.NewNotifier(os.Getenv("KUBRON_TEST_DATABASE_URL"), t.Logf) // отправитель не запущен: очередь переполнится
	done := make(chan struct{})
	go func() {
		for i := 0; i < 10*notifyQueue; i++ {
			n.Publish("x")
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Publish must drop messages instead of blocking")
	}
}

func TestAccessNotifierFiresOnLocalChangesOnly(t *testing.T) {
	s := newTestStore(t, keyString("k1"))
	var fired atomic.Int32
	s.SetAccessNotifier(func() { fired.Add(1) })
	s.invalidateAccess()
	if fired.Load() != 1 {
		t.Fatalf("a local change must be announced: %d", fired.Load())
	}
	s.ApplyRemoteAccessChange() // чужое изменение не рассылается повторно
	if fired.Load() != 1 {
		t.Errorf("a remote change must not be re-published: %d", fired.Load())
	}
}
