// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package server

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"log/slog"
	"net/http"
	"net/netip"
	"sync"
	"time"

	"github.com/24DSO-labs/kubron/internal/replicaevents"
	"github.com/24DSO-labs/kubron/internal/termbuf"
	"github.com/24DSO-labs/kubron/internal/websession"
)

// Session — веб-сессия браузера: привязывает cookie к пользователю и CSRF-токену.
type Session struct {
	// ID — публичный идентификатор сессии (не секрет, не токен): для привязки WebSocket-соединений.
	ID     string
	UserID string
	CSRF   string
	// View — вид ядра этой сессии (nil — общий экземпляр диспетчера). Освобождается при завершении сессии.
	View    any
	Expires time.Time
	// ActiveCluster — активный кластер, как он записан в хранилище сессий (только с общим хранилищем).
	ActiveCluster string
	// LastActive — последнее действие пользователя в интерфейсе по данным хранилища (только с общим хранилищем).
	LastActive time.Time

	hash        [sha256.Size]byte // текущий хэш токена (ключ в items; прежние ключи — псевдонимы после ротации)
	prevUntil   time.Time         // до какого момента действуют псевдонимы после ротации
	checked     time.Time         // когда запись сверена с хранилищем
	touched     time.Time         // когда в хранилище записана активность
	pendingConn string            // активный кластер, который ещё надо подключить на этой реплике
}

// Параметры работы с общим хранилищем сессий (ADR-0016, раздел 1).
const (
	// sessionCacheTTL — как долго запись, сверенная с хранилищем, считается верной: не более одного SELECT
	// на сессию за этот срок. Худшая задержка отзыва сессии без доставленного события.
	sessionCacheTTL = 2 * time.Second
	// sessionStaleGrace — сколько реплика обслуживает сессию из кэша, если хранилище недоступно (fail-closed).
	sessionStaleGrace = 30 * time.Second
	// activityWriteEvery — не чаще одной записи активности в хранилище на сессию.
	activityWriteEvery = 20 * time.Second
	// storeTimeout — предел одной операции с хранилищем сессий.
	storeTimeout = 5 * time.Second
)

// ErrSessionStoreUnavailable — общее хранилище сессий недоступно и кэш реплики устарел: ответ 503, пользователь
// не выходит из системы, после восстановления БД повторный вход не нужен.
var ErrSessionStoreUnavailable = errors.New("session store is unavailable")

// Sessions — реестр веб-сессий. Идентификатор сессии (256 бит) выдаётся браузеру в cookie,
// а в памяти хранится только его SHA-256: дамп памяти или лога не даёт действующий токен.
//
// Каждая веб-сессия владеет видом ядра (Session.View): у него своя сессия пользователя, права и
// ресурсы. Режим Exclusive (не более одной веб-сессии, новый вход вытесняет прежнюю) нужен только
// там, где ядро общее на всех (тесты, одиночный режим).
//
// Без Backend реестр целиком в памяти процесса. С Backend (PostgreSQL) источник истины — хранилище:
// карта в памяти служит кэшем видов и сроков, а вид сессии, открытой на другой реплике, строится заново
// при первом запросе (Restore).
type Sessions struct {
	// Exclusive — не более одной активной веб-сессии.
	Exclusive bool
	// OnEnd вызывается (вне блокировки) для каждой завершённой сессии: выход, истечение, вытеснение.
	OnEnd func(sessionID string)
	// Release освобождает вид ядра завершённой сессии (вне блокировки); вызывается до OnEnd.
	Release func(view any)

	// Backend — общее хранилище сессий; nil — только память процесса.
	Backend websession.Backend
	// ReplicaID — идентификатор этой реплики (записывается в строку сессии).
	ReplicaID string
	// Restore строит вид для сессии, найденной в хранилище, но не имеющей вида на этой реплике:
	// создаёт вид, открывает в нём сессию пользователя и привязывает к rec.ID. Ошибка — сессия недействительна.
	Restore func(rec websession.Record) (view any, err error)
	// Publish сообщает другим репликам о завершении сессий (выход, отзыв, смена пароля); nil — не сообщать.
	// Сообщение — ускорение: остальные реплики и без него увидят это при сверке с хранилищем (кэш 2 с).
	Publish func(replicaevents.Message)
	// Sync переносит в вид данные, записанные другой репликой (время последней активности).
	Sync func(view any, rec websession.Record)

	mu    sync.Mutex
	ttl   time.Duration
	now   func() time.Time
	items map[[sha256.Size]byte]*Session
}

// NewSessions создаёт реестр; ttl — абсолютный срок жизни сессии (простой считает ядро).
func NewSessions(ttl time.Duration, exclusive bool) *Sessions {
	return &Sessions{Exclusive: exclusive, ttl: ttl, now: time.Now, items: map[[sha256.Size]byte]*Session{}}
}

func tokenKey(token string) [sha256.Size]byte { return sha256.Sum256([]byte(token)) }

func sessionID(k [sha256.Size]byte) string { return hex.EncodeToString(k[:8]) }

func randomToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic("server: crypto/rand is unavailable: " + err.Error())
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

func storeCtx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), storeTimeout)
}

// Create открывает сессию для пользователя и возвращает токен для cookie. С общим хранилищем используйте
// Open: Create не сообщает об ошибке записи.
func (s *Sessions) Create(userID string, view any) (token string, sess Session) {
	token, sess, _ = s.Open(userID, view, "")
	return token, sess
}

// Open открывает сессию. source — адрес клиента (для хранилища). Ошибка возможна только при записи в
// общее хранилище: сессия тогда не открыта, а вид освобождает вызывающий.
func (s *Sessions) Open(userID string, view any, source string) (token string, sess Session, err error) {
	token = randomToken()
	k := tokenKey(token)
	now := s.now()
	sess = Session{ID: sessionID(k), UserID: userID, CSRF: randomToken(), View: view, Expires: now.Add(s.ttl),
		LastActive: now, hash: k, checked: now, touched: now}
	if s.Backend != nil {
		ctx, cancel := storeCtx()
		err = s.Backend.Create(ctx, websession.Record{TokenHash: k, ID: sess.ID, UserID: userID, CSRF: sess.CSRF,
			CreatedAt: now, ExpiresAt: sess.Expires, LastActive: now, Source: source, ReplicaID: s.ReplicaID})
		cancel()
		if err != nil {
			return "", Session{}, err
		}
	}
	s.mu.Lock()
	var ended []Session
	if s.Exclusive {
		for _, it := range s.items {
			ended = appendUnique(ended, it)
		}
		clear(s.items)
	}
	ended = append(ended, s.sweepLocked()...)
	cp := sess
	s.items[k] = &cp
	s.mu.Unlock()
	s.notify(ended)
	return token, sess, nil
}

// appendUnique добавляет копию сессии, если её ещё нет (одна сессия может лежать под несколькими ключами).
func appendUnique(list []Session, it *Session) []Session {
	for _, e := range list {
		if e.ID == it.ID {
			return list
		}
	}
	return append(list, *it)
}

func (s *Sessions) notify(ended []Session) {
	for _, e := range ended {
		if s.Release != nil && e.View != nil {
			s.Release(e.View)
		}
		if s.OnEnd != nil {
			s.OnEnd(e.ID)
		}
	}
}

// removeLocked убирает сессию со всеми её ключами и возвращает копию (для notify).
func (s *Sessions) removeLocked(it *Session) Session {
	for k, v := range s.items {
		if v == it {
			delete(s.items, k)
		}
	}
	return *it
}

// Get возвращает действующую сессию по токену из cookie. Недоступность хранилища для Get — отказ;
// кто различает эти случаи (RPC), использует Lookup.
func (s *Sessions) Get(token string) (Session, bool) {
	sess, err := s.Lookup(token)
	return sess, err == nil
}

// Lookup возвращает действующую сессию. websession.ErrNotFound — сессии нет (или она закончилась);
// ErrSessionStoreUnavailable — общее хранилище недоступно, а кэш устарел.
func (s *Sessions) Lookup(token string) (Session, error) {
	if token == "" {
		return Session{}, websession.ErrNotFound
	}
	k := tokenKey(token)
	s.mu.Lock()
	it := s.items[k]
	now := s.now()
	if it != nil {
		switch {
		case !now.Before(it.Expires):
			ended := s.removeLocked(it)
			s.mu.Unlock()
			s.notify([]Session{ended})
			return Session{}, websession.ErrNotFound
		case it.hash != k && !now.Before(it.prevUntil): // прежний токен после ротации, срок вышел
			delete(s.items, k)
			s.mu.Unlock()
			return Session{}, websession.ErrNotFound
		case s.Backend == nil || now.Sub(it.checked) < sessionCacheTTL:
			out := *it
			s.mu.Unlock()
			return out, nil
		}
	} else if s.Backend == nil {
		s.mu.Unlock()
		return Session{}, websession.ErrNotFound
	}
	s.mu.Unlock()
	return s.revalidate(k, it)
}

// revalidate сверяет запись с общим хранилищем; it — запись этой реплики (nil — вида ещё нет).
func (s *Sessions) revalidate(k [sha256.Size]byte, it *Session) (Session, error) {
	ctx, cancel := storeCtx()
	rec, err := s.Backend.Get(ctx, k)
	cancel()
	now := s.now()
	switch {
	case errors.Is(err, websession.ErrNotFound):
		if it != nil {
			s.mu.Lock()
			var ended []Session
			if s.items[k] == it {
				if it.hash == k {
					ended = append(ended, s.removeLocked(it)) // сессию закончили (выход, отзыв) — освобождаем вид
				} else {
					delete(s.items, k) // устаревший псевдоним: сама сессия жива под новым токеном
				}
			}
			s.mu.Unlock()
			s.notify(ended)
		}
		return Session{}, websession.ErrNotFound
	case err != nil:
		slog.Warn("sessions: the store is unavailable", "error", err)
		if it != nil {
			s.mu.Lock()
			stale := now.Sub(it.checked) < sessionStaleGrace
			out := *it
			s.mu.Unlock()
			if stale {
				return out, nil
			}
		}
		return Session{}, ErrSessionStoreUnavailable
	}

	var view any
	s.mu.Lock()
	known := s.items[k] != nil || s.items[rec.TokenHash] != nil // есть вид (в том числе под новым токеном после ротации)
	s.mu.Unlock()
	if it == nil && !known && s.Restore != nil { // вид строится вне блокировки: он может обращаться к хранилищам
		if view, err = s.Restore(rec); err != nil {
			ctx, cancel := storeCtx()
			_ = s.Backend.Delete(ctx, rec.ID) // пользователь удалён или потерял вход: строка больше не нужна
			cancel()
			return Session{}, websession.ErrNotFound
		}
	}
	s.mu.Lock()
	cur := s.items[k]
	if cur == nil {
		cur = s.items[rec.TokenHash] // та же сессия под новым токеном (запрос со старой cookie после ротации)
	}
	if cur == nil && it != nil { // запись закрыли, пока шла сверка: не воскрешаем её без вида
		s.mu.Unlock()
		if view != nil && s.Release != nil {
			s.Release(view)
		}
		return Session{}, websession.ErrNotFound
	}
	if cur == nil {
		cur = &Session{ID: rec.ID, UserID: rec.UserID, View: view, touched: rec.LastActive, pendingConn: rec.ActiveCluster}
		view = nil
	}
	cur.CSRF, cur.Expires, cur.ActiveCluster, cur.LastActive = rec.CSRF, rec.ExpiresAt, rec.ActiveCluster, rec.LastActive
	cur.hash, cur.checked = rec.TokenHash, now
	if k != rec.TokenHash {
		cur.prevUntil = now.Add(websession.PrevTokenGrace)
	}
	s.items[k], s.items[rec.TokenHash] = cur, cur
	out, curView := *cur, cur.View
	s.mu.Unlock()
	if view != nil && s.Release != nil { // параллельный запрос успел раньше: лишний вид не нужен
		s.Release(view)
	}
	if curView != nil && s.Sync != nil {
		s.Sync(curView, rec)
	}
	return out, nil
}

// TakePendingCluster возвращает (однократно) активный кластер, записанный другой репликой и ещё не подключённый
// здесь: первый запрос сессии на этой реплике подключает его (права проверяются как обычно).
func (s *Sessions) TakePendingCluster(sessionID string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, it := range s.items {
		if it.ID == sessionID {
			c := it.pendingConn
			it.pendingConn = ""
			return c
		}
	}
	return ""
}

// SetActiveCluster запоминает активный кластер сессии в хранилище, чтобы другая реплика подключила его заново.
func (s *Sessions) SetActiveCluster(sessionID, clusterID string) {
	s.mu.Lock()
	for _, it := range s.items {
		if it.ID == sessionID {
			it.ActiveCluster = clusterID
		}
	}
	s.mu.Unlock()
	if s.Backend == nil {
		return
	}
	ctx, cancel := storeCtx()
	defer cancel()
	if err := s.Backend.SetActiveCluster(ctx, sessionID, clusterID); err != nil {
		slog.Warn("sessions: the active cluster was not saved", "error", err)
	}
}

// Activity отмечает действие пользователя в интерфейсе: в хранилище пишется не чаще раза в activityWriteEvery.
func (s *Sessions) Activity(sessionID string) {
	if s.Backend == nil {
		return
	}
	now := s.now()
	s.mu.Lock()
	due := false
	for _, it := range s.items {
		if it.ID == sessionID {
			it.LastActive = now
			if now.Sub(it.touched) >= activityWriteEvery {
				it.touched, due = now, true
			}
		}
	}
	s.mu.Unlock()
	if !due {
		return
	}
	ctx, cancel := storeCtx()
	defer cancel()
	if err := s.Backend.Touch(ctx, sessionID, now); err != nil {
		slog.Warn("sessions: the activity was not saved", "error", err)
	}
}

// Rotate выдаёт сессии новый токен (вход, смена пароля). Прежний действует ещё websession.PrevTokenGrace.
func (s *Sessions) Rotate(token string) (string, error) {
	sess, err := s.Lookup(token)
	if err != nil {
		return "", err
	}
	newTok := randomToken()
	nk := tokenKey(newTok)
	now := s.now()
	if s.Backend != nil {
		ctx, cancel := storeCtx()
		err = s.Backend.Rotate(ctx, sess.ID, nk, now.Add(websession.PrevTokenGrace))
		cancel()
		if err != nil {
			return "", err
		}
	}
	s.mu.Lock()
	for _, it := range s.items {
		if it.ID == sess.ID {
			it.hash, it.prevUntil = nk, now.Add(websession.PrevTokenGrace)
			s.items[nk] = it
			break
		}
	}
	s.mu.Unlock()
	return newTok, nil
}

// Delete закрывает сессию: удаляет строку в хранилище (на всех репликах сессия перестаёт действовать не
// позже sessionCacheTTL) и освобождает вид на этой реплике. Ошибка хранилища возвращается: выход, который
// не удалось записать, не должен выглядеть удавшимся.
func (s *Sessions) Delete(token string) error {
	k := tokenKey(token)
	s.mu.Lock()
	it := s.items[k]
	s.mu.Unlock()
	var err error
	if s.Backend != nil {
		id := ""
		if it != nil {
			id = it.ID
		} else {
			ctx, cancel := storeCtx()
			rec, gerr := s.Backend.Get(ctx, k)
			cancel()
			if gerr == nil {
				id = rec.ID
			} else if !errors.Is(gerr, websession.ErrNotFound) {
				err = gerr
			}
		}
		if id != "" {
			ctx, cancel := storeCtx()
			if derr := s.Backend.Delete(ctx, id); derr != nil {
				err = derr
			} else {
				s.publish(replicaevents.Message{Kind: replicaevents.SessionEnded, Session: id})
			}
			cancel()
		}
	}
	if it != nil {
		s.mu.Lock()
		var ended []Session
		if s.items[k] == it {
			ended = append(ended, s.removeLocked(it))
		}
		s.mu.Unlock()
		s.notify(ended)
	}
	return err
}

// EndUser завершает все сессии пользователя, кроме exceptID (пусто — все): отключение, смена пароля.
// В хранилище строки удаляются сразу, другие реплики узнают об этом при ближайшей сверке (до sessionCacheTTL).
func (s *Sessions) EndUser(userID, exceptID string) error {
	var err error
	if s.Backend != nil {
		ctx, cancel := storeCtx()
		_, err = s.Backend.DeleteUser(ctx, userID, exceptID)
		cancel()
		if err == nil {
			// Session — исключение: сессия, из которой сменили пароль, остаётся.
			s.publish(replicaevents.Message{Kind: replicaevents.UserSessionsEnded, User: userID, Session: exceptID})
		}
	}
	s.endLocal(func(it *Session) bool { return it.UserID == userID && it.ID != exceptID })
	return err
}

// EndRemote завершает на этой реплике сессию, закрытую на другой (сообщение session.ended): закрывает её
// WebSocket и потоки, освобождает вид, сбрасывает кэш. Хранилище не трогает: строку уже удалил отправитель.
func (s *Sessions) EndRemote(sessionID string) {
	s.endLocal(func(it *Session) bool { return it.ID == sessionID })
}

// EndUserRemote завершает на этой реплике сессии пользователя, кроме exceptID (сообщение user.sessions_ended).
func (s *Sessions) EndUserRemote(userID, exceptID string) {
	s.endLocal(func(it *Session) bool { return it.UserID == userID && it.ID != exceptID })
}

// endLocal убирает из реестра этой реплики подходящие сессии и освобождает их ресурсы.
func (s *Sessions) endLocal(match func(*Session) bool) {
	s.mu.Lock()
	var ended []Session
	for _, it := range s.items {
		if match(it) {
			ended = appendUnique(ended, it)
		}
	}
	for _, e := range ended {
		for k, v := range s.items {
			if v.ID == e.ID {
				delete(s.items, k)
			}
		}
	}
	s.mu.Unlock()
	s.notify(ended)
}

func (s *Sessions) publish(m replicaevents.Message) {
	if s.Publish != nil {
		s.Publish(m)
	}
}

// Reap завершает сессии, у которых истёк срок или чей вид ядра больше не жив (alive == false:
// ядро закрыло пользовательскую сессию из-за простоя, отключения пользователя и т. п.). Без фоновой
// уборки такие сессии держали бы ссылки пула подключений, пока кто-нибудь не обратится к серверу.
// Возвращает число завершённых сессий.
//
// С общим хранилищем вид мог простаивать только на этой реплике, пока пользователь работает на другой:
// перед закрытием запись сверяется с хранилищем, и время активности переносится в вид (Sync).
func (s *Sessions) Reap(alive func(view any) bool) int {
	s.mu.Lock()
	ended := s.sweepLocked()
	var dead []*Session
	if alive != nil {
		seen := map[*Session]bool{}
		for _, it := range s.items {
			if it.View != nil && !seen[it] && !alive(it.View) {
				seen[it] = true
				dead = append(dead, it)
			}
		}
	}
	s.mu.Unlock()

	for _, it := range dead {
		if s.Backend != nil {
			ctx, cancel := storeCtx()
			rec, err := s.Backend.Get(ctx, it.hash)
			cancel()
			if err == nil && s.Sync != nil {
				s.Sync(it.View, rec)
				if alive(it.View) {
					continue // пользователь активен на другой реплике
				}
			}
			if err != nil && !errors.Is(err, websession.ErrNotFound) {
				continue // хранилище недоступно: решим при следующей уборке, не закрывая чужую сессию
			}
			if err == nil {
				ctx, cancel := storeCtx()
				if s.Backend.Delete(ctx, rec.ID) == nil {
					s.publish(replicaevents.Message{Kind: replicaevents.SessionEnded, Session: rec.ID})
				}
				cancel()
			}
		}
		s.mu.Lock()
		var e []Session
		if s.items[it.hash] == it {
			e = append(e, s.removeLocked(it))
		}
		s.mu.Unlock()
		ended = append(ended, e...)
	}
	s.notify(ended)
	return len(ended)
}

// Revalidate сверяет с общим хранилищем все сессии этой реплики: завершённые на другой реплике (выход, отключение
// пользователя) освобождают свой вид, даже если к ним никто не обращается. Без хранилища ничего не делает.
// Вызывается из фоновой уборки; недоступность хранилища не закрывает сессии (см. sessionStaleGrace).
func (s *Sessions) Revalidate() {
	if s.Backend == nil {
		return
	}
	type target struct {
		k  [sha256.Size]byte
		it *Session
	}
	s.mu.Lock()
	var list []target
	seen := map[*Session]bool{}
	for _, it := range s.items {
		if !seen[it] {
			seen[it] = true
			list = append(list, target{it.hash, it})
		}
	}
	s.mu.Unlock()
	for _, t := range list {
		_, _ = s.revalidate(t.k, t.it)
	}
}

// Views возвращает виды живых сессий. Фоновые проверки обходят их вне блокировки реестра: проверка прав
// и закрытие ресурсов сессии не должны задерживать вход и выход остальных.
func (s *Sessions) Views() []any {
	s.mu.Lock()
	ended := s.sweepLocked()
	out := make([]any, 0, len(s.items))
	seen := map[*Session]bool{}
	for _, it := range s.items {
		if it.View != nil && !seen[it] {
			seen[it] = true
			out = append(out, it.View)
		}
	}
	s.mu.Unlock()
	s.notify(ended)
	return out
}

// Alive сообщает, что сессия с публичным идентификатором id жива (не истекла и не закрыта).
func (s *Sessions) Alive(id string) bool {
	s.mu.Lock()
	ended := s.sweepLocked()
	alive := false
	for _, it := range s.items {
		if it.ID == id {
			alive = true
			break
		}
	}
	s.mu.Unlock()
	s.notify(ended)
	return alive
}

// Len — число живых сессий (для тестов и метрик).
func (s *Sessions) Len() int {
	s.mu.Lock()
	ended := s.sweepLocked()
	seen := map[*Session]bool{}
	for _, it := range s.items {
		seen[it] = true
	}
	n := len(seen)
	s.mu.Unlock()
	s.notify(ended)
	return n
}

func (s *Sessions) sweepLocked() (ended []Session) {
	now := s.now()
	for _, it := range s.items {
		if !now.Before(it.Expires) {
			ended = appendUnique(ended, it)
		}
	}
	for _, e := range ended {
		for k, v := range s.items {
			if v.ID == e.ID {
				delete(s.items, k)
			}
		}
	}
	return ended
}

// csrfEqual сравнивает токены за постоянное время.
func csrfEqual(a, b string) bool {
	return a != "" && subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

const (
	cookieName       = "kubron_session"
	secureCookieName = "__Host-kubron_session" // префикс __Host- требует Secure, Path=/ и отсутствия Domain
	csrfHeader       = "X-CSRF-Token"
)

// Guard — защита веб-API: cookie-сессии, CSRF-токен, источник запроса.
type Guard struct {
	Sessions *Sessions
	// TrustProxy: доверять X-Forwarded-Proto от обратного прокси (TLS завершается на нём).
	TrustProxy bool
	// TrustedProxyHops — сколько доверенных прокси стоит перед сервером (по умолчанию 1): адрес клиента
	// берётся из X-Forwarded-For справа, а не слева (левые элементы подделывает клиент).
	TrustedProxyHops int
	// TrustedProxies — адреса доверенных прокси; если задан, вместо счётчика используется он (clientip.go).
	TrustedProxies []netip.Prefix
	// AnonLimiter — ограничение частоты запросов без сессии по адресу клиента; nil — без ограничения.
	AnonLimiter *RateLimiter
	// Hub — WebSocket-хаб событий; nil отключает /api/ws.
	Hub *Hub
	// Terminals — реестр веб-терминалов; nil отключает /api/terminal/{id}.
	Terminals *termbuf.Registry
	// NewView создаёт вид ядра для нового входа (source — адрес клиента); nil — все сессии работают
	// с общим экземпляром диспетчера (тесты). Освобождение — Sessions.Release.
	NewView func(source string) any
	// Bind сообщает виду идентификатор созданной для него веб-сессии (адресат событий); nil — не нужно.
	Bind func(view any, sessionID string)

	// ActiveCluster возвращает активный кластер вида (для записи в общее хранилище сессий); nil — не запоминать.
	ActiveCluster func(view any) string
	// RestoreCluster подключает вид к кластеру, активному в сессии на другой реплике; nil — не подключать.
	RestoreCluster func(view any, clusterID string) error

	// Lifecycle — идентичность и состояние остановки реплики (draining, закрытие соединений); nil — без них.
	Lifecycle *Lifecycle

	termConns termConns
}

// CloseSession закрывает все WebSocket сессии (события и терминалы). Подключается к Sessions.OnEnd.
func (g *Guard) CloseSession(sessionID string) {
	if g.Hub != nil {
		g.Hub.CloseSession(sessionID)
	}
	g.termConns.closeSession(sessionID)
}

func (g *Guard) secure(r *http.Request) bool { return isSecure(r, g.TrustProxy) }

// clientIP — адрес клиента для журнала и блокировки перебора паролей (см. ProxyTrust).
func (g *Guard) clientIP(r *http.Request) string {
	if g == nil {
		return remoteHost(r)
	}
	return ProxyTrust{Enabled: g.TrustProxy, Hops: g.TrustedProxyHops, CIDRs: g.TrustedProxies}.ClientIP(r)
}

func (g *Guard) token(r *http.Request) string {
	for _, n := range []string{secureCookieName, cookieName} {
		if c, err := r.Cookie(n); err == nil && c.Value != "" {
			return c.Value
		}
	}
	return ""
}

func (g *Guard) setCookie(w http.ResponseWriter, r *http.Request, token string, ttl time.Duration) {
	c := &http.Cookie{
		Name: cookieName, Value: token, Path: "/", HttpOnly: true,
		SameSite: http.SameSiteLaxMode, Secure: g.secure(r),
	}
	if c.Secure {
		c.Name = secureCookieName
	}
	if ttl < 0 {
		c.MaxAge = -1
		c.Value = ""
	} else {
		c.MaxAge = int(ttl.Seconds())
	}
	http.SetCookie(w, c)
}

func (g *Guard) clearCookie(w http.ResponseWriter, r *http.Request) {
	g.setCookie(w, r, "", -1)
}
