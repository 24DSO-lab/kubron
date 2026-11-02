// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

// Package netpolicy защищает сервер от SSRF: исходящие соединения, адрес которых задаёт пользователь
// (репозитории Helm, OCI-реестры, адрес API-сервера кластера), проверяются по адресу, к которому
// действительно открывается сокет (net.Dialer.Control), а не по разбору URL заранее. Поэтому подмена
// DNS между проверкой и соединением (DNS rebinding), нестандартные записи IP и редиректы не обходят запрет.
//
// Политика включается так же, как k8s.SetConnectionPolicy: сервер выставляет её при старте (Set), десктоп
// и режим одного пользователя её не выставляют — тогда For возвращает nil, а все методы nil-политики
// ничего не ограничивают. Решение и профили — ADR-0015.
package netpolicy

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/netip"
	"syscall"
	"time"
)

// Profile — набор запретов.
type Profile int

const (
	// ProfileOff — проверка выключена (New вернёт nil-политику).
	ProfileOff Profile = iota
	// ProfilePermissive запрещает только то, что опасно всегда: loopback, link-local (включая адреса
	// метаданных облака), unspecified, multicast. Частные диапазоны разрешены (адрес API-сервера кластера).
	ProfilePermissive
	// ProfileStrict дополнительно запрещает частные диапазоны: 10/8, 172.16/12, 192.168/16, fc00::/7, 100.64/10
	// (репозитории Helm и OCI-реестры).
	ProfileStrict
)

func (p Profile) String() string {
	switch p {
	case ProfilePermissive:
		return "permissive"
	case ProfileStrict:
		return "strict"
	}
	return "off"
}

// Reason — почему адрес запрещён. Идёт только в серверный лог, пользователю не показывается.
type Reason string

const (
	ReasonLoopback    Reason = "loopback"
	ReasonLinkLocal   Reason = "link-local"
	ReasonUnspecified Reason = "unspecified"
	ReasonMulticast   Reason = "multicast"
	ReasonMetadata    Reason = "cloud-metadata"
	ReasonReserved    Reason = "reserved"
	ReasonPrivate     Reason = "private"
	ReasonInvalid     Reason = "invalid"
)

// Resolver — то, чем политика разрешает имена (net.Resolver подходит; в тестах — подмена).
type Resolver interface {
	LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error)
}

// Policy — политика одного класса соединений. Нулевое значение не используйте: создавайте через New.
// Все методы безопасны для nil-получателя и тогда ничего не ограничивают.
type Policy struct {
	// Name попадает в лог ("helm", "cluster-api").
	Name    string
	Profile Profile
	// Allow — явные исключения оператора. Снимают запрет с loopback, link-local и (в strict) частных
	// диапазонов, но никогда — с unspecified, multicast и адресов метаданных облака.
	Allow []netip.Prefix
	// Resolver разрешает имена; по умолчанию net.DefaultResolver.
	Resolver Resolver
	// Logger получает записи о блокировках; по умолчанию slog.Default().
	Logger *slog.Logger
	// DialTimeout — таймаут одной попытки соединения; по умолчанию 10 с.
	DialTimeout time.Duration
}

// New создаёт политику. Профиль ProfileOff даёт nil: проверка не действует.
func New(name string, profile Profile, allow []netip.Prefix) *Policy {
	if profile == ProfileOff {
		return nil
	}
	masked := make([]netip.Prefix, 0, len(allow))
	for _, p := range allow {
		masked = append(masked, p.Masked())
	}
	return &Policy{Name: name, Profile: profile, Allow: masked}
}

// ─── классификация адресов ───

var (
	// Известные адреса служб метаданных облаков: 169.254.169.254 (AWS, GCP, Azure, OpenStack…),
	// fd00:ec2::254 (AWS IPv6), 168.63.129.16 (Azure WireServer), 100.100.100.200 (Alibaba).
	metadataAddrs = []netip.Addr{
		netip.MustParseAddr("169.254.169.254"),
		netip.MustParseAddr("fd00:ec2::254"),
		netip.MustParseAddr("168.63.129.16"),
		netip.MustParseAddr("100.100.100.200"),
	}
	broadcast = netip.MustParseAddr("255.255.255.255")

	strictPrefixes = []netip.Prefix{
		netip.MustParsePrefix("10.0.0.0/8"),
		netip.MustParsePrefix("172.16.0.0/12"),
		netip.MustParsePrefix("192.168.0.0/16"),
		netip.MustParsePrefix("fc00::/7"),
		netip.MustParsePrefix("100.64.0.0/10"),
	}
)

// classify возвращает причину запрета ("" — адрес разрешён) и признак «исключения Allow могут это снять».
func classify(a netip.Addr, strict bool) (Reason, bool) {
	if !a.IsValid() {
		return ReasonInvalid, false
	}
	for _, m := range metadataAddrs {
		if a == m {
			return ReasonMetadata, false
		}
	}
	switch {
	case a.IsUnspecified() || (a.Is4() && a.As4()[0] == 0): // 0.0.0.0/8 на Linux ведёт на этот же хост
		return ReasonUnspecified, false
	case a == broadcast:
		return ReasonReserved, false
	case a.IsMulticast():
		return ReasonMulticast, false
	case a.IsLoopback():
		return ReasonLoopback, true
	case a.IsLinkLocalUnicast():
		return ReasonLinkLocal, true
	}
	if strict {
		for _, p := range strictPrefixes {
			if p.Contains(a) {
				return ReasonPrivate, true
			}
		}
	}
	return "", false
}

// candidates — сам адрес и IPv4, вложенный в IPv6-запись: ::ffff:a.b.c.d (Unmap), NAT64 64:ff9b::/96,
// 6to4 2002::/16, IPv4-translated ::ffff:0:a.b.c.d и устаревший IPv4-compatible ::a.b.c.d. Без этого
// metadata-сервис достижим как 64:ff9b::a9fe:a9fe в сетях с NAT64.
func candidates(a netip.Addr) []netip.Addr {
	a = a.WithZone("").Unmap()
	out := []netip.Addr{a}
	if !a.Is6() {
		return out
	}
	b := a.As16()
	v4 := func(x []byte) netip.Addr { return netip.AddrFrom4([4]byte{x[0], x[1], x[2], x[3]}) }
	zero := func(x []byte) bool {
		for _, c := range x {
			if c != 0 {
				return false
			}
		}
		return true
	}
	switch {
	case b[0] == 0x00 && b[1] == 0x64 && b[2] == 0xff && b[3] == 0x9b && zero(b[4:12]): // 64:ff9b::/96
		out = append(out, v4(b[12:16]))
	case b[0] == 0x20 && b[1] == 0x02: // 2002::/16, IPv4 в байтах 2–5
		out = append(out, v4(b[2:6]))
	case zero(b[0:8]) && b[8] == 0xff && b[9] == 0xff && b[10] == 0 && b[11] == 0: // ::ffff:0:a.b.c.d
		out = append(out, v4(b[12:16]))
	case zero(b[0:12]): // ::a.b.c.d, кроме :: и ::1
		if tail := b[12:16]; !zero(tail[:3]) || tail[3] > 1 {
			out = append(out, v4(tail))
		}
	}
	return out
}

func (p *Policy) allowed(a netip.Addr) bool {
	for _, pr := range p.Allow {
		if pr.Contains(a) {
			return true
		}
	}
	return false
}

// verdict — итог по адресу: reason != "" означает «заблокирован».
func (p *Policy) verdict(a netip.Addr) Reason {
	strict := p.Profile == ProfileStrict
	for _, c := range candidates(a) {
		r, liftable := classify(c, strict)
		if r == "" || (liftable && p.allowed(c)) {
			continue
		}
		return r
	}
	return ""
}

// ─── ошибка блокировки ───

// blockedError выглядит для пользователя как обычный отказ в соединении («connection refused»):
// по ответу нельзя отличить блокировку от закрытого порта, поэтому сервер нельзя использовать как
// сканер внутренней сети. Причина доступна только коду через IsBlocked/BlockReason и идёт в лог.
type blockedError struct{ reason Reason }

func (blockedError) Error() string { return "connection refused" }
func (blockedError) Unwrap() error { return syscall.ECONNREFUSED }

// IsBlocked сообщает, что ошибка вызвана запретом политики. Только для внутренней логики и тестов:
// пользователю текст не меняйте — он должен совпадать с обычной недоступностью.
func IsBlocked(err error) bool {
	var b blockedError
	return errors.As(err, &b)
}

// BlockReason возвращает причину блокировки ("" — ошибка не от политики).
func BlockReason(err error) Reason {
	var b blockedError
	if errors.As(err, &b) {
		return b.reason
	}
	return ""
}

func (p *Policy) logger() *slog.Logger {
	if p.Logger != nil {
		return p.Logger
	}
	return slog.Default()
}

// deny пишет причину в лог (только адрес и причина: ни URL, ни учётных данных) и возвращает ошибку,
// неотличимую от отказа в соединении.
func (p *Policy) deny(stage string, a netip.Addr, r Reason) error {
	// CheckURL is a preflight validation, not an attempted outbound connection. Logging every
	// rejected URL as a warning is noisy (and especially misleading for the default service policy).
	// Keep warnings for actual dial attempts and redirect-time enforcement.
	if stage != "url" {
		p.logger().Warn("outbound connection blocked by network policy",
			"policy", p.Name, "profile", p.Profile.String(), "stage", stage, "reason", string(r), "ip", a.WithZone("").String())
	}
	return blockedError{reason: r}
}

// opError оформляет блокировку как обычную ошибку соединения: «dial tcp 10.0.0.1:80: connection refused».
func opError(a netip.Addr, port uint16, err error) error {
	return &net.OpError{Op: "dial", Net: "tcp", Addr: net.TCPAddrFromAddrPort(netip.AddrPortFrom(a.Unmap(), port)), Err: err}
}

// CheckAddr проверяет адрес без соединения (для проверок «заранее», например при сохранении репозитория).
// Окончательное решение всё равно принимает Control на самом соединении.
func (p *Policy) CheckAddr(a netip.Addr) error {
	if p == nil {
		return nil
	}
	if r := p.verdict(a); r != "" {
		return opError(a, 0, p.deny("check", a, r))
	}
	return nil
}
