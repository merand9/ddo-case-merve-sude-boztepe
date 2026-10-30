// Komut verify ayağa kalkmış ve seed edilmiş sistem üzerinde tüm özellikleri ve zorlu görevleri
// API (ve gerektiğinde veritabanı) üzerinden uçtan uca dener, PASS/FAIL tablosu basar ve hata
// varsa sıfırdan farklı kodla çıkar.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	api    = env("API_URL", "http://localhost:8080")
	web    = env("WEB_URL", "http://localhost:3000")
	dbURL  = env("DATABASE_URL", "")
	pw     = env("DEMO_PASSWORD", "Demo1234!")
	client = &http.Client{Timeout: 20 * time.Second}
	run    = rand.Uint32() % 100000
)

type result struct {
	id, name string
	ok       bool
	note     string
	dur      time.Duration
}

type state struct {
	tok        map[string]string
	refresh    map[string]string
	db         *pgxpool.Pool
	patientRef string
	donorID    int64
}

func main() {
	ctx := context.Background()
	if err := waitHealthy(3 * time.Minute); err != nil {
		fmt.Println("API hazır değil:", err)
		os.Exit(2)
	}
	s := &state{tok: map[string]string{}, refresh: map[string]string{}}
	if dbURL != "" {
		if p, err := pgxpool.New(ctx, dbURL); err == nil {
			s.db = p
			defer p.Close()
		}
	}
	for _, u := range []string{"admin", "hemsire", "lab", "lab2", "bankasorumlusu", "hastane", "hastane2", "bagisci"} {
		if err := s.login(u); err != nil {
			fmt.Println("giriş başarısız:", u, err)
			os.Exit(2)
		}
	}
	checks := []struct {
		id, name string
		fn       func(*state) (string, error)
	}{
		{"HEALTH", "GET /health (veritabanı dahil)", checkHealth},
		{"AUTH-1", "Yanlış parola → 401", checkWrongPassword},
		{"AUTH-2", "Belirteçsiz istek → 401", checkNoToken},
		{"AUTH-3", "Yetkisiz rol → 403 (sunucu tarafı RBAC)", checkForbidden},
		{"AUTH-4", "Refresh token rotasyonu ve tekrar kullanım tespiti", checkRefresh},
		{"F-1", "Bağışçı kaydı ve uygunluk kontrolü", checkF1},
		{"F-2", "Randevu ve bağış kaydı (+ idempotans)", checkF2},
		{"F-3", "Test sonuçları ve karantina", checkF3},
		{"F-4", "Bileşen üretimi (RBC 42 / PLT 5 / FFP 365 gün)", checkF4},
		{"F-5", "Stok: kan grubu, Rh, SKT, keyset sayfalama", checkF5},
		{"F-6", "Hastane talepleri ve rezervasyon", checkF6},
		{"F-7", "Dağıtım, transfüzyon ve raporlar", checkF7},
		{"T-1", "Uygunluk kuralları (süre, ölçüm, erteleme, kalıcı red, zaman geçerliliği)", checkT1},
		{"T-2", "Bileşen geri izlenebilirliği (recursive, look-back, türev karantinası)", checkT2},
		{"T-3", "ABO/Rh uyumu, FEFO, eşzamanlı rezervasyon, süre dolumu, acil öncelik", checkT3},
		{"T-4", "Stok tahmini, MAPE backtest, alarm ve çağrı listesi", checkT4},
		{"T-5", "Mahremiyet ve bütünlük (şifreleme, gerekçe+denetim, çift onay, geri alınamaz kayıt)", checkT5},
		{"IDEM", "Idempotency-Key: aynı anahtar farklı gövde → 422", checkIdem},
		{"OBS", "Prometheus /metrics ve OpenAPI", checkObs},
		{"SIM", "Simülatör: 30.000 bağışçı seed + canlı olaylar", checkSim},
		{"WEB", "Ön yüz ve API vekili (nginx)", checkWeb},
	}
	var results []result
	failed := 0
	for _, c := range checks {
		t0 := time.Now()
		note, err := c.fn(s)
		r := result{id: c.id, name: c.name, ok: err == nil, note: note, dur: time.Since(t0)}
		if err != nil {
			r.note = err.Error()
			failed++
		}
		results = append(results, r)
		mark := "PASS"
		if !r.ok {
			mark = "FAIL"
		}
		fmt.Printf("  %-6s %-4s %s\n", c.id, mark, r.note)
	}
	fmt.Println()
	fmt.Println("| Kimlik | Sonuç | Süre | Özellik | Not |")
	fmt.Println("|---|---|---|---|---|")
	for _, r := range results {
		mark := "PASS"
		if !r.ok {
			mark = "**FAIL**"
		}
		fmt.Printf("| %s | %s | %dms | %s | %s |\n", r.id, mark, r.dur.Milliseconds(), r.name, strings.ReplaceAll(r.note, "|", "/"))
	}
	fmt.Printf("\nToplam: %d, başarılı: %d, başarısız: %d\n", len(results), len(results)-failed, failed)
	if failed > 0 {
		os.Exit(1)
	}
}

// ---------------- kontroller ----------------

func checkHealth(s *state) (string, error) {
	var h struct {
		Status string `json:"status"`
		Checks struct {
			Database struct {
				Status string `json:"status"`
				Schema string `json:"schema"`
			} `json:"database"`
		} `json:"checks"`
	}
	if err := s.call("", "GET", "/health", nil, 200, &h); err != nil {
		return "", err
	}
	if h.Status != "ok" || h.Checks.Database.Status != "ok" {
		return "", fmt.Errorf("sağlık durumu: %+v", h)
	}
	return "status=ok, db=ok, şema=" + h.Checks.Database.Schema, nil
}

func checkWrongPassword(s *state) (string, error) {
	var e apiErr
	if err := s.call("", "POST", "/auth/login", map[string]string{"email": "admin@damarbagis.local", "password": "yanlis"}, 401, &e); err != nil {
		return "", err
	}
	return "401 " + e.Error.Code, expect(e.Error.Code == "invalid_credentials", "kod: "+e.Error.Code)
}

func checkNoToken(s *state) (string, error) {
	var e apiErr
	if err := s.call("", "GET", "/api/v1/stock", nil, 401, &e); err != nil {
		return "", err
	}
	return "401 " + e.Error.Code, nil
}

func checkForbidden(s *state) (string, error) {
	cases := []struct{ who, method, path string }{
		{"hastane", "GET", "/api/v1/donors"},
		{"hastane", "GET", "/api/v1/products"},
		{"bagisci", "GET", "/api/v1/stock"},
		{"hemsire", "POST", "/api/v1/requests"},
		{"lab", "GET", "/api/v1/audit"},
		{"bankasorumlusu", "GET", "/api/v1/donors/1/health"},
		{"hastane", "GET", "/api/v1/trace/donors/1"},
	}
	for _, c := range cases {
		if err := s.call(c.who, c.method, c.path, map[string]any{}, 403, nil); err != nil {
			return "", fmt.Errorf("%s %s %s: %w", c.who, c.method, c.path, err)
		}
	}
	return fmt.Sprintf("%d yetkisiz rol/uç nokta denemesi 403 döndü", len(cases)), nil
}

func checkRefresh(s *state) (string, error) {
	var t struct {
		RefreshToken string `json:"refresh_token"`
	}
	old := s.refresh["admin"]
	if err := s.call("", "POST", "/auth/refresh", map[string]string{"refresh_token": old}, 200, &t); err != nil {
		return "", err
	}
	if err := s.call("", "POST", "/auth/refresh", map[string]string{"refresh_token": old}, 401, nil); err != nil {
		return "", fmt.Errorf("eski refresh token tekrar kullanılabildi: %w", err)
	}
	return "yeni belirteç alındı, eski belirteç reddedildi", s.login("admin")
}

func checkF1(s *state) (string, error) {
	d, err := s.newDonor("M", "A", true, "1990-03-04", nil)
	if err != nil {
		return "", err
	}
	s.donorID = d
	ok, err := s.elig(d, map[string]any{"measurements": map[string]any{"hemoglobin": 14.6, "weight_kg": 78}})
	if err != nil {
		return "", err
	}
	if !ok.Eligible {
		return "", fmt.Errorf("sağlıklı bağışçı uygun olmalıydı: %+v", ok)
	}
	bad, err := s.elig(d, map[string]any{"measurements": map[string]any{"hemoglobin": 11.2, "weight_kg": 45}})
	if err != nil {
		return "", err
	}
	if bad.Eligible || !bad.has("HB_TOO_LOW") || !bad.has("WEIGHT_TOO_LOW") {
		return "", fmt.Errorf("düşük Hb/kilo reddedilmeliydi: %+v", bad)
	}
	return fmt.Sprintf("bağışçı #%d kaydedildi; uygun=evet; düşük Hb+kilo → %d gerekçe", d, len(bad.Reasons)), nil
}

func checkF2(s *state) (string, error) {
	d := s.donorID
	when := time.Now().UTC().Add(26 * time.Hour).Truncate(time.Hour)
	var ap struct {
		ID     int64  `json:"id"`
		Status string `json:"status"`
	}
	if err := s.call("hemsire", "POST", "/api/v1/appointments", map[string]any{"donor_id": d, "scheduled_at": when}, 201, &ap); err != nil {
		return "", err
	}
	if err := s.call("hemsire", "POST", "/api/v1/appointments", map[string]any{"donor_id": d, "scheduled_at": when.Add(time.Hour)}, 409, nil); err != nil {
		return "", fmt.Errorf("ikinci açık randevu reddedilmeli: %w", err)
	}
	body := map[string]any{"donor_id": d, "appointment_id": ap.ID, "volume_ml": 450, "hemoglobin": 14.6, "weight_kg": 78}
	key := fmt.Sprintf("verify-f2-%d", run)
	var d1, d2 donation
	h1, err := s.callH("hemsire", "POST", "/api/v1/donations", body, 201, &d1, key)
	if err != nil {
		return "", err
	}
	h2, err := s.callH("hemsire", "POST", "/api/v1/donations", body, 201, &d2, key)
	if err != nil {
		return "", err
	}
	_ = h1
	if d1.ID != d2.ID || h2.Get("Idempotent-Replayed") != "true" {
		return "", fmt.Errorf("aynı Idempotency-Key aynı sonucu dönmeli: %d vs %d", d1.ID, d2.ID)
	}
	var e apiErr
	delete(body, "appointment_id")
	if err := s.call("hemsire", "POST", "/api/v1/donations", body, 422, &e); err != nil {
		return "", err
	}
	if e.Error.Code != "donor_ineligible" || !strings.Contains(string(e.Error.Details), "INTERVAL_NOT_ELAPSED") {
		return "", fmt.Errorf("ikinci bağış bekleme süresiyle reddedilmeli: %+v", e)
	}
	return fmt.Sprintf("randevu #%d → bağış #%d; tekrar istek aynı kaydı döndü; 2. bağış 422 INTERVAL_NOT_ELAPSED", ap.ID, d1.ID), nil
}

func checkF3(s *state) (string, error) {
	neg, err := s.donateProduce("M", "B", true)
	if err != nil {
		return "", err
	}
	var r testOut
	if err := s.call("lab", "POST", fmt.Sprintf("/api/v1/donations/%d/test-results", neg.donation), markers(false), 201, &r); err != nil {
		return "", err
	}
	if r.Outcome != "negative" || r.Released != 3 {
		return "", fmt.Errorf("negatif sonuçta 3 ürün serbest kalmalı: %+v", r)
	}
	pos, err := s.donateProduce("F", "O", true)
	if err != nil {
		return "", err
	}
	var rp testOut
	if err := s.call("lab", "POST", fmt.Sprintf("/api/v1/donations/%d/test-results", pos.donation), map[string]bool{"hiv": false, "hbv": true, "hcv": false, "syphilis": false}, 201, &rp); err != nil {
		return "", err
	}
	if rp.Outcome != "positive" || len(rp.Quarantined) != 3 {
		return "", fmt.Errorf("pozitif sonuçta 3 ürün karantinaya alınmalı: %+v", rp)
	}
	if err := s.call("lab", "POST", fmt.Sprintf("/api/v1/donations/%d/test-results", pos.donation), markers(false), 409, nil); err != nil {
		return "", fmt.Errorf("ikinci test girişi reddedilmeli: %w", err)
	}
	dec, err := s.elig(pos.donor, map[string]any{})
	if err != nil {
		return "", err
	}
	if dec.Eligible || !dec.has("PERMANENTLY_DEFERRED") {
		return "", fmt.Errorf("pozitif bağışçı kalıcı reddedilmeli: %+v", dec)
	}
	return fmt.Sprintf("negatif → %d ürün stokta; pozitif → %d ürün karantina, bağışçı kalıcı red; 2. giriş 409", r.Released, len(rp.Quarantined)), nil
}

func checkF4(s *state) (string, error) {
	p, err := s.donateProduce("M", "AB", true)
	if err != nil {
		return "", err
	}
	var don donation
	if err := s.call("lab", "GET", fmt.Sprintf("/api/v1/donations/%d", p.donation), nil, 200, &don); err != nil {
		return "", err
	}
	want := map[string]int{"RBC": 42, "PLT": 5, "FFP": 365}
	for _, pr := range p.products {
		days := int(pr.ExpiresAt.Sub(don.CollectedAt).Hours()/24 + 0.5)
		if want[pr.Component] != days {
			return "", fmt.Errorf("%s SKT %d gün, beklenen %d", pr.Component, days, want[pr.Component])
		}
		if pr.Status != "pending_test" {
			return "", fmt.Errorf("test öncesi ürün pending_test olmalı: %s", pr.Status)
		}
		delete(want, pr.Component)
	}
	if len(want) != 0 {
		return "", fmt.Errorf("eksik bileşen: %v", want)
	}
	if err := s.call("lab", "POST", fmt.Sprintf("/api/v1/donations/%d/components", p.donation), map[string]any{}, 409, nil); err != nil {
		return "", fmt.Errorf("aynı bağıştan ikinci üretim reddedilmeli: %w", err)
	}
	return "tam kan → RBC(42g) + PLT(5g) + FFP(365g), pending_test; tekrar üretim 409", nil
}

func checkF5(s *state) (string, error) {
	var stock []struct {
		Component string `json:"component"`
		Group     string `json:"group"`
		Available int    `json:"available"`
	}
	if err := s.call("bankasorumlusu", "GET", "/api/v1/stock", nil, 200, &stock); err != nil {
		return "", err
	}
	if len(stock) != 24 {
		return "", fmt.Errorf("stok matrisi 3×8=24 satır olmalı: %d", len(stock))
	}
	total := 0
	for _, r := range stock {
		total += r.Available
	}
	var p1, p2 struct {
		Items      []product `json:"items"`
		NextCursor *int64    `json:"next_cursor"`
	}
	q := "/api/v1/products?status=available&component=RBC&group=" + url.QueryEscape("A+") + "&limit=5"
	if err := s.call("bankasorumlusu", "GET", q, nil, 200, &p1); err != nil {
		return "", err
	}
	if len(p1.Items) != 5 || p1.NextCursor == nil {
		return "", fmt.Errorf("ilk sayfada 5 kayıt ve cursor bekleniyordu")
	}
	for _, p := range p1.Items {
		if p.Component != "RBC" || p.ABO != "A" || !p.RhPositive || p.Status != "available" {
			return "", fmt.Errorf("filtre dışı ürün: %+v", p)
		}
	}
	if err := s.call("bankasorumlusu", "GET", fmt.Sprintf("%s&cursor=%d", q, *p1.NextCursor), nil, 200, &p2); err != nil {
		return "", err
	}
	if len(p2.Items) == 0 || p2.Items[0].ID >= p1.Items[4].ID {
		return "", fmt.Errorf("keyset sayfa 2, sayfa 1'den küçük kimliklerle başlamalı")
	}
	var fefo struct {
		Items []product `json:"items"`
	}
	if err := s.call("bankasorumlusu", "GET", "/api/v1/products?status=available&component=RBC&sort=expiry&limit=20", nil, 200, &fefo); err != nil {
		return "", err
	}
	for i := 1; i < len(fefo.Items); i++ {
		if fefo.Items[i].ExpiresAt.Before(fefo.Items[i-1].ExpiresAt) {
			return "", fmt.Errorf("SKT sıralaması bozuk")
		}
	}
	return fmt.Sprintf("24 hücre, %d kullanılabilir ünite; grup/Rh/bileşen filtresi, keyset ve SKT sıralaması doğru", total), nil
}

func checkF6(s *state) (string, error) {
	raw, req, err := s.request("hastane", "RBC", "A+", 2, "urgent")
	if err != nil {
		return "", err
	}
	if req.Status != "reserved" || len(req.Reservations) != 2 {
		return "", fmt.Errorf("2 ünite rezerve edilmeli: %s %d", req.Status, len(req.Reservations))
	}
	for _, k := range []string{"donor", "donation_id", "donor_id", "first_name", "birth_date"} {
		if strings.Contains(raw, `"`+k+`"`) {
			return "", fmt.Errorf("hastane yanıtında bağışçı alanı var: %s", k)
		}
	}
	if err := s.call("hastane2", "GET", fmt.Sprintf("/api/v1/requests/%d", req.ID), nil, 404, nil); err != nil {
		return "", fmt.Errorf("başka hastane talebi görmemeli: %w", err)
	}
	return fmt.Sprintf("talep #%d: 2/2 rezerve, bağışçı alanı yok, diğer hastane 404", req.ID), nil
}

func checkF7(s *state) (string, error) {
	_, req, err := s.request("hastane", "RBC", "O+", 1, "routine")
	if err != nil {
		return "", err
	}
	if req.Status != "reserved" {
		return "", fmt.Errorf("talep rezerve olmalı: %s", req.Status)
	}
	var issued request
	if err := s.call("bankasorumlusu", "POST", fmt.Sprintf("/api/v1/requests/%d/issue", req.ID), map[string]any{}, 200, &issued); err != nil {
		return "", err
	}
	if issued.Status != "issued" {
		return "", fmt.Errorf("talep issued olmalı: %s", issued.Status)
	}
	s.patientRef = req.PatientRef
	no := issued.Reservations[0].ProductNo
	if err := s.call("hastane", "POST", "/api/v1/transfusions", map[string]string{"product_no": no, "patient_ref": req.PatientRef}, 201, nil); err != nil {
		return "", err
	}
	if err := s.call("hastane2", "POST", "/api/v1/transfusions", map[string]string{"product_no": no, "patient_ref": "X-1"}, 404, nil); err != nil {
		return "", fmt.Errorf("başka hastanenin ürünü kaydedilmemeli: %w", err)
	}
	var rep struct {
		Issued     map[string]int `json:"issued"`
		ByHospital []any          `json:"issued_by_hospital"`
	}
	if err := s.call("bankasorumlusu", "GET", "/api/v1/reports/summary", nil, 200, &rep); err != nil {
		return "", err
	}
	if rep.Issued["RBC"] == 0 || len(rep.ByHospital) == 0 {
		return "", fmt.Errorf("raporda dağıtım görünmeli: %+v", rep)
	}
	return fmt.Sprintf("talep #%d dağıtıldı, %s transfüze edildi; 30 günlük raporda %d RBC dağıtımı", req.ID, no, rep.Issued["RBC"]), nil
}

func checkT1(s *state) (string, error) {
	notes := []string{}
	// Kadın 120 gün / erkek 90 gün
	f, err := s.newDonor("F", "B", true, "1992-07-01", nil)
	if err != nil {
		return "", err
	}
	m, err := s.newDonor("M", "B", true, "1988-02-01", nil)
	if err != nil {
		return "", err
	}
	past := time.Now().UTC().AddDate(0, 0, -100)
	for _, d := range []int64{f, m} {
		if err := s.call("hemsire", "POST", "/api/v1/donations", map[string]any{"donor_id": d, "volume_ml": 450, "hemoglobin": 14, "weight_kg": 70, "collected_at": past}, 201, nil); err != nil {
			return "", err
		}
	}
	df, err := s.elig(f, map[string]any{})
	if err != nil {
		return "", err
	}
	dm, err := s.elig(m, map[string]any{})
	if err != nil {
		return "", err
	}
	if df.Eligible || !df.has("INTERVAL_NOT_ELAPSED") || !dm.Eligible {
		return "", fmt.Errorf("100 gün: kadın uygun değil, erkek uygun olmalı: F=%+v M=%+v", df, dm)
	}
	notes = append(notes, "100. gün: K red (120g) / E uygun (90g)")
	// Trombosit aferezi 14 gün
	p, err := s.newDonor("M", "A", false, "1985-01-01", nil)
	if err != nil {
		return "", err
	}
	if err := s.call("hemsire", "POST", "/api/v1/donations", map[string]any{"donor_id": p, "donation_type": "platelet_apheresis", "volume_ml": 250, "hemoglobin": 14, "weight_kg": 80, "collected_at": time.Now().UTC().AddDate(0, 0, -10)}, 201, nil); err != nil {
		return "", err
	}
	d10, _ := s.elig(p, map[string]any{"donation_type": "platelet_apheresis"})
	d15, _ := s.elig(p, map[string]any{"donation_type": "platelet_apheresis", "at": time.Now().UTC().AddDate(0, 0, 4).Format("2006-01-02")})
	if d10.Eligible || !d15.Eligible {
		return "", fmt.Errorf("aferez 14 gün kuralı: 10.gün=%v 14.gün=%v", d10.Eligible, d15.Eligible)
	}
	notes = append(notes, "aferez 10.gün red / 14.gün uygun")
	// Seyahat ertelemesi (beyan) ve kalıcı red (kayıt)
	trav, _ := s.elig(m, map[string]any{"declarations": []map[string]string{{"code": "travel_malaria", "date": time.Now().UTC().AddDate(0, -2, 0).Format("2006-01-02")}}})
	if trav.Eligible || !trav.has("DEFERRED") || trav.NextEligibleAt == nil {
		return "", fmt.Errorf("seyahat ertelemesi tarihli red vermeli: %+v", trav)
	}
	if err := s.call("hemsire", "POST", fmt.Sprintf("/api/v1/donors/%d/deferrals", m), map[string]string{"reason_code": "iv_drug_use"}, 201, nil); err != nil {
		return "", err
	}
	perm, _ := s.elig(m, map[string]any{})
	if perm.Eligible || !perm.has("PERMANENTLY_DEFERRED") || perm.NextEligibleAt != nil {
		return "", fmt.Errorf("kalıcı red bekleniyordu: %+v", perm)
	}
	notes = append(notes, "seyahat → "+perm2(trav), "kalıcı red tarihsiz")
	// Zaman geçerliliği: ileri tarihli yeni sürüm (max_age=20) yalnızca o tarihten itibaren uygulanır.
	var rules []struct {
		ValidFrom string          `json:"valid_from"`
		Params    json.RawMessage `json:"params"`
	}
	if err := s.call("admin", "GET", "/api/v1/rules?code=eligibility", nil, 200, &rules); err != nil {
		return "", err
	}
	var params map[string]any
	_ = json.Unmarshal(rules[len(rules)-1].Params, &params)
	params["max_age"] = 20
	from := time.Now().UTC().AddDate(0, 0, 3000+int(run%2000)).Format("2006-01-02")
	if err := s.call("admin", "POST", "/api/v1/rules", map[string]any{"code": "eligibility", "valid_from": from, "note": "verify", "params": params}, 201, nil); err != nil {
		return "", err
	}
	before, _ := s.elig(f, map[string]any{"at": time.Now().UTC().AddDate(0, 0, 30).Format("2006-01-02")})
	after, _ := s.elig(f, map[string]any{"at": from})
	if !before.Eligible || after.Eligible || !after.has("AGE_TOO_OLD") || after.RuleVersion != from {
		return "", fmt.Errorf("zaman geçerliliği: önce=%+v sonra=%+v", before, after)
	}
	notes = append(notes, "kural sürümü "+from+" öncesi eski, sonrası yeni kural")
	return strings.Join(notes, "; "), nil
}

func perm2(d decision) string {
	if d.NextEligibleAt == nil {
		return "?"
	}
	return d.NextEligibleAt.Format("2006-01-02") + "'e kadar"
}

func checkT2(s *state) (string, error) {
	// Bağış → üretim → negatif → RBC bölme → çift onaylı düzeltme ile pozitif → alt ürünler karantinada.
	p, err := s.donateProduce("M", "A", false)
	if err != nil {
		return "", err
	}
	if err := s.call("lab", "POST", fmt.Sprintf("/api/v1/donations/%d/test-results", p.donation), markers(false), 201, nil); err != nil {
		return "", err
	}
	var rbc int64
	for _, pr := range p.products {
		if pr.Component == "RBC" {
			rbc = pr.ID
		}
	}
	var kids []product
	if err := s.call("bankasorumlusu", "POST", fmt.Sprintf("/api/v1/products/%d/split", rbc), map[string]int{"parts": 2}, 201, &kids); err != nil {
		return "", err
	}
	var tc struct {
		ID int64 `json:"id"`
	}
	if err := s.call("lab", "POST", fmt.Sprintf("/api/v1/donations/%d/test-changes", p.donation),
		map[string]any{"proposed": map[string]bool{"hiv": false, "hbv": false, "hcv": true, "syphilis": false}, "reason": "Doğrulama testi reaktif"}, 201, &tc); err != nil {
		return "", err
	}
	var dec struct {
		Effect testOut `json:"effect"`
	}
	if err := s.call("lab2", "POST", fmt.Sprintf("/api/v1/test-changes/%d/approve", tc.ID), map[string]any{}, 200, &dec); err != nil {
		return "", err
	}
	depth1 := 0
	for _, q := range dec.Effect.Quarantined {
		if q.Depth == 1 {
			depth1++
		}
	}
	if depth1 != 2 {
		return "", fmt.Errorf("bölünmüş 2 alt ürün recursive bulunup karantinaya alınmalı: %+v", dec.Effect)
	}
	var kid struct {
		Product product `json:"product"`
	}
	if err := s.call("bankasorumlusu", "GET", fmt.Sprintf("/api/v1/products/%d", kids[0].ID), nil, 200, &kid); err != nil {
		return "", err
	}
	if kid.Product.Status != "quarantined" {
		return "", fmt.Errorf("alt ürün karantinada olmalı: %s", kid.Product.Status)
	}
	// Hasta → bağışçı (F-7'de transfüze edilen hasta) ve bağışçı → hasta
	if s.patientRef == "" {
		return "", fmt.Errorf("F-7 hasta referansı yok")
	}
	var back struct {
		Links []struct {
			DonorID int64  `json:"donor_id"`
			DonorNo string `json:"donor_no"`
			Hops    int    `json:"hops"`
		} `json:"links"`
	}
	if err := s.call("bankasorumlusu", "GET", "/api/v1/trace/patients/"+url.PathEscape(s.patientRef), nil, 200, &back); err != nil {
		return "", err
	}
	if len(back.Links) == 0 {
		return "", fmt.Errorf("hastadan bağışçıya bağlantı bulunamadı")
	}
	var fw struct {
		Patients []string `json:"patients"`
	}
	if err := s.call("bankasorumlusu", "GET", fmt.Sprintf("/api/v1/trace/donors/%d", back.Links[0].DonorID), nil, 200, &fw); err != nil {
		return "", err
	}
	found := false
	for _, x := range fw.Patients {
		found = found || x == s.patientRef
	}
	if !found {
		return "", fmt.Errorf("bağışçıdan hastaya izleme hastayı bulmalı")
	}
	return fmt.Sprintf("bölünmüş 2 alt ürün (depth=1) karantinada; hasta %s → bağışçı %s → hasta", s.patientRef, back.Links[0].DonorNo), nil
}

func checkT3(s *state) (string, error) {
	notes := []string{}
	var c struct {
		Candidates []struct {
			Group string `json:"group"`
		} `json:"candidates"`
	}
	if err := s.call("hastane", "GET", "/api/v1/compat?component=RBC&group="+url.QueryEscape("A+"), nil, 200, &c); err != nil {
		return "", err
	}
	var order []string
	for _, x := range c.Candidates {
		order = append(order, x.Group)
	}
	if strings.Join(order, ",") != "A+,A-,O+,O-" {
		return "", fmt.Errorf("A+ için uyum sırası: %v", order)
	}
	notes = append(notes, "A+ sırası "+strings.Join(order, ">"))
	// FEFO + tam uyum
	var avail struct {
		Items []product `json:"items"`
	}
	if err := s.call("bankasorumlusu", "GET", "/api/v1/products?status=available&component=RBC&group="+url.QueryEscape("A+")+"&sort=expiry&limit=1", nil, 200, &avail); err != nil {
		return "", err
	}
	_, req, err := s.request("hastane", "RBC", "A+", 1, "routine")
	if err != nil {
		return "", err
	}
	if len(req.Reservations) != 1 || !req.Reservations[0].Exact {
		return "", fmt.Errorf("tam uyumlu rezervasyon bekleniyordu: %+v", req.Reservations)
	}
	if len(avail.Items) > 0 && req.Reservations[0].ProductExp.After(avail.Items[0].ExpiresAt.Add(time.Second)) {
		return "", fmt.Errorf("FEFO ihlali: seçilen %v, en erken %v", req.Reservations[0].ProductExp, avail.Items[0].ExpiresAt)
	}
	notes = append(notes, "tam uyum + en erken SKT seçildi")
	// Eşzamanlı rezervasyon: 15 paralel talep; hiçbir ürün iki aktif rezervasyonda olamaz.
	var wg sync.WaitGroup
	var mu sync.Mutex
	statuses := map[int]int{}
	for i := 0; i < 15; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			who := "hastane"
			if i%2 == 1 {
				who = "hastane2"
			}
			code, _ := s.raw(who, "POST", "/api/v1/requests", map[string]any{"component": "PLT", "group": "A-", "units": 1, "urgency": "routine",
				"patient_ref": fmt.Sprintf("YARIS-%d-%d", run, i)}, fmt.Sprintf("race-%d-%d", run, i))
			mu.Lock()
			statuses[code]++
			mu.Unlock()
		}(i)
	}
	wg.Wait()
	var active []struct {
		ProductID int64 `json:"product_id"`
	}
	if err := s.call("bankasorumlusu", "GET", "/api/v1/reservations", nil, 200, &active); err != nil {
		return "", err
	}
	seen := map[int64]bool{}
	for _, a := range active {
		if seen[a.ProductID] {
			return "", fmt.Errorf("ürün %d iki aktif rezervasyonda", a.ProductID)
		}
		seen[a.ProductID] = true
	}
	if statuses[201] != 15 {
		return "", fmt.Errorf("15 talep oluşturulmalıydı: %v", statuses)
	}
	notes = append(notes, fmt.Sprintf("15 eşzamanlı talep, %d aktif rezervasyonda çift ürün yok", len(active)))
	// Rezervasyon süresi dolunca otomatik serbest kalma
	_, r2, err := s.request("hastane", "FFP", "A+", 1, "routine")
	if err != nil {
		return "", err
	}
	if len(r2.Reservations) == 0 {
		return "", fmt.Errorf("FFP rezervasyonu bekleniyordu")
	}
	if err := s.call("bankasorumlusu", "POST", fmt.Sprintf("/api/v1/reservations/%d/release", r2.Reservations[0].ID), map[string]any{}, 200, nil); err != nil {
		return "", err
	}
	var al struct {
		Reserved []int64 `json:"reserved_product_ids"`
	}
	if err := s.call("bankasorumlusu", "POST", fmt.Sprintf("/api/v1/requests/%d/allocate", r2.ID), map[string]int{"ttl_seconds": 2}, 200, &al); err != nil {
		return "", err
	}
	var got request
	_ = s.call("bankasorumlusu", "GET", fmt.Sprintf("/api/v1/requests/%d", r2.ID), nil, 200, &got)
	var resID int64
	for _, r := range got.Reservations {
		if r.Status == "active" {
			resID = r.ID
		}
	}
	if resID == 0 {
		return "", fmt.Errorf("kısa süreli rezervasyon oluşturulamadı")
	}
	released := false
	for i := 0; i < 20 && !released; i++ {
		time.Sleep(time.Second)
		var rs struct {
			Status string `json:"status"`
		}
		_ = s.call("bankasorumlusu", "GET", fmt.Sprintf("/api/v1/reservations/%d", resID), nil, 200, &rs)
		released = rs.Status == "released"
	}
	if !released {
		return "", fmt.Errorf("2 sn'lik rezervasyon 20 sn içinde serbest kalmadı")
	}
	notes = append(notes, "2 sn TTL rezervasyon otomatik serbest kaldı")
	// Acil öncelik: çok acil talep kuyruğun önünde ve anında karşılanır
	_, em, err := s.request("hastane2", "RBC", "O+", 2, "emergency")
	if err != nil {
		return "", err
	}
	if em.Status != "reserved" {
		return "", fmt.Errorf("çok acil talep anında rezerve edilmeli: %s", em.Status)
	}
	notes = append(notes, "çok acil talep anında rezerve (rutin rezervasyonu devralma: TestEmergencyPreemptsRoutine)")
	return strings.Join(notes, "; "), nil
}

func checkT4(s *state) (string, error) {
	var fc struct {
		Groups []struct {
			Group        string    `json:"group"`
			Forecast     []float64 `json:"forecast"`
			DaysOfSupply float64   `json:"days_of_supply"`
			Expiry       int       `json:"expected_expiry_loss"`
			Level        string    `json:"level"`
		} `json:"groups"`
	}
	if err := s.call("bankasorumlusu", "GET", "/api/v1/forecast?component=RBC&horizon=7", nil, 200, &fc); err != nil {
		return "", err
	}
	if len(fc.Groups) != 8 || len(fc.Groups[0].Forecast) != 7 {
		return "", fmt.Errorf("8 grup × 7 gün tahmin bekleniyordu")
	}
	var bt struct {
		Total struct {
			MAPE float64 `json:"mape"`
			WAPE float64 `json:"wape"`
			Days int     `json:"days"`
		} `json:"total"`
	}
	if err := s.call("bankasorumlusu", "GET", "/api/v1/forecast/backtest?component=RBC&days=28", nil, 200, &bt); err != nil {
		return "", err
	}
	if bt.Total.Days != 28 || bt.Total.MAPE <= 0 || bt.Total.MAPE > 50 {
		return "", fmt.Errorf("MAPE makul aralıkta olmalı: %+v", bt.Total)
	}
	var alerts []struct {
		Group string `json:"group"`
		Level string `json:"level"`
	}
	if err := s.call("bankasorumlusu", "POST", "/api/v1/alerts/evaluate", map[string]any{}, 200, &alerts); err != nil {
		return "", err
	}
	group := "O-"
	if len(alerts) > 0 {
		group = alerts[0].Group
	}
	var cl map[string][]struct {
		DonorID int64 `json:"donor_id"`
	}
	if err := s.call("bankasorumlusu", "GET", "/api/v1/call-list?limit=10&group="+url.QueryEscape(group), nil, 200, &cl); err != nil {
		return "", err
	}
	list := cl[group]
	if len(list) == 0 {
		return "", fmt.Errorf("%s için çağrı listesi boş", group)
	}
	for i, c := range list {
		if i >= 3 {
			break
		}
		d, err := s.elig(c.DonorID, map[string]any{})
		if err != nil || !d.Eligible {
			return "", fmt.Errorf("çağrı listesindeki bağışçı %d uygun değil: %+v", c.DonorID, d)
		}
	}
	return fmt.Sprintf("8 grup × 7 gün tahmin; backtest MAPE=%.2f%% WAPE=%.2f%%; %d alarm; %s çağrı listesi %d kişi (ilk 3 uygunluk teyitli)",
		bt.Total.MAPE, bt.Total.WAPE, len(alerts), group, len(list)), nil
}

func checkT5(s *state) (string, error) {
	secret := fmt.Sprintf("gizli-ilac-%d", run)
	d, err := s.newDonor("F", "AB", false, "1995-09-09", []string{secret})
	if err != nil {
		return "", err
	}
	notes := []string{}
	if s.db != nil {
		var pos int
		if err := s.db.QueryRow(context.Background(), `SELECT position(convert_to($1,'UTF8') in health_enc) FROM donors WHERE id=$2`, secret, d).Scan(&pos); err != nil {
			return "", err
		}
		if pos != 0 {
			return "", fmt.Errorf("sağlık verisi düz metin saklanıyor")
		}
		notes = append(notes, "DB'de şifreli")
	}
	var e apiErr
	if err := s.call("hemsire", "GET", fmt.Sprintf("/api/v1/donors/%d/health", d), nil, 400, &e); err != nil {
		return "", err
	}
	if e.Error.Code != "access_reason_required" {
		return "", fmt.Errorf("gerekçesiz erişim kodu: %s", e.Error.Code)
	}
	reason := fmt.Sprintf("Muayene öncesi ilaç kontrolü %d", run)
	var h struct {
		Medications []string `json:"medications"`
	}
	if err := s.callHdr("hemsire", "GET", fmt.Sprintf("/api/v1/donors/%d/health", d), nil, 200, &h, map[string]string{"X-Access-Reason": reason}); err != nil {
		return "", err
	}
	if len(h.Medications) != 1 || h.Medications[0] != secret {
		return "", fmt.Errorf("şifre çözümü hatalı")
	}
	var au struct {
		Items []struct {
			Reason string `json:"reason"`
		} `json:"items"`
	}
	if err := s.call("admin", "GET", fmt.Sprintf("/api/v1/audit?action=donor.health.read&entity_id=%d", d), nil, 200, &au); err != nil {
		return "", err
	}
	if len(au.Items) == 0 || au.Items[0].Reason != reason {
		return "", fmt.Errorf("erişim gerekçesiyle denetime yazılmamış")
	}
	notes = append(notes, "gerekçesiz 400, gerekçeli okuma denetimde")
	// Çift onay: aynı kişi onaylayamaz
	p, err := s.donateProduce("M", "O", true)
	if err != nil {
		return "", err
	}
	if err := s.call("lab", "POST", fmt.Sprintf("/api/v1/donations/%d/test-results", p.donation), markers(false), 201, nil); err != nil {
		return "", err
	}
	var tc struct {
		ID int64 `json:"id"`
	}
	if err := s.call("lab", "POST", fmt.Sprintf("/api/v1/donations/%d/test-changes", p.donation),
		map[string]any{"proposed": map[string]bool{"hiv": false, "hbv": false, "hcv": false, "syphilis": true}, "reason": "Kayıt hatası düzeltmesi"}, 201, &tc); err != nil {
		return "", err
	}
	if err := s.call("lab", "POST", fmt.Sprintf("/api/v1/test-changes/%d/approve", tc.ID), map[string]any{}, 403, &e); err != nil {
		return "", err
	}
	if err := s.call("lab2", "POST", fmt.Sprintf("/api/v1/test-changes/%d/reject", tc.ID), map[string]any{}, 200, nil); err != nil {
		return "", err
	}
	notes = append(notes, "kendi talebini onaylama 403, ikinci uzman karar verdi")
	// Geri alınamaz geçiş: transfüze ürün stoğa döndürülemez
	var hp []struct {
		ID     int64  `json:"id"`
		Status string `json:"status"`
	}
	if err := s.call("hastane", "GET", "/api/v1/hospital/products", nil, 200, &hp); err != nil {
		return "", err
	}
	var tid int64
	for _, x := range hp {
		if x.Status == "transfused" {
			tid = x.ID
			break
		}
	}
	if tid == 0 {
		return "", fmt.Errorf("transfüze ürün bulunamadı")
	}
	if err := s.call("bankasorumlusu", "POST", fmt.Sprintf("/api/v1/products/%d/transition", tid), map[string]string{"to": "available", "reason": "geri alma denemesi"}, 409, &e); err != nil {
		return "", err
	}
	if e.Error.Code != "invalid_transition" {
		return "", fmt.Errorf("geçersiz geçiş kodu: %s", e.Error.Code)
	}
	notes = append(notes, "transfused→available 409")
	if s.db != nil {
		_, err := s.db.Exec(context.Background(), `UPDATE product_events SET reason='x' WHERE id=(SELECT max(id) FROM product_events)`)
		_, err2 := s.db.Exec(context.Background(), `DELETE FROM audit_log WHERE id=(SELECT max(id) FROM audit_log)`)
		if err == nil || err2 == nil {
			return "", fmt.Errorf("olay/denetim kaydı değiştirilebildi")
		}
		notes = append(notes, "product_events/audit_log UPDATE/DELETE reddedildi")
	}
	return strings.Join(notes, "; "), nil
}

func checkIdem(s *state) (string, error) {
	key := fmt.Sprintf("verify-idem-%d", run)
	body := map[string]any{"component": "FFP", "group": "B+", "units": 1, "patient_ref": fmt.Sprintf("IDEM-%d", run)}
	var r1, r2 request
	if _, err := s.callH("hastane", "POST", "/api/v1/requests", body, 201, &r1, key); err != nil {
		return "", err
	}
	if _, err := s.callH("hastane", "POST", "/api/v1/requests", body, 201, &r2, key); err != nil {
		return "", err
	}
	if r1.ID != r2.ID {
		return "", fmt.Errorf("aynı anahtar iki talep oluşturdu: %d %d", r1.ID, r2.ID)
	}
	body["units"] = 2
	var e apiErr
	if _, err := s.callH("hastane", "POST", "/api/v1/requests", body, 422, &e, key); err != nil {
		return "", err
	}
	return fmt.Sprintf("talep #%d tekrar isteğinde aynı döndü; farklı gövde 422 %s", r1.ID, e.Error.Code), nil
}

func checkObs(s *state) (string, error) {
	_, body := s.raw("", "GET", "/metrics", nil, "")
	for _, want := range []string{"http_requests_total", "http_request_duration_seconds_bucket", "damar_stock_available_units"} {
		if !strings.Contains(body, want) {
			return "", fmt.Errorf("/metrics içinde yok: %s", want)
		}
	}
	code, spec := s.raw("", "GET", "/openapi.yaml", nil, "")
	if code != 200 || !strings.Contains(spec, "openapi:") || !strings.Contains(spec, "/api/v1/requests") {
		return "", fmt.Errorf("OpenAPI dokümanı eksik")
	}
	return "metrikler ve OpenAPI 3 dokümanı yayında", nil
}

func checkSim(s *state) (string, error) {
	if s.db == nil {
		return "", fmt.Errorf("DATABASE_URL gerekli")
	}
	ctx := context.Background()
	var donors, seeds int
	var fp string
	if err := s.db.QueryRow(ctx, `SELECT (SELECT count(*) FROM donors), (SELECT count(*) FROM audit_log WHERE action='sim.seed'),
		coalesce((SELECT new_value->>'fingerprint' FROM audit_log WHERE action='sim.seed' LIMIT 1),'')`).Scan(&donors, &seeds, &fp); err != nil {
		return "", err
	}
	if donors < 30000 || seeds != 1 {
		return "", fmt.Errorf("seed eksik: %d bağışçı, %d seed kaydı", donors, seeds)
	}
	var recent int
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		_ = s.db.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE actor_id IS NOT NULL AND at > now() - interval '2 minutes'
			AND action IN ('donation.create','request.create','test_result.create')`).Scan(&recent)
		if recent > 0 {
			break
		}
		time.Sleep(2 * time.Second)
	}
	if recent == 0 {
		return "", fmt.Errorf("son 2 dakikada canlı olay yok")
	}
	var rej int
	_ = s.db.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE action='eligibility.rejected'`).Scan(&rej)
	return fmt.Sprintf("%d bağışçı, seed parmak izi %s; son 2 dk %d canlı olay; toplam %d uygunsuz bağış denemesi reddedildi", donors, fp, recent, rej), nil
}

func checkWeb(s *state) (string, error) {
	resp, err := client.Get(web + "/")
	if err != nil {
		return "", err
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(string(b), "DamarBağış") {
		return "", fmt.Errorf("ön yüz açılmadı: %d", resp.StatusCode)
	}
	resp, err = client.Get(web + "/health")
	if err != nil {
		return "", err
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("nginx → API vekili çalışmıyor: %d", resp.StatusCode)
	}
	resp, err = client.Get(web + "/app.js")
	if err != nil {
		return "", err
	}
	resp.Body.Close()
	return "index, statik dosyalar ve /health vekili 200", expect(resp.StatusCode == 200, "app.js yok")
}

// ---------------- yardımcılar ----------------

type apiErr struct {
	Error struct {
		Code    string          `json:"code"`
		Message string          `json:"message"`
		Details json.RawMessage `json:"details"`
	} `json:"error"`
}

type decision struct {
	Eligible       bool       `json:"eligible"`
	NextEligibleAt *time.Time `json:"next_eligible_at"`
	RuleVersion    string     `json:"rule_version"`
	Reasons        []struct {
		Code string `json:"code"`
	} `json:"reasons"`
}

func (d decision) has(code string) bool {
	for _, r := range d.Reasons {
		if r.Code == code {
			return true
		}
	}
	return false
}

type donation struct {
	ID          int64     `json:"id"`
	CollectedAt time.Time `json:"collected_at"`
}

type product struct {
	ID         int64     `json:"id"`
	ProductNo  string    `json:"product_no"`
	Component  string    `json:"component"`
	ABO        string    `json:"abo"`
	RhPositive bool      `json:"rh_positive"`
	ExpiresAt  time.Time `json:"expires_at"`
	Status     string    `json:"status"`
}

type request struct {
	ID           int64  `json:"id"`
	Status       string `json:"status"`
	PatientRef   string `json:"patient_ref"`
	Reservations []struct {
		ID         int64     `json:"id"`
		ProductNo  string    `json:"product_no"`
		Status     string    `json:"status"`
		Exact      bool      `json:"exact_match"`
		ProductExp time.Time `json:"product_expires_at"`
	} `json:"reservations"`
}

type testOut struct {
	Outcome     string `json:"outcome"`
	Released    int    `json:"released"`
	Quarantined []struct {
		Depth int `json:"depth"`
	} `json:"quarantined"`
}

type produced struct {
	donor, donation int64
	products        []product
}

func markers(pos bool) map[string]bool {
	return map[string]bool{"hiv": pos, "hbv": false, "hcv": false, "syphilis": false}
}

func (s *state) newDonor(sex, abo string, rh bool, birth string, meds []string) (int64, error) {
	if meds == nil {
		meds = []string{}
	}
	var d struct {
		ID int64 `json:"id"`
	}
	err := s.call("hemsire", "POST", "/api/v1/donors", map[string]any{"first_name": "Doğrulama", "last_name": fmt.Sprintf("Kişi%d%d", run, rand.IntN(1e6)),
		"sex": sex, "birth_date": birth, "abo": abo, "rh_positive": rh, "health": map[string]any{"medications": meds, "conditions": []string{}}}, 201, &d)
	return d.ID, err
}

func (s *state) elig(id int64, body map[string]any) (decision, error) {
	var d decision
	err := s.call("hemsire", "POST", fmt.Sprintf("/api/v1/donors/%d/eligibility", id), body, 200, &d)
	return d, err
}

func (s *state) donateProduce(sex, abo string, rh bool) (produced, error) {
	var out produced
	d, err := s.newDonor(sex, abo, rh, "1991-01-01", nil)
	if err != nil {
		return out, err
	}
	out.donor = d
	var don donation
	if err := s.call("hemsire", "POST", "/api/v1/donations", map[string]any{"donor_id": d, "volume_ml": 450, "hemoglobin": 14.2, "weight_kg": 72}, 201, &don); err != nil {
		return out, err
	}
	out.donation = don.ID
	err = s.call("lab", "POST", fmt.Sprintf("/api/v1/donations/%d/components", don.ID), map[string]any{}, 201, &out.products)
	return out, err
}

func (s *state) request(who, comp, group string, units int, urgency string) (string, request, error) {
	var r request
	code, body := s.raw(who, "POST", "/api/v1/requests", map[string]any{"component": comp, "group": group, "units": units, "urgency": urgency,
		"patient_ref": fmt.Sprintf("DOG-%d-%d", run, rand.IntN(1e6))}, fmt.Sprintf("req-%d-%d", run, rand.IntN(1e9)))
	if code != 201 {
		return body, r, fmt.Errorf("talep oluşturulamadı: %d %s", code, trunc(body))
	}
	return body, r, json.Unmarshal([]byte(body), &r)
}

func (s *state) login(who string) error {
	var t struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
	}
	if err := s.call("", "POST", "/auth/login", map[string]string{"email": who + "@damarbagis.local", "password": pw}, 200, &t); err != nil {
		return err
	}
	s.tok[who], s.refresh[who] = t.AccessToken, t.RefreshToken
	return nil
}

func (s *state) call(who, method, path string, body any, want int, out any) error {
	_, err := s.callH(who, method, path, body, want, out, "")
	return err
}

func (s *state) callH(who, method, path string, body any, want int, out any, key string) (http.Header, error) {
	h := map[string]string{}
	if key != "" {
		h["Idempotency-Key"] = key
	}
	return s.do(who, method, path, body, want, out, h)
}

func (s *state) callHdr(who, method, path string, body any, want int, out any, hdr map[string]string) error {
	_, err := s.do(who, method, path, body, want, out, hdr)
	return err
}

func (s *state) do(who, method, path string, body any, want int, out any, hdr map[string]string) (http.Header, error) {
	var rd io.Reader
	if body != nil && method != "GET" {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, api+path, rd)
	req.Header.Set("Content-Type", "application/json")
	if who != "" {
		req.Header.Set("Authorization", "Bearer "+s.tok[who])
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != want {
		return resp.Header, fmt.Errorf("%s %s: HTTP %d (beklenen %d) %s", method, path, resp.StatusCode, want, trunc(string(data)))
	}
	if out != nil && len(data) > 0 {
		if err := json.Unmarshal(data, out); err != nil {
			return resp.Header, fmt.Errorf("yanıt çözümlenemedi: %w", err)
		}
	}
	return resp.Header, nil
}

func (s *state) raw(who, method, path string, body any, key string) (int, string) {
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, api+path, rd)
	req.Header.Set("Content-Type", "application/json")
	if who != "" {
		req.Header.Set("Authorization", "Bearer "+s.tok[who])
	}
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, err.Error()
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(data)
}

func expect(ok bool, msg string) error {
	if ok {
		return nil
	}
	return fmt.Errorf("%s", msg)
}

func trunc(s string) string {
	if len(s) > 300 {
		return s[:300] + "…"
	}
	return s
}

func waitHealthy(max time.Duration) error {
	deadline := time.Now().Add(max)
	for {
		resp, err := client.Get(api + "/health")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == 200 {
				return nil
			}
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("%s/health yanıt vermiyor", api)
		}
		time.Sleep(2 * time.Second)
	}
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
