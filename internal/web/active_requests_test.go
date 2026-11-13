package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

func TestHTTPActiveRequests(t *testing.T) {
	_, reader := telemetryTestProviders(t)
	check := func(want int64) {
		t.Helper()
		var data metricdata.ResourceMetrics
		if err := reader.Collect(context.Background(), &data); err != nil {
			t.Fatal(err)
		}
		for _, scope := range data.ScopeMetrics {
			for _, m := range scope.Metrics {
				if m.Name != "http.server.active_requests" {
					continue
				}
				sum := m.Data.(metricdata.Sum[int64])
				if len(sum.DataPoints) != 1 || sum.DataPoints[0].Value != want {
					t.Fatalf("Wrong active count: %v", sum.DataPoints)
				}
				if sum.DataPoints[0].Attributes.Len() != 1 {
					t.Fatal("Unexpected dimensions")
				}
				return
			}
		}
		t.Fatal("Missing active requests")
	}
	routes := http.NewServeMux()
	routes.HandleFunc("GET /active", func(w http.ResponseWriter, r *http.Request) { check(1); w.WriteHeader(200) })
	instrumentHTTP(routes, routes).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/active?private=value", nil))
	check(0)
}
