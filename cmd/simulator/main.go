// Komut simulator seed verisini (30.000 bağışçı + 120 günlük geçmiş) yükler ve ardından
// API üzerinden canlı olay üretir: bağışlar, bilerek uygunsuz bağış denemeleri, test sonuçları,
// bileşen üretimi, hastane talepleri, dağıtım ve transfüzyon.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/msboztepe/damarbagis/internal/config"
	"github.com/msboztepe/damarbagis/internal/db"
	"github.com/msboztepe/damarbagis/internal/secure"
	"github.com/msboztepe/damarbagis/internal/sim"
)

const readyFile = "/tmp/sim-ready"

func main() {
	if len(os.Args) > 1 && os.Args[1] == "healthcheck" {
		if _, err := os.Stat(readyFile); err != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil)).With("service", "simulator")
	if err := run(log); err != nil {
		log.Error("simülatör durdu", "err", err.Error())
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	_ = os.Remove(readyFile)

	seed := envU("SIM_SEED", 42)
	cfg := sim.DefaultConfig(seed, today())
	if v := os.Getenv("SIM_ANCHOR"); v != "" {
		t, err := time.Parse("2006-01-02", v)
		if err != nil {
			return fmt.Errorf("SIM_ANCHOR YYYY-AA-GG olmalı")
		}
		cfg.Anchor = t
	}
	cfg.Donors = int(envU("SIM_DONORS", 30000))
	cfg.HistoryDays = int(envU("SIM_HISTORY_DAYS", 120))
	apiURL := env("API_URL", "http://localhost:8080")
	key, err := config.Key32("HEALTH_ENC_KEY")
	if err != nil {
		return err
	}
	sealer, err := secure.NewSealer(key)
	if err != nil {
		return err
	}

	if err := waitHealthy(ctx, apiURL, 3*time.Minute); err != nil {
		return err
	}
	pool, err := db.Connect(ctx, env("DATABASE_URL", "postgres://damar:damar@localhost:5432/damar?sslmode=disable"), 5, time.Minute)
	if err != nil {
		return err
	}
	defer pool.Close()

	seeded, oldSeed, oldAnchor, err := sim.Seeded(ctx, pool)
	if err != nil {
		return err
	}
	if seeded {
		log.Info("veritabanı zaten seed edilmiş; yükleme atlandı", "seed", oldSeed, "anchor", oldAnchor.Format("2006-01-02"))
	} else {
		t0 := time.Now()
		ds := sim.Generate(cfg)
		log.Info("seed verisi üretildi", "seed", seed, "anchor", cfg.Anchor.Format("2006-01-02"), "fingerprint", ds.Fingerprint(),
			"donors", len(ds.Donors), "donations", len(ds.Donations), "products", len(ds.Products), "ms", time.Since(t0).Milliseconds())
		if err := sim.Load(ctx, pool, ds, cfg, sealer); err != nil {
			return err
		}
		log.Info("seed verisi yüklendi", "ms", time.Since(t0).Milliseconds())
	}
	if err := os.WriteFile(readyFile, []byte("ok"), 0o644); err != nil {
		return err
	}
	if os.Getenv("SIM_LIVE") == "off" {
		log.Info("canlı mod kapalı (SIM_LIVE=off)")
		<-ctx.Done()
		return nil
	}
	l := &live{api: apiURL, db: pool, log: log, rng: rand.New(rand.NewPCG(seed, 99)), pw: env("DEMO_PASSWORD", "Demo1234!"),
		http: &http.Client{Timeout: 10 * time.Second}, tokens: map[string]string{}}
	tick := envDur("SIM_TICK", 3*time.Second)
	log.Info("canlı olay üretimi başladı", "tick", tick.String())
	t := time.NewTicker(tick)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			log.Info("simülatör kapanıyor")
			return nil
		case <-t.C:
			l.step(ctx)
		}
	}
}

// ---- canlı mod ----

type live struct {
	api    string
	db     *pgxpool.Pool
	log    *slog.Logger
	rng    *rand.Rand
	pw     string
	http   *http.Client
	tokens map[string]string
	n      int
}

var groups = []string{"A+", "A+", "A+", "O+", "O+", "B+", "AB+", "A-", "O-", "O-", "B-", "AB-"}

func (l *live) step(ctx context.Context) {
	l.n++
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	// 1) Bağış: uygun görünen rastgele bir bağışçı
	if l.rng.Float64() < 0.7 {
		var id int64
		err := l.db.QueryRow(ctx, `SELECT d.id FROM donors d
			WHERE d.id >= (SELECT min(id) + floor(random() * (max(id) - min(id)))::bigint FROM donors)
			  AND NOT EXISTS (SELECT 1 FROM donations x WHERE x.donor_id=d.id AND x.collected_at > now() - interval '130 days')
			  AND NOT EXISTS (SELECT 1 FROM deferrals f WHERE f.donor_id=d.id AND (f.ends_on IS NULL OR f.ends_on > current_date))
			  AND d.birth_date BETWEEN current_date - interval '64 years' AND current_date - interval '19 years'
			ORDER BY d.id LIMIT 1`).Scan(&id)
		if err == nil {
			code, _ := l.post(ctx, "hemsire", "/api/v1/donations", map[string]any{
				"donor_id": id, "volume_ml": 450, "hemoglobin": 13.8 + l.rng.Float64()*2, "weight_kg": 60 + l.rng.Float64()*30,
			}, true)
			l.log.Info("sim: bağış", "donor_id", id, "status", code)
		}
	}
	// 2) Bilerek uygunsuz deneme: yakın zamanda bağış yapmış ya da ertelenmiş bağışçı / düşük Hb
	if l.rng.Float64() < 0.25 {
		var id int64
		err := l.db.QueryRow(ctx, `SELECT donor_id FROM donations WHERE collected_at > now() - interval '60 days'
			ORDER BY random() LIMIT 1`).Scan(&id)
		if err == nil {
			code, _ := l.post(ctx, "hemsire", "/api/v1/donations", map[string]any{
				"donor_id": id, "volume_ml": 450, "hemoglobin": 11.9, "weight_kg": 48}, true)
			l.log.Info("sim: bilerek uygunsuz bağış denemesi", "donor_id", id, "status", code, "expected", 422)
		}
	}
	// 3) Laboratuvar: bileşen üretimi ve test sonuçları
	l.each(ctx, `SELECT d.id FROM donations d WHERE NOT EXISTS (SELECT 1 FROM products p WHERE p.donation_id=d.id)
		AND d.collected_at > now() - interval '20 hours' ORDER BY d.id LIMIT 3`, func(id int64) {
		l.post(ctx, "lab", fmt.Sprintf("/api/v1/donations/%d/components", id), map[string]any{}, true)
	})
	l.each(ctx, `SELECT d.id FROM donations d WHERE NOT EXISTS (SELECT 1 FROM test_results t WHERE t.donation_id=d.id)
		AND d.collected_at < now() - interval '20 seconds' ORDER BY d.id LIMIT 4`, func(id int64) {
		pos := l.rng.Float64() < 0.01
		l.post(ctx, "lab", fmt.Sprintf("/api/v1/donations/%d/test-results", id), map[string]any{
			"hiv": false, "hbv": pos, "hcv": false, "syphilis": false}, true)
	})
	// 4) Hastane talepleri
	if l.rng.Float64() < 0.45 {
		who := "hastane"
		if l.rng.Float64() < 0.4 {
			who = "hastane2"
		}
		urg := "routine"
		if r := l.rng.Float64(); r < 0.05 {
			urg = "emergency"
		} else if r < 0.2 {
			urg = "urgent"
		}
		comp := "RBC"
		if r := l.rng.Float64(); r < 0.15 {
			comp = "PLT"
		} else if r < 0.3 {
			comp = "FFP"
		}
		code, _ := l.post(ctx, who, "/api/v1/requests", map[string]any{"component": comp, "group": groups[l.rng.IntN(len(groups))],
			"units": 1 + l.rng.IntN(3), "urgency": urg, "patient_ref": fmt.Sprintf("SIM-%d-%d", l.n, l.rng.IntN(100000))}, true)
		l.log.Info("sim: hastane talebi", "status", code, "urgency", urg)
	}
	// 5) Kan bankası: rezerve talepleri dağıt; hastane: transfüzyon kaydet
	l.each(ctx, `SELECT id FROM requests WHERE status IN ('reserved','partial') AND created_at < now() - interval '15 seconds'
		AND hospital_id IN (1,3) ORDER BY urgency DESC, id LIMIT 2`, func(id int64) {
		l.post(ctx, "bankasorumlusu", fmt.Sprintf("/api/v1/requests/%d/issue", id), map[string]any{}, true)
	})
	rows, err := l.db.Query(ctx, `SELECT p.product_no, d.hospital_id, q.patient_ref FROM distributions d
		JOIN products p ON p.id=d.product_id JOIN requests q ON q.id=d.request_id
		WHERE p.status='issued' AND d.hospital_id IN (1,3) AND d.issued_at < now() - interval '20 seconds' ORDER BY d.id LIMIT 2`)
	if err == nil {
		type tr struct {
			no, pat string
			h       int64
		}
		var list []tr
		for rows.Next() {
			var t tr
			if rows.Scan(&t.no, &t.h, &t.pat) == nil {
				list = append(list, t)
			}
		}
		rows.Close()
		for _, t := range list {
			who := "hastane"
			if t.h == 3 {
				who = "hastane2"
			}
			l.post(ctx, who, "/api/v1/transfusions", map[string]any{"product_no": t.no, "patient_ref": t.pat}, true)
		}
	}
}

func (l *live) each(ctx context.Context, sql string, fn func(int64)) {
	rows, err := l.db.Query(ctx, sql)
	if err != nil {
		return
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	rows.Close()
	for _, id := range ids {
		fn(id)
	}
}

func (l *live) token(ctx context.Context, who string) string {
	if t, ok := l.tokens[who]; ok {
		return t
	}
	b, _ := json.Marshal(map[string]string{"email": who + "@damarbagis.local", "password": l.pw})
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, l.api+"/auth/login", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	resp, err := l.http.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	var out struct {
		AccessToken string `json:"access_token"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&out)
	if out.AccessToken != "" {
		l.tokens[who] = out.AccessToken
	}
	return out.AccessToken
}

func (l *live) post(ctx context.Context, who, path string, body any, idem bool) (int, []byte) {
	for attempt := 0; attempt < 2; attempt++ {
		b, _ := json.Marshal(body)
		req, _ := http.NewRequestWithContext(ctx, http.MethodPost, l.api+path, bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+l.token(ctx, who))
		if idem {
			req.Header.Set("Idempotency-Key", fmt.Sprintf("sim-%d-%d-%d", time.Now().UnixNano(), l.n, l.rng.IntN(1e9)))
		}
		resp, err := l.http.Do(req)
		if err != nil {
			l.log.Warn("sim: API isteği başarısız", "path", path, "err", err.Error())
			return 0, nil
		}
		data, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode == http.StatusUnauthorized {
			delete(l.tokens, who)
			continue
		}
		return resp.StatusCode, data
	}
	return 401, nil
}

func waitHealthy(ctx context.Context, api string, max time.Duration) error {
	deadline := time.Now().Add(max)
	c := &http.Client{Timeout: 3 * time.Second}
	for {
		resp, err := c.Get(api + "/health")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == 200 {
				return nil
			}
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("API sağlıklı hale gelmedi: %s", api)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}

func today() time.Time {
	n := time.Now().UTC()
	return time.Date(n.Year(), n.Month(), n.Day(), 0, 0, 0, 0, time.UTC)
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func envU(k string, def uint64) uint64 {
	if v, err := strconv.ParseUint(os.Getenv(k), 10, 64); err == nil {
		return v
	}
	return def
}

func envDur(k string, def time.Duration) time.Duration {
	if v, err := time.ParseDuration(os.Getenv(k)); err == nil {
		return v
	}
	return def
}
