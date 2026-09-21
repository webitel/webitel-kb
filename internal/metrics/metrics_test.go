package metrics

import (
	"context"
	"errors"
	"testing"
	"time"

	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

func newTestMetrics(t *testing.T) (*Metrics, *sdkmetric.ManualReader) {
	t.Helper()

	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))

	m, err := New(provider.Meter("test"))
	if err != nil {
		t.Fatalf("new: %v", err)
	}

	return m, reader
}

// collect returns the points of every metric by name.
func collect(t *testing.T, reader *sdkmetric.ManualReader) map[string]metricdata.Aggregation {
	t.Helper()

	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("collect: %v", err)
	}

	out := make(map[string]metricdata.Aggregation)

	for _, scope := range rm.ScopeMetrics {
		for _, m := range scope.Metrics {
			out[m.Name] = m.Data
		}
	}

	return out
}

func gaugeValue(t *testing.T, data metricdata.Aggregation) (float64, bool) {
	t.Helper()

	switch g := data.(type) {
	case metricdata.Gauge[int64]:
		if len(g.DataPoints) == 0 {
			return 0, false
		}

		return float64(g.DataPoints[0].Value), true
	case metricdata.Gauge[float64]:
		if len(g.DataPoints) == 0 {
			return 0, false
		}

		return g.DataPoints[0].Value, true
	case nil:
		return 0, false
	default:
		t.Fatalf("unexpected aggregation %T", data)

		return 0, false
	}
}

func TestReadingsFollowTheRelayTerm(t *testing.T) {
	tests := []struct {
		name       string
		apply      func(m *Metrics)
		wantLeader float64
		wantRead   bool
	}{
		{
			name:       "follower",
			apply:      func(*Metrics) {},
			wantLeader: 0,
		},
		{
			name: "leading with readings",
			apply: func(m *Metrics) {
				m.Leading(true)
				m.Backlog(3, 90*time.Second)
				m.IndexFailed(2)
			},
			wantLeader: 1,
			wantRead:   true,
		},
		{
			name: "stepped down",
			apply: func(m *Metrics) {
				m.Leading(true)
				m.Backlog(3, 90*time.Second)
				m.IndexFailed(2)
				m.Leading(false)
			},
			wantLeader: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, reader := newTestMetrics(t)
			tt.apply(m)

			got := collect(t, reader)

			leader, ok := gaugeValue(t, got["kb_relay_leader"])
			if !ok || leader != tt.wantLeader {
				t.Fatalf("kb_relay_leader = %v (reported %v), want %v", leader, ok, tt.wantLeader)
			}

			for name, want := range map[string]float64{
				"kb_outbox_backlog":            3,
				"kb_outbox_oldest_age_seconds": 90,
				"kb_articles_index_failed":     2,
			} {
				value, reported := gaugeValue(t, got[name])
				if reported != tt.wantRead {
					t.Fatalf("%s reported = %v, want %v", name, reported, tt.wantRead)
				}

				if reported && value != want {
					t.Fatalf("%s = %v, want %v", name, value, want)
				}
			}
		})
	}
}

func TestProviderCallsCarryTheOutcome(t *testing.T) {
	tests := []struct {
		name   string
		metric string
		record func(m *Metrics, err error)
		err    error
		want   string
	}{
		{
			name:   "embedding answered",
			metric: "kb_embedding_duration_seconds",
			record: func(m *Metrics, err error) {
				m.Embedding(context.Background(), "gemini", "gemini-embedding-001", time.Second, err)
			},
			want: OutcomeOK,
		},
		{
			name:   "rerank failed",
			metric: "kb_rerank_duration_seconds",
			record: func(m *Metrics, err error) {
				m.Rerank(context.Background(), "gemini", "gemini-embedding-001", time.Second, err)
			},
			err:  errors.New("timeout"),
			want: OutcomeError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, reader := newTestMetrics(t)
			tt.record(m, tt.err)

			hist, ok := collect(t, reader)[tt.metric].(metricdata.Histogram[float64])
			if !ok || len(hist.DataPoints) != 1 {
				t.Fatalf("%s: no single histogram point", tt.metric)
			}

			point := hist.DataPoints[0]
			if point.Count != 1 {
				t.Fatalf("count = %d, want 1", point.Count)
			}

			for key, want := range map[attribute.Key]string{
				"provider": "gemini", "model": "gemini-embedding-001", "outcome": tt.want,
			} {
				if got, _ := point.Attributes.Value(key); got.AsString() != want {
					t.Fatalf("%s = %q, want %q", key, got.AsString(), want)
				}
			}
		})
	}
}

func TestOutboxCounters(t *testing.T) {
	m, reader := newTestMetrics(t)
	ctx := context.Background()

	m.Published(ctx, nil)
	m.Published(ctx, nil)
	m.Published(ctx, errors.New("broker is gone"))
	m.Poisoned(ctx)

	got := collect(t, reader)

	published, ok := got["kb_outbox_published_total"].(metricdata.Sum[int64])
	if !ok {
		t.Fatal("kb_outbox_published_total missing")
	}

	byOutcome := make(map[string]int64)

	for _, point := range published.DataPoints {
		outcome, _ := point.Attributes.Value("outcome")
		byOutcome[outcome.AsString()] = point.Value
	}

	if byOutcome[OutcomeOK] != 2 || byOutcome[OutcomeError] != 1 {
		t.Fatalf("published by outcome = %v, want ok 2 and error 1", byOutcome)
	}

	poisoned, ok := got["kb_outbox_poisoned_total"].(metricdata.Sum[int64])
	if !ok || len(poisoned.DataPoints) != 1 || poisoned.DataPoints[0].Value != 1 {
		t.Fatalf("kb_outbox_poisoned_total = %+v, want 1", poisoned)
	}
}
