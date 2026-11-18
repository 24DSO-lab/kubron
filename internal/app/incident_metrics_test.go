// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/24DSO-lab/kubron/internal/opsplatform"
	"github.com/24DSO-lab/kubron/internal/signals"
)

// fakeMetrics — подставка платформенного читателя метрик.
type fakeMetrics struct {
	samples []opsplatform.MetricSample
	err     error
}

func (f fakeMetrics) Query(context.Context, string, time.Time) ([]opsplatform.MetricSample, error) {
	return f.samples, f.err
}

func TestMetricsQuerierConvertsSamples(t *testing.T) {
	at := time.Unix(1700000000, 0).UTC()
	q := metricsQuerier{fakeMetrics{samples: []opsplatform.MetricSample{
		{Labels: map[string]string{"pod": "p1"}, Value: 0.5, At: at},
		{Labels: map[string]string{"pod": "p2"}, Value: 2, At: at},
	}}}
	got, err := q.Query(context.Background(), "up", at)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Labels["pod"] != "p1" || got[0].Value != 0.5 || !got[0].At.Equal(at) || got[1].Value != 2 {
		t.Fatalf("samples were not converted as is: %+v", got)
	}
}

func TestMetricsQuerierMapsErrors(t *testing.T) {
	cases := []struct {
		name     string
		platform error
		domain   error
	}{
		{"not found", opsplatform.ErrNotFound, signals.ErrNotFound},
		{"forbidden", opsplatform.ErrForbidden, signals.ErrForbidden},
		{"unavailable", opsplatform.ErrUnavailable, signals.ErrUnavailable},
		{"too large", opsplatform.ErrTooLarge, signals.ErrTooLarge},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			wrapped := fmt.Errorf("metrics: %w", c.platform)
			got, err := metricsQuerier{fakeMetrics{err: wrapped}}.Query(context.Background(), "up", time.Time{})
			if got != nil {
				t.Errorf("samples on error: %+v", got)
			}
			if !errors.Is(err, c.domain) {
				t.Errorf("error %v is not the domain error %v", err, c.domain)
			}
			if !errors.Is(err, c.platform) {
				t.Errorf("error %v lost the platform error %v", err, c.platform)
			}
			if err.Error() != wrapped.Error() {
				t.Errorf("error text changed: %q, want %q", err.Error(), wrapped.Error())
			}
		})
	}
}

func TestMetricsQuerierKeepsUnknownError(t *testing.T) {
	boom := errors.New("boom")
	_, err := metricsQuerier{fakeMetrics{err: boom}}.Query(context.Background(), "up", time.Time{})
	if !errors.Is(err, boom) {
		t.Fatalf("unknown error was replaced: %v", err)
	}
}

func TestMetricSignalsWithoutMetrics(t *testing.T) {
	if p := metricSignals(nil); p != nil {
		t.Fatalf("no metrics must mean no signals provider, got %T", p)
	}
	if metricSignals(fakeMetrics{}) == nil {
		t.Fatal("a metrics reader must give a signals provider")
	}
}

func TestIncidentCallerCarriesSignals(t *testing.T) {
	if c := incidentCaller(&opsplatform.Op{}); c.Signals != nil {
		t.Errorf("an operation without metrics must not carry signals, got %T", c.Signals)
	}
	if c := incidentCaller(&opsplatform.Op{Metrics: fakeMetrics{}}); c.Signals == nil {
		t.Error("an operation with metrics must carry signals")
	}
	if c := storedCaller(&opsplatform.Op{Metrics: fakeMetrics{}}); c.Signals != nil || c.Reader != nil {
		t.Error("a stored-only caller must not read the cluster")
	}
}
