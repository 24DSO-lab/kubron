// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package credential

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"strings"
	"time"
)

const (
	// AttemptWindow bounds the rolling failure counters for interactive sign-in.
	AttemptWindow = 15 * time.Minute
	// MaxUserAnswerFailures limits failed answers by one user across identity providers.
	MaxUserAnswerFailures = 10
	// MaxIDPAnswerFailures limits failures against one identity provider across users.
	MaxIDPAnswerFailures = 50
	// AttemptLockout is the cool-down after either threshold is reached.
	AttemptLockout = 5 * time.Minute
)

// AttemptLimiter stores shared failed-answer counters. Implementations must be safe across replicas.
type AttemptLimiter interface {
	Check(ctx context.Context, userKey, idpKey string, now time.Time) (time.Duration, error)
	Fail(ctx context.Context, userKey, idpKey string, now time.Time, window time.Duration, userLimit, idpLimit int, lockout time.Duration) (time.Duration, error)
	// Reset clears only the user's counter; the aggregate IdP counter expires by its failure window.
	Reset(ctx context.Context, userKey, idpKey string) error
	Sweep(ctx context.Context, before time.Time) (int, error)
}

// RateLimitedError is returned when failed interactive sign-ins exceed a user or IdP limit.
type RateLimitedError struct{ RetryAfter time.Duration }

func (e *RateLimitedError) Error() string {
	seconds := int((e.RetryAfter + time.Second - 1) / time.Second)
	if seconds < 1 {
		seconds = 1
	}
	return fmt.Sprintf("too many requests, try again in %d s", seconds)
}

// RetryAfterSeconds lets the HTTP RPC layer translate this error to 429 + Retry-After.
func (e *RateLimitedError) RetryAfterSeconds() int {
	seconds := int((e.RetryAfter + time.Second - 1) / time.Second)
	if seconds < 1 {
		return 1
	}
	return seconds
}

// attemptKeys returns opaque keys only. Neither user identifiers nor IdP URLs are stored in the limiter.
func attemptKeys(sub Subject, d Descriptor) (userKey, idpKey string) {
	identity := attemptIdentity(d)
	if identity != "" {
		idpKey = digest("idp:" + identity)
	}
	userKey = digest("user:" + string(sub.Scope) + ":" + sub.OwnerID)
	return userKey, idpKey
}

func attemptIdentity(d Descriptor) string {
	var raw string
	switch d.Kind {
	case KindOIDC:
		raw = strings.TrimSpace(d.Params["issuer-url"])
	case KindEKS:
		raw = "aws-sts:" + strings.ToLower(strings.TrimSpace(d.Params["region"]))
	case KindGKE:
		raw = "https://accounts.google.com"
	case KindAKS:
		authority := strings.TrimRight(strings.TrimSpace(d.Params["authority-host"]), "/")
		if authority == "" {
			switch strings.ToLower(strings.TrimSpace(d.Params["environment"])) {
			case "azureusgovernmentcloud", "azureusgovernment":
				authority = "https://login.microsoftonline.us"
			case "azurechinacloud", "azurechina":
				authority = "https://login.chinacloudapi.cn"
			default:
				authority = "https://login.microsoftonline.com"
			}
		}
		raw = authority + "/" + strings.TrimSpace(d.Params["tenant-id"])
	default:
		return ""
	}
	if raw == "" {
		return ""
	}
	if u, err := url.Parse(raw); err == nil && u.Scheme != "" && u.Host != "" {
		u.Scheme = strings.ToLower(u.Scheme)
		u.Host = strings.ToLower(u.Host)
		u.Path = strings.TrimRight(u.Path, "/")
		u.RawQuery, u.Fragment, u.User = "", "", nil
		return u.String()
	}
	return strings.TrimRight(raw, "/")
}

func digest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
