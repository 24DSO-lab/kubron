// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/24DSO-labs/kubron/internal/metrics"
)

type answerTarget struct{}

func (answerTarget) AnswerChallenge(string, map[string]string) error { return &limitedErr{secs: 1} }

// Neither the arguments of a sign-in call (a challenge id, a password), nor the user or cluster, nor an invented
// method name reaches the metrics: the labels are the method from the allowlist (or "unknown") and the status.
func TestSignInMetricsCarryNoSecretsUsersOrClusters(t *testing.T) {
	const pw, chal, cluster, user = "CANARY-PASSWORD-e5", "CANARY-CHALLENGE-f6", "CANARY-CLUSTER-g7", "CANARY-USER-h8"
	d, err := NewDispatcher(answerTarget{}, []string{"AnswerChallenge"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	m := metrics.New("test", func() metrics.Snapshot { return metrics.Snapshot{} })
	h := WithRPCObserver(WithRequestID(NewHandler(d, nil, nil)), d, m)
	post(h, "/api/rpc/AnswerChallenge", `["`+chal+`",{"password":"`+pw+`","user":"`+user+`"}]`, nil)
	post(h, "/api/rpc/"+cluster, `[]`, nil)
	post(h, "/api/rpc/AnswerChallenge", `["`+chal+`",{"password":"`+pw+`"}]`, map[string]string{"X-Cluster": cluster})

	rec := httptest.NewRecorder()
	m.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	out := rec.Body.String()
	if !strings.Contains(out, `method="AnswerChallenge"`) || !strings.Contains(out, `code="429"`) {
		t.Fatalf("the call must be counted by method and status:\n%s", out)
	}
	for _, s := range []string{pw, chal, cluster, user} {
		if strings.Contains(out, s) {
			t.Errorf("the metrics contain the control string %q", s)
		}
	}
}
