package metrics

import (
	"context"
	"errors"
	"testing"
	"time"

	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.opentelemetry.io/otel/semconv/v1.40.0/genaiconv"
	"go.opentelemetry.io/otel/semconv/v1.43.0/messagingconv"

	"github.com/webitel/webitel-go-kit/infra/otel/semconv/v0.2.0/kbconv"
	"github.com/webitel/webitel-go-kit/infra/otel/semconv/v0.2.0/outboxconv"

	"github.com/webitel/webitel-kb/internal/model"
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

// point is one data point of any aggregation.
type point struct {
	attrs attribute.Set
	value float64
}

// collect returns the points of every metric by name.
func collect(t *testing.T, reader *sdkmetric.ManualReader) map[string][]point {
	t.Helper()

	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("collect: %v", err)
	}

	out := make(map[string][]point)

	for _, scope := range rm.ScopeMetrics {
		for _, m := range scope.Metrics {
			switch data := m.Data.(type) {
			case metricdata.Sum[int64]:
				for _, p := range data.DataPoints {
					out[m.Name] = append(out[m.Name], point{p.Attributes, float64(p.Value)})
				}
			case metricdata.Gauge[float64]:
				for _, p := range data.DataPoints {
					out[m.Name] = append(out[m.Name], point{p.Attributes, p.Value})
				}
			case metricdata.Histogram[float64]:
				for _, p := range data.DataPoints {
					out[m.Name] = append(out[m.Name], point{p.Attributes, float64(p.Count)})
				}
			default:
				t.Fatalf("%s: unexpected aggregation %T", m.Name, m.Data)
			}
		}
	}

	return out
}

// byAttr maps the points to the value of one attribute.
func byAttr(points []point, key attribute.Key) map[string]float64 {
	out := make(map[string]float64, len(points))

	for _, p := range points {
		v, _ := p.attrs.Value(key)
		out[v.String()] = p.value
	}

	return out
}

func attrs(p point) map[string]string {
	out := make(map[string]string)

	for _, kv := range p.attrs.ToSlice() {
		out[string(kv.Key)] = kv.Value.String()
	}

	return out
}

func TestReadingsFollowTheRelayTerm(t *testing.T) {
	counts := map[int32]int64{model.IndexStateIndexed: 5, model.IndexStateFailed: 2}

	tests := []struct {
		name       string
		apply      func(m *Metrics)
		wantStatus map[string]float64
		wantRead   bool
	}{
		{
			name:       "follower",
			apply:      func(*Metrics) {},
			wantStatus: map[string]float64{"leader": 0, "follower": 1},
		},
		{
			name: "leading with readings",
			apply: func(m *Metrics) {
				m.Leading(true)
				m.Backlog(3, 90*time.Second)
				m.IndexStates(counts)
			},
			wantStatus: map[string]float64{"leader": 1, "follower": 0},
			wantRead:   true,
		},
		{
			name: "stepped down",
			apply: func(m *Metrics) {
				m.Leading(true)
				m.Backlog(3, 90*time.Second)
				m.IndexStates(counts)
				m.Leading(false)
			},
			wantStatus: map[string]float64{"leader": 0, "follower": 1},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, reader := newTestMetrics(t)
			tt.apply(m)

			got := collect(t, reader)

			status := byAttr(got[outboxconv.RelayStatusObservable{}.Name()], "webitel.outbox.relay.state")
			if len(status) != 2 || status["leader"] != tt.wantStatus["leader"] ||
				status["follower"] != tt.wantStatus["follower"] {
				t.Fatalf("relay status = %v, want %v", status, tt.wantStatus)
			}

			reported := map[string]int{
				outboxconv.EventCountObservable{}.Name():    len(got[outboxconv.EventCountObservable{}.Name()]),
				outboxconv.EventAgeObservable{}.Name():      len(got[outboxconv.EventAgeObservable{}.Name()]),
				kbconv.ArticleIndexCountObservable{}.Name(): len(got[kbconv.ArticleIndexCountObservable{}.Name()]),
			}

			for name, n := range reported {
				if (n > 0) != tt.wantRead {
					t.Fatalf("%s reported %d points, want reported %v", name, n, tt.wantRead)
				}
			}

			if !tt.wantRead {
				return
			}

			if v := got[outboxconv.EventCountObservable{}.Name()][0].value; v != 3 {
				t.Fatalf("event count = %v, want 3", v)
			}

			if v := got[outboxconv.EventAgeObservable{}.Name()][0].value; v != 90 {
				t.Fatalf("event age = %v, want 90", v)
			}

			indexes := byAttr(got[kbconv.ArticleIndexCountObservable{}.Name()], "webitel.kb.article.index.state")
			want := map[string]float64{"pending": 0, "indexing": 0, "indexed": 5, "failed": 2}

			if len(indexes) != len(want) {
				t.Fatalf("index count = %v, want %v", indexes, want)
			}

			for state, n := range want {
				if indexes[state] != n {
					t.Fatalf("index count = %v, want %v", indexes, want)
				}
			}
		})
	}
}

func TestProviderCallsCarryTheError(t *testing.T) {
	failure := errors.New("timeout")

	tests := []struct {
		name   string
		metric string
		record func(m *Metrics, err error)
		err    error
		want   map[string]string
	}{
		{
			name:   "embedding answered",
			metric: genaiconv.ClientOperationDuration{}.Name(),
			record: func(m *Metrics, err error) {
				m.Embedding(context.Background(), "gemini", "gemini-embedding-001", time.Second, err)
			},
			want: map[string]string{
				"gen_ai.operation.name": "embeddings",
				"gen_ai.provider.name":  "gcp.gemini",
				"gen_ai.request.model":  "gemini-embedding-001",
			},
		},
		{
			name:   "embedding failed",
			metric: genaiconv.ClientOperationDuration{}.Name(),
			record: func(m *Metrics, err error) {
				m.Embedding(context.Background(), "e5", "multilingual-e5-large", time.Second, err)
			},
			err: failure,
			want: map[string]string{
				"gen_ai.operation.name": "embeddings",
				"gen_ai.provider.name":  "e5",
				"gen_ai.request.model":  "multilingual-e5-large",
				"error.type":            "*errors.errorString",
			},
		},
		{
			name:   "rerank failed",
			metric: kbconv.RerankDuration{}.Name(),
			record: func(m *Metrics, err error) {
				m.Rerank(context.Background(), "bge-reranker", "bge-reranker-v2-m3", time.Second, err)
			},
			err: failure,
			want: map[string]string{
				"webitel.kb.rerank.provider": "bge-reranker",
				"webitel.kb.rerank.model":    "bge-reranker-v2-m3",
				"error.type":                 "*errors.errorString",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, reader := newTestMetrics(t)
			tt.record(m, tt.err)

			points := collect(t, reader)[tt.metric]
			if len(points) != 1 || points[0].value != 1 {
				t.Fatalf("%s: %v, want one recorded call", tt.metric, points)
			}

			got := attrs(points[0])
			if len(got) != len(tt.want) {
				t.Fatalf("attributes = %v, want %v", got, tt.want)
			}

			for key, want := range tt.want {
				if got[key] != want {
					t.Fatalf("attributes = %v, want %v", got, tt.want)
				}
			}
		})
	}
}

func TestOutboxCounters(t *testing.T) {
	m, reader := newTestMetrics(t)
	ctx := context.Background()

	m.Published(ctx, "kb.reindex", nil)
	m.Published(ctx, "kb.reindex", nil)
	m.Published(ctx, "kb.reindex", errors.New("broker is gone"))
	m.Poisoned(ctx)

	got := collect(t, reader)

	sent := got[messagingconv.ClientSentMessages{}.Name()]
	if len(sent) != 2 {
		t.Fatalf("sent messages = %v, want a series for success and one for failure", sent)
	}

	for _, p := range sent {
		a := attrs(p)
		if a["messaging.system"] != "rabbitmq" || a["messaging.operation.name"] != "send" ||
			a["messaging.destination.template"] != "kb.reindex" {
			t.Fatalf("sent messages attributes = %v", a)
		}
	}

	if byError := byAttr(sent, "error.type"); byError[""] != 2 || byError["*errors.errorString"] != 1 {
		t.Fatalf("sent messages by error.type = %v, want 2 sent and 1 failed", byError)
	}

	poisoned := got[outboxconv.RelayPoisoned{}.Name()]
	if len(poisoned) != 1 || poisoned[0].value != 1 {
		t.Fatalf("poisoned = %v, want 1", poisoned)
	}
}
