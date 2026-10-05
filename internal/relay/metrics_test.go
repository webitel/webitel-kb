package relay

import (
	"context"
	"errors"
	"testing"
	"time"

	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
	"go.opentelemetry.io/otel/semconv/v1.43.0/messagingconv"

	webitelconv "github.com/webitel/webitel-go-kit/infra/otel/semconv/v0.2.0"
	"github.com/webitel/webitel-go-kit/infra/otel/semconv/v0.2.0/kbconv"
	"github.com/webitel/webitel-go-kit/infra/otel/semconv/v0.2.0/outboxconv"

	"github.com/webitel/webitel-kb/internal/event"
	"github.com/webitel/webitel-kb/internal/model"
)

// withReader points the metrics of f at a reader the test collects.
func withReader(f *Forwarder) *sdkmetric.ManualReader {
	reader := sdkmetric.NewManualReader()
	f.metrics = newRelayMetrics(sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader)))

	return reader
}

// point is one data point of a sum or a gauge.
type point struct {
	attrs map[attribute.Key]string
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
					out[m.Name] = append(out[m.Name], point{attrsOf(p.Attributes), float64(p.Value)})
				}
			case metricdata.Gauge[float64]:
				for _, p := range data.DataPoints {
					out[m.Name] = append(out[m.Name], point{attrsOf(p.Attributes), p.Value})
				}
			default:
				t.Fatalf("%s: unexpected aggregation %T", m.Name, m.Data)
			}
		}
	}

	return out
}

func attrsOf(set attribute.Set) map[attribute.Key]string {
	out := make(map[attribute.Key]string, set.Len())

	for _, kv := range set.ToSlice() {
		out[kv.Key] = kv.Value.String()
	}

	return out
}

// byAttr maps the points to the value of one attribute.
func byAttr(points []point, key attribute.Key) map[string]float64 {
	out := make(map[string]float64, len(points))

	for _, p := range points {
		out[p.attrs[key]] = p.value
	}

	return out
}

func TestPublishRecordsTheSend(t *testing.T) {
	tests := []struct {
		name         string
		exchange     string
		brokerErr    error
		wantError    string
		wantPoisoned float64
	}{
		{name: "delivered", exchange: event.ReindexExchange},
		{
			name: "rejected by the broker", exchange: event.ReindexExchange,
			brokerErr: errors.New("broker is gone"), wantError: "*errors.errorString",
		},
		{name: "set aside", exchange: event.ReindexDLX, wantPoisoned: 1},
		{
			name: "poison queue unreachable", exchange: event.ReindexDLX,
			brokerErr: errors.New("broker is gone"), wantError: "*errors.errorString",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := testForwarder(&fakeBroker{err: tt.brokerErr}, &fakeOutbox{})
			reader := withReader(f)

			_ = f.publisherFor(tt.exchange).Publish("42", reindexMessage("42"))

			got := collect(t, reader)

			sent := got[messagingconv.ClientSentMessages{}.Name()]
			if len(sent) != 1 || sent[0].value != 1 {
				t.Fatalf("sent messages = %v, want one send", sent)
			}

			want := map[attribute.Key]string{
				semconv.MessagingOperationNameKey:   "send",
				semconv.MessagingSystemKey:          "rabbitmq",
				semconv.MessagingDestinationNameKey: tt.exchange,
			}
			if tt.wantError != "" {
				want[semconv.ErrorTypeKey] = tt.wantError
			}

			if a := sent[0].attrs; len(a) != len(want) {
				t.Fatalf("sent messages attributes = %v, want %v", a, want)
			}

			for k, v := range want {
				if sent[0].attrs[k] != v {
					t.Fatalf("sent messages attributes = %v, want %v", sent[0].attrs, want)
				}
			}

			var poisoned float64
			if p := got[outboxconv.RelayPoisoned{}.Name()]; len(p) == 1 {
				poisoned = p[0].value
			}

			if poisoned != tt.wantPoisoned {
				t.Fatalf("poisoned = %v, want %v", poisoned, tt.wantPoisoned)
			}
		})
	}
}

func TestReadingsFollowTheRelayTerm(t *testing.T) {
	store := &fakeOutbox{
		backlog:     3,
		oldest:      90 * time.Second,
		indexStates: map[int32]int64{model.IndexStateIndexed: 5, model.IndexStateFailed: 2},
	}

	observe := func(f *Forwarder) {
		f.observeBacklog(context.Background())
		f.observeIndexStates(context.Background())
	}

	tests := []struct {
		name       string
		store      *fakeOutbox
		apply      func(f *Forwarder)
		wantLeader float64
		wantRead   bool
	}{
		{name: "follower", store: store, apply: func(*Forwarder) {}},
		{
			name: "leading with readings", store: store,
			apply:      func(f *Forwarder) { f.metrics.lead(true); observe(f) },
			wantLeader: 1, wantRead: true,
		},
		{
			name: "leading, database unavailable", store: &fakeOutbox{observeError: errors.New("db")},
			apply:      func(f *Forwarder) { f.metrics.lead(true); observe(f) },
			wantLeader: 1,
		},
		{
			name: "stepped down", store: store,
			apply: func(f *Forwarder) { f.metrics.lead(true); observe(f); f.metrics.lead(false) },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := testForwarder(&fakeBroker{}, tt.store)
			reader := withReader(f)
			tt.apply(f)

			got := collect(t, reader)

			status := byAttr(got[outboxconv.RelayStatusObservable{}.Name()], webitelconv.WebitelOutboxRelayStateKey)
			if len(status) != 2 || status["leader"] != tt.wantLeader || status["follower"] != 1-tt.wantLeader {
				t.Fatalf("relay status = %v, want leader %v", status, tt.wantLeader)
			}

			for _, name := range []string{
				outboxconv.EventCountObservable{}.Name(),
				outboxconv.EventAgeObservable{}.Name(),
				kbconv.ArticleIndexCountObservable{}.Name(),
			} {
				if n := len(got[name]); (n > 0) != tt.wantRead {
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

			indexes := byAttr(got[kbconv.ArticleIndexCountObservable{}.Name()], webitelconv.WebitelKBArticleIndexStateKey)
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
