// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package credential

import "errors"

// ErrServiceUnavailable means that the sign-in service (the identity provider, the network on the way to it) cannot
// be reached or answers with a server error. It is not the user's fault and is never shown as wrong credentials:
// the user may try again later. Providers wrap it into their own errors; the Broker keeps it unchanged.
var ErrServiceUnavailable = errors.New("the sign-in service is unavailable")
