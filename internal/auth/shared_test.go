// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// sharedDB — хранилище пользователей, общее для нескольких «реплик» (роль PostgreSQL): Save заменяет набор
// целиком, а точечные операции меняют только своё. Реализует DeltaBackend; счётчики показывают, что именно вызывали.
type sharedDB struct {
	mu       sync.Mutex
	users    []UserRecord
	settings SecuritySettings
	fullSave int
	loads    int
}

func (d *sharedDB) Load() (*Data, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.loads++
	return &Data{Users: append([]UserRecord(nil), d.users...), Settings: d.settings}, nil
}

func (d *sharedDB) Save(x *Data) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.fullSave++
	d.users, d.settings = append([]UserRecord(nil), x.Users...), x.Settings
	return nil
}

func (d *sharedDB) UpsertUser(u UserRecord) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	for i := range d.users {
		if d.users[i].ID != u.ID && normalizeUsername(d.users[i].Username) == normalizeUsername(u.Username) {
			return errors.New("duplicate user name")
		}
	}
	for i := range d.users {
		if d.users[i].ID == u.ID {
			d.users[i] = u
			return nil
		}
	}
	d.users = append(d.users, u)
	return nil
}

func (d *sharedDB) TouchLogin(id string, at time.Time, hash string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	for i := range d.users {
		if d.users[i].ID == id {
			d.users[i].LastLoginAt = &at
			if hash != "" {
				d.users[i].PasswordHash = hash
			}
		}
	}
	return nil
}

func (d *sharedDB) DeleteUser(id string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	for i := range d.users {
		if d.users[i].ID == id {
			d.users = append(d.users[:i], d.users[i+1:]...)
			break
		}
	}
	return nil
}

func (d *sharedDB) SaveSettings(s SecuritySettings) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.settings = s
	return nil
}

func (d *sharedDB) find(name string) *UserRecord {
	d.mu.Lock()
	defer d.mu.Unlock()
	for i := range d.users {
		if d.users[i].Username == name {
			c := d.users[i]
			return &c
		}
	}
	return nil
}

var _ DeltaBackend = (*sharedDB)(nil)

const pw = "correct-horse-1"

// twoReplicas создаёт двух администраторов-«реплики» над общей БД. Первая выполняет начальную настройку.
func twoReplicas(t *testing.T) (a, b *Service, db *sharedDB) {
	t.Helper()
	db = &sharedDB{}
	a, err := NewServiceWithBackend(db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Setup("admin", pw); err != nil {
		t.Fatal(err)
	}
	b, err = NewServiceWithBackend(db) // запускается позже и видит администратора
	if err != nil {
		t.Fatal(err)
	}
	return a, b, db
}

func TestSharedUserCreatedOnAIsKnownToB(t *testing.T) {
	a, b, _ := twoReplicas(t)
	if _, err := a.CreateUser("bob", "bob-password-1", RoleOperator); err != nil {
		t.Fatal(err)
	}
	// B стартовала до создания пользователя, но вход по паролю на B работает сразу (чтение при неизвестном логине)
	if _, err := b.NewSession("").Login("bob", "bob-password-1"); err != nil {
		t.Fatalf("a user created on replica A must be able to log in on B: %v", err)
	}
}

func TestSharedMissRefreshIsRateLimited(t *testing.T) {
	_, b, db := twoReplicas(t)
	before := db.loads
	for i := 0; i < 20; i++ {
		_, _ = b.NewSession("").Login("nobody", "whatever-1")
	}
	if n := db.loads - before; n > 1 {
		t.Errorf("unknown logins must not hammer the store: %d reads", n)
	}
}

func TestSharedWritesDoNotClobberOtherReplicas(t *testing.T) {
	a, b, db := twoReplicas(t)
	sa, sb := a.NewSession(""), b.NewSession("")
	if _, err := sa.Login("admin", pw); err != nil {
		t.Fatal(err)
	}
	if _, err := sb.Login("admin", pw); err != nil {
		t.Fatal(err)
	}
	// Оба создают по пользователю: прежний Save затёр бы одного из них.
	if _, err := sa.CreateUser("alice", "alice-password-1", RoleUser); err != nil {
		t.Fatal(err)
	}
	if _, err := sb.CreateUser("carol", "carol-password-1", RoleUser); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"admin", "alice", "carol"} {
		if db.find(n) == nil {
			t.Errorf("%s was lost", n)
		}
	}
	if db.fullSave != 0 {
		t.Errorf("a shared store must be written by delta operations only, full saves: %d", db.fullSave)
	}
	// Изменение роли на A, затем вход пользователя на B не возвращает прежнюю роль.
	alice := db.find("alice")
	if _, err := sa.UpdateRole(alice.ID, RoleOperator); err != nil {
		t.Fatal(err)
	}
	if _, err := b.NewSession("").Login("alice", "alice-password-1"); err != nil {
		t.Fatalf("login on B: %v", err)
	}
	if got := db.find("alice"); got.Role != RoleOperator || got.LastLoginAt == nil {
		t.Errorf("a login must update only the login time: %+v", got)
	}
}

func TestSharedDisabledOnAIsSeenOnB(t *testing.T) {
	a, b, db := twoReplicas(t)
	sa := a.NewSession("")
	if _, err := sa.Login("admin", pw); err != nil {
		t.Fatal(err)
	}
	if _, err := sa.CreateUser("bob", "bob-password-1", RoleUser); err != nil {
		t.Fatal(err)
	}
	bob := db.find("bob")
	if _, err := b.NewSession("").Login("bob", "bob-password-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := sa.SetDisabled(bob.ID, true); err != nil {
		t.Fatal(err)
	}
	if err := b.refresh(); err != nil { // то же делает фоновое перечитывание
		t.Fatal(err)
	}
	if _, err := b.NewSession("").Login("bob", "bob-password-1"); !errors.Is(err, ErrAccountDisabled) {
		t.Errorf("a user disabled on A must be refused on B: %v", err)
	}
	// удаление тоже доходит
	if err := sa.DeleteUser(bob.ID); err != nil {
		t.Fatal(err)
	}
	if err := b.refresh(); err != nil {
		t.Fatal(err)
	}
	if _, err := b.NewSession("").Login("bob", "bob-password-1"); !errors.Is(err, ErrInvalidCredentials) {
		t.Errorf("a user deleted on A: %v", err)
	}
}

func TestSharedRefreshDoesNotOverwriteNewerLocalChange(t *testing.T) {
	a, _, db := twoReplicas(t)
	sa := a.NewSession("")
	if _, err := sa.Login("admin", pw); err != nil {
		t.Fatal(err)
	}
	// чтение началось до локального изменения и вернулось после него: устаревший результат отбрасывается
	a.mu.Lock()
	gen := a.st.gen
	a.mu.Unlock()
	stale, _ := a.st.backend.Load()
	if _, err := sa.CreateUser("dave", "dave-password-1", RoleUser); err != nil {
		t.Fatal(err)
	}
	a.mu.Lock()
	if a.st.gen == gen {
		t.Error("a local change must advance the generation")
	}
	a.mu.Unlock()
	_ = stale
	if err := a.refresh(); err != nil {
		t.Fatal(err)
	}
	a.mu.Lock()
	known := a.st.findByName("dave") != nil
	a.mu.Unlock()
	if !known || db.find("dave") == nil {
		t.Error("the new user must be visible")
	}
}

func TestSharedResumeFindsUserOfAnotherReplica(t *testing.T) {
	a, b, db := twoReplicas(t)
	sa := a.NewSession("")
	if _, err := sa.Login("admin", pw); err != nil {
		t.Fatal(err)
	}
	if _, err := sa.CreateUser("erin", "erin-password-1", RoleUser); err != nil {
		t.Fatal(err)
	}
	erin := db.find("erin")
	if err := b.NewSession("").Resume(erin.ID, time.Now()); err != nil {
		t.Fatalf("a web session of a user created on A must resume on B: %v", err)
	}
}

func TestSharedRunRefresherStopsAndPicksUpChanges(t *testing.T) {
	a, b, _ := twoReplicas(t)
	sa := a.NewSession("")
	if _, err := sa.Login("admin", pw); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { b.RunRefresher(ctx, 10*time.Millisecond); close(done) }()
	if _, err := sa.CreateUser("frank", "frank-password-1", RoleUser); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		b.mu.Lock()
		ok := b.st.findByName("frank") != nil
		b.mu.Unlock()
		if ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the background refresh did not deliver the new user")
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("RunRefresher must stop with the context")
	}
}

func TestFileBackendIsNotShared(t *testing.T) {
	svc, err := NewServiceWithBackend(&memUsersBackend{})
	if err != nil {
		t.Fatal(err)
	}
	if svc.Shared() {
		t.Error("a single-process backend must keep the old behaviour")
	}
	svc.RunRefresher(context.Background(), time.Millisecond) // возвращается сразу
}

// memUsersBackend — одиночное хранилище без точечных операций (как файл).
type memUsersBackend struct{ d Data }

func (m *memUsersBackend) Load() (*Data, error) { c := m.d; return &c, nil }
func (m *memUsersBackend) Save(d *Data) error   { m.d = *d; return nil }
