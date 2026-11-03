// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// sharedMem — общее хранилище двух «реплик»: DeltaBackend с атомарной вставкой первого пользователя.
type sharedMem struct {
	mu    sync.Mutex
	users []UserRecord
}

func (m *sharedMem) Load() (*Data, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return &Data{Users: append([]UserRecord(nil), m.users...)}, nil
}
func (m *sharedMem) Save(d *Data) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.users = append([]UserRecord(nil), d.Users...)
	return nil
}
func (m *sharedMem) UpsertUser(u UserRecord) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.users = append(m.users, u)
	return nil
}
func (m *sharedMem) InsertFirstUser(u UserRecord) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.users) > 0 {
		return false, nil
	}
	m.users = append(m.users, u)
	return true, nil
}
func (m *sharedMem) TouchLogin(string, time.Time, string) error { return nil }
func (m *sharedMem) DeleteUser(string) error                    { return nil }
func (m *sharedMem) SaveSettings(SecuritySettings) error        { return nil }

var _ FirstUserBackend = (*sharedMem)(nil)

// Две реплики с пустой БД одновременно настраивают первого администратора: создаётся ровно один.
func TestSetupRaceAcrossReplicasCreatesOneAdmin(t *testing.T) {
	mem := &sharedMem{}
	var svcs []*Service
	for i := 0; i < 2; i++ {
		s, err := NewServiceWithBackend(mem)
		if err != nil {
			t.Fatal(err)
		}
		svcs = append(svcs, s)
	}
	const n = 16
	var ok, done atomic.Int32
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			_, err := svcs[i%2].NewSession("").Setup(fmt.Sprintf("admin%d", i), goodPass)
			switch {
			case err == nil:
				ok.Add(1)
			case errors.Is(err, ErrSetupDone):
				done.Add(1)
			default:
				t.Errorf("unexpected error: %v", err)
			}
		}(i)
	}
	close(start)
	wg.Wait()
	if ok.Load() != 1 || done.Load() != n-1 {
		t.Fatalf("created=%d already-set-up=%d, want 1 and %d", ok.Load(), done.Load(), n-1)
	}
	if len(mem.users) != 1 || mem.users[0].Role != RoleAdmin {
		t.Fatalf("storage must hold exactly one administrator: %+v", mem.users)
	}
}

// Внутри одного процесса виды сессий делят общее состояние: параллельные настройки тоже дают одного администратора.
func TestSetupRaceInOneProcessCreatesOneAdmin(t *testing.T) {
	root, _ := newTestService(t)
	const n = 16
	var ok atomic.Int32
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			if _, err := root.NewSession("").Setup(fmt.Sprintf("admin%d", i), goodPass); err == nil {
				ok.Add(1)
			} else if !errors.Is(err, ErrSetupDone) {
				t.Errorf("unexpected error: %v", err)
			}
		}(i)
	}
	close(start)
	wg.Wait()
	if ok.Load() != 1 {
		t.Fatalf("created %d administrators, want 1", ok.Load())
	}
	if st := root.Status(); st.NeedsSetup {
		t.Fatal("setup must be complete")
	}
}

func TestSetupRefusedWhenUsersExist(t *testing.T) {
	s, _ := newTestService(t)
	if _, err := s.Setup("admin", goodPass); err != nil {
		t.Fatal(err)
	}
	if _, err := s.NewSession("").Setup("second", goodPass); !errors.Is(err, ErrSetupDone) {
		t.Fatalf("second setup: %v, want ErrSetupDone", err)
	}
}
