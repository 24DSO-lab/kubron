// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package server

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/24DSO-labs/kubron/internal/auth"
)

func TestMapErrorClusterAccess(t *testing.T) {
	cases := []struct {
		err    error
		status int
		code   string
	}{
		{auth.ErrForbidden, http.StatusForbidden, "forbidden"},
		// Чужой и несуществующий кластер выглядят одинаково — «не найдено», а не «запрещено».
		{fmt.Errorf("connect: %w", auth.ErrClusterNotFound), http.StatusNotFound, "not_found"},
		{auth.ErrNotAuthenticated, http.StatusUnauthorized, "unauthenticated"},
	}
	for _, c := range cases {
		if st, code := MapError(c.err); st != c.status || code != c.code {
			t.Errorf("%v: %d %s", c.err, st, code)
		}
	}
}
