package metrics

import (
	"bytes"
	"fmt"
	"io"
	"strings"
	"testing"
)

func TestExposition(t *testing.T) {
	r := New()
	r.Counter("http_requests_total", "istek", "route", "code")
	r.Histogram("http_duration_seconds", "süre", []float64{0.1, 1}, "route")
	r.Inc("http_requests_total", "/health", "200")
	r.Inc("http_requests_total", "/health", "200")
	r.Observe("http_duration_seconds", 0.05, "/health")
	r.Observe("http_duration_seconds", 0.5, "/health")
	r.Inc("unknown")
	r.Observe("unknown", 1)
	r.Gauge(func(w io.Writer) { fmt.Fprintln(w, "stock_units 3") })
	var b bytes.Buffer
	r.Write(&b)
	out := b.String()
	for _, want := range []string{
		`http_requests_total{route="/health",code="200"} 2`,
		`http_duration_seconds_bucket{route="/health",le="0.1"} 1`,
		`http_duration_seconds_bucket{route="/health",le="+Inf"} 2`,
		`http_duration_seconds_count{route="/health"} 2`,
		"# TYPE http_requests_total counter",
		"stock_units 3",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("çıktıda yok: %s\n%s", want, out)
		}
	}
	if escape(`a"b`) != `a\"b` {
		t.Error("kaçış hatalı")
	}
}
