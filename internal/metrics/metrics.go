// Package metrics Prometheus metin biçiminde (exposition format 0.0.4) sayaç ve histogram sağlar.
// Harici bağımlılık eklememek için minimal bir uygulama yazıldı (bkz. DECISIONS.md).
package metrics

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
)

// Registry metrikleri tutar.
type Registry struct {
	mu       sync.Mutex
	counters map[string]*counterVec
	hists    map[string]*histVec
	gauges   []GaugeFunc
}

// GaugeFunc kazıma anında hesaplanan gösterge satırlarını yazar.
type GaugeFunc func(w io.Writer)

// New boş kayıt.
func New() *Registry {
	return &Registry{counters: map[string]*counterVec{}, hists: map[string]*histVec{}}
}

type counterVec struct {
	help   string
	labels []string
	vals   map[string]float64
}

type histVec struct {
	help    string
	labels  []string
	buckets []float64
	series  map[string]*histSeries
}

type histSeries struct {
	counts []uint64
	sum    float64
	n      uint64
}

// Counter bir sayaç tanımlar.
func (r *Registry) Counter(name, help string, labels ...string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.counters[name] = &counterVec{help: help, labels: labels, vals: map[string]float64{}}
}

// Histogram bir histogram tanımlar.
func (r *Registry) Histogram(name, help string, buckets []float64, labels ...string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.hists[name] = &histVec{help: help, labels: labels, buckets: buckets, series: map[string]*histSeries{}}
}

// Gauge kazıma anında çalışacak bir gösterge fonksiyonu ekler.
func (r *Registry) Gauge(f GaugeFunc) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.gauges = append(r.gauges, f)
}

// Inc sayacı artırır.
func (r *Registry) Inc(name string, labelValues ...string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	c, ok := r.counters[name]
	if !ok {
		return
	}
	c.vals[strings.Join(labelValues, "\x00")]++
}

// Observe histograma gözlem ekler.
func (r *Registry) Observe(name string, v float64, labelValues ...string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	h, ok := r.hists[name]
	if !ok {
		return
	}
	key := strings.Join(labelValues, "\x00")
	s := h.series[key]
	if s == nil {
		s = &histSeries{counts: make([]uint64, len(h.buckets))}
		h.series[key] = s
	}
	for i, b := range h.buckets {
		if v <= b {
			s.counts[i]++
		}
	}
	s.sum += v
	s.n++
}

// Write tüm metrikleri yazar.
func (r *Registry) Write(w io.Writer) {
	r.mu.Lock()
	names := make([]string, 0, len(r.counters))
	for n := range r.counters {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		c := r.counters[n]
		fmt.Fprintf(w, "# HELP %s %s\n# TYPE %s counter\n", n, c.help, n)
		for _, k := range sortedKeys(c.vals) {
			fmt.Fprintf(w, "%s%s %g\n", n, labelStr(c.labels, k, ""), c.vals[k])
		}
	}
	hnames := make([]string, 0, len(r.hists))
	for n := range r.hists {
		hnames = append(hnames, n)
	}
	sort.Strings(hnames)
	for _, n := range hnames {
		h := r.hists[n]
		fmt.Fprintf(w, "# HELP %s %s\n# TYPE %s histogram\n", n, h.help, n)
		keys := make([]string, 0, len(h.series))
		for k := range h.series {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			s := h.series[k]
			for i, b := range h.buckets {
				fmt.Fprintf(w, "%s_bucket%s %d\n", n, labelStr(h.labels, k, fmt.Sprintf(`le="%g"`, b)), s.counts[i])
			}
			fmt.Fprintf(w, "%s_bucket%s %d\n", n, labelStr(h.labels, k, `le="+Inf"`), s.n)
			fmt.Fprintf(w, "%s_sum%s %g\n", n, labelStr(h.labels, k, ""), s.sum)
			fmt.Fprintf(w, "%s_count%s %d\n", n, labelStr(h.labels, k, ""), s.n)
		}
	}
	gauges := append([]GaugeFunc(nil), r.gauges...)
	r.mu.Unlock()
	for _, g := range gauges {
		g(w)
	}
}

func sortedKeys(m map[string]float64) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func labelStr(names []string, key, extra string) string {
	var parts []string
	if len(names) > 0 {
		vals := strings.Split(key, "\x00")
		for i, n := range names {
			v := ""
			if i < len(vals) {
				v = vals[i]
			}
			parts = append(parts, fmt.Sprintf(`%s="%s"`, n, escape(v)))
		}
	}
	if extra != "" {
		parts = append(parts, extra)
	}
	if len(parts) == 0 {
		return ""
	}
	return "{" + strings.Join(parts, ",") + "}"
}

func escape(s string) string {
	return strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`).Replace(s)
}
