// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package update

import (
	"context"
	"errors"
	"log/slog"
	"math/rand/v2"
	"strings"
	"sync"
	"time"
)

const (
	// DefaultInterval — как часто фоновая проверка спрашивает GitHub. Без токена лимит API — 60 запросов в час на адрес,
	// а условные запросы (ETag) его не расходуют, так что сутки с большим запасом.
	DefaultInterval = 24 * time.Hour
	// DefaultRetryInterval — повтор после неудачи (нет сети, лимит): не чаще раза в час.
	DefaultRetryInterval = time.Hour
	// DefaultMinForceGap — минимальный промежуток между ручными проверками. Кнопка «Проверить» нажимается любым
	// администратором, а лимит GitHub общий для адреса сервера: частые нажатия отдают кэш.
	DefaultMinForceGap = time.Minute
	// DefaultStartDelay — пауза перед первой фоновой проверкой: запуск не должен ждать сети.
	DefaultStartDelay = 30 * time.Second

	// Разброс интервала между проверками (±10 %), чтобы реплики, запущенные вместе, не ходили на GitHub одновременно.
	jitterFraction = 0.1
)

// Переменная окружения политики проверки: on (по умолчанию) | off. off — Locked: сервер не обращается к сети.
const EnvUpdateCheck = "KUBRON_UPDATE_CHECK"

// ReleaseSource — откуда берётся последний релиз (Client или заглушка в тестах).
type ReleaseSource interface {
	Latest(ctx context.Context) (*Release, error)
}

// Config — параметры Checker. Нулевые значения интервалов заменяются значениями по умолчанию.
type Config struct {
	Current string // версия этой сборки: «1.2.0» или DevVersion
	Install Install
	Repo    string // по умолчанию DefaultRepo
	Source  ReleaseSource

	// Locked запрещает любые обращения к сети (политика администратора).
	Locked bool
	// AutoCheck — начальное состояние фоновой проверки.
	AutoCheck bool

	Interval, RetryInterval, MinForceGap, StartDelay time.Duration
	Now                                              func() time.Time
}

// Checker хранит последний известный релиз и обновляет его в фоне и по запросу.
// Info не обращается к сети и не блокируется надолго, поэтому подходит для каждого запроса страницы.
type Checker struct {
	cfg Config

	fetchMu sync.Mutex // не больше одного запроса к GitHub одновременно

	mu        sync.RWMutex
	autoCheck bool
	latest    *Release
	checkedAt time.Time // последняя удачная проверка
	attempted time.Time // последняя попытка, удачная или нет
	errCode   string
	announced string // версия, о которой уже написано в журнал
}

// NewChecker создаёт Checker. Source по умолчанию — Client для DefaultRepo.
func NewChecker(cfg Config) *Checker {
	if cfg.Repo == "" {
		cfg.Repo = DefaultRepo
	}
	if cfg.Source == nil {
		cfg.Source = NewClient(cfg.Current)
	}
	if cfg.Interval <= 0 {
		cfg.Interval = DefaultInterval
	}
	if cfg.RetryInterval <= 0 {
		cfg.RetryInterval = DefaultRetryInterval
	}
	if cfg.MinForceGap <= 0 {
		cfg.MinForceGap = DefaultMinForceGap
	}
	if cfg.StartDelay <= 0 {
		cfg.StartDelay = DefaultStartDelay
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &Checker{cfg: cfg, autoCheck: cfg.AutoCheck}
}

// PolicyFromEnv читает KUBRON_UPDATE_CHECK. locked=true — проверка запрещена. Неизвестное значение — ошибка:
// опечатка в политике не должна молча оставить исходящие запросы включёнными.
func PolicyFromEnv(getenv func(string) string) (locked bool, err error) {
	switch strings.ToLower(strings.TrimSpace(getenv(EnvUpdateCheck))) {
	case "", "on", "true", "1", "yes":
		return false, nil
	case "off", "false", "0", "no":
		return true, nil
	}
	return false, Error("invalid " + EnvUpdateCheck + " value: use on or off")
}

// checkable — можно ли сравнивать версии: у сборки «dev» и любой не-SemVer версии сравнивать не с чем.
func (c *Checker) checkable() bool {
	_, ok := parseVersion(c.cfg.Current)
	return ok
}

// Version — версия сборки и оболочка; без обращения к сети.
func (c *Checker) Version() Version {
	return Version{Version: c.cfg.Current, Target: c.cfg.Install.Target}
}

// Locked — проверка запрещена политикой.
func (c *Checker) Locked() bool { return c.cfg.Locked }

// AutoCheck — включена ли фоновая проверка.
func (c *Checker) AutoCheck() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.autoCheck && !c.cfg.Locked && c.checkable()
}

// SetAutoCheck включает или выключает фоновую проверку. Ручная проверка от этого не зависит. При Locked не действует.
func (c *Checker) SetAutoCheck(on bool) {
	c.mu.Lock()
	c.autoCheck = on
	c.mu.Unlock()
}

// Info — текущее состояние без обращения к сети.
func (c *Checker) Info() Info {
	autoCheck := c.AutoCheck()
	c.mu.RLock()
	defer c.mu.RUnlock()
	in := Info{
		Current:   c.cfg.Current,
		Install:   c.cfg.Install,
		Locked:    c.cfg.Locked,
		AutoCheck: autoCheck,
		Error:     c.errCode,
	}
	rel := c.latest
	if rel == nil {
		return in
	}
	in.Latest = rel.Version
	in.ReleaseURL = rel.URL
	in.Available = IsNewer(c.cfg.Current, rel.Version)
	if !rel.PublishedAt.IsZero() {
		in.PublishedAt = rel.PublishedAt.UTC().Format(time.RFC3339)
	}
	if !c.checkedAt.IsZero() {
		in.CheckedAt = c.checkedAt.UTC().Format(time.RFC3339)
	}
	if in.Available {
		in.Download, in.Commands = plan(c.cfg.Install, rel, c.cfg.Repo)
	}
	return in
}

// CheckNow — ручная проверка по запросу администратора. Чаще раза в MinForceGap возвращает уже известное
// состояние, не обращаясь к GitHub. При Locked и у сборки «dev» сеть не используется.
func (c *Checker) CheckNow(ctx context.Context) Info {
	c.fetch(ctx, c.cfg.MinForceGap)
	return c.Info()
}

// fetch спрашивает GitHub, если с последней попытки прошло не меньше minGap.
func (c *Checker) fetch(ctx context.Context, minGap time.Duration) {
	if c.cfg.Locked || !c.checkable() {
		return
	}
	c.fetchMu.Lock()
	defer c.fetchMu.Unlock()

	c.mu.RLock()
	last := c.attempted
	c.mu.RUnlock()
	if !last.IsZero() && c.cfg.Now().Sub(last) < minGap {
		return // другой вызов только что проверил
	}

	rel, err := c.cfg.Source.Latest(ctx)

	c.mu.Lock()
	defer c.mu.Unlock()
	c.attempted = c.cfg.Now()
	if err != nil {
		c.errCode = errorCode(err)
		slog.Debug("update: check failed", "code", c.errCode)
		return
	}
	c.latest, c.errCode, c.checkedAt = rel, "", c.attempted
	if IsNewer(c.cfg.Current, rel.Version) && c.announced != rel.Version {
		c.announced = rel.Version
		slog.Info("update: a new version is available",
			"current", c.cfg.Current, "latest", rel.Version, "install", string(c.cfg.Install.Method))
	}
}

func errorCode(err error) string {
	var e Error
	if errors.As(err, &e) {
		return string(e)
	}
	return string(ErrUnexpected)
}

// Run проверяет обновления в фоне до отмены ctx: через StartDelay, затем каждые Interval (после неудачи —
// через RetryInterval). Выключенная фоновая проверка пропускает шаг, но цикл живёт: её можно включить позже.
func (c *Checker) Run(ctx context.Context) {
	if c.cfg.Locked || !c.checkable() {
		return
	}
	timer := time.NewTimer(c.cfg.StartDelay)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		if c.AutoCheck() {
			c.fetch(ctx, 0)
		}
		timer.Reset(c.nextDelay())
	}
}

func (c *Checker) nextDelay() time.Duration {
	c.mu.RLock()
	failed := c.errCode != ""
	c.mu.RUnlock()
	if failed {
		return c.cfg.RetryInterval
	}
	spread := time.Duration(float64(c.cfg.Interval) * jitterFraction)
	if spread <= 0 {
		return c.cfg.Interval
	}
	return c.cfg.Interval - spread + rand.N(2*spread)
}
