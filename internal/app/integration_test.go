package app

// Entegrasyon testleri gerçek PostgreSQL ister: TEST_DATABASE_URL tanımlı değilse atlanır.
// Her test paketi çalıştırmasında şema sıfırlanır ve migrasyonlar yeniden uygulanır.

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/msboztepe/damarbagis/internal/auth"
	"github.com/msboztepe/damarbagis/internal/config"
	"github.com/msboztepe/damarbagis/internal/db"
	"github.com/msboztepe/damarbagis/internal/httpx"
)

var (
	testApp  *App
	testOnce sync.Once
	testErr  error
)

func setup(t *testing.T) *App {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL tanımlı değil; entegrasyon testi atlandı")
	}
	testOnce.Do(func() {
		ctx := context.Background()
		var pool *pgxpool.Pool
		pool, testErr = db.Connect(ctx, url, 40, 10*time.Second)
		if testErr != nil {
			return
		}
		if _, testErr = pool.Exec(ctx, `DROP SCHEMA public CASCADE; CREATE SCHEMA public;`); testErr != nil {
			return
		}
		log := slog.New(slog.NewTextHandler(io.Discard, nil))
		if testErr = db.Migrate(ctx, pool, log); testErr != nil {
			return
		}
		cfg := config.Config{
			JWTSecret: []byte(strings.Repeat("j", 32)), AccessTTL: time.Minute, RefreshTTL: time.Hour,
			HealthKey: []byte(strings.Repeat("h", 32)), BarcodeSecret: []byte("barcode-secret-xx"),
			DemoPassword: "Demo1234!", AlertCritical: 2, AlertLow: 4,
		}
		_ = base64.StdEncoding
		testApp, testErr = New(pool, cfg, log)
		if testErr != nil {
			return
		}
		testErr = testApp.EnsureDemoData(ctx)
	})
	if testErr != nil {
		t.Fatal(testErr)
	}
	return testApp
}

func as(t *testing.T, a *App, email string) context.Context {
	t.Helper()
	var c auth.Claims
	var hid, did *int64
	err := a.DB.QueryRow(context.Background(), `SELECT id, role, hospital_id, donor_id FROM users WHERE email=$1`, email).
		Scan(&c.UserID, &c.Role, &hid, &did)
	if err != nil {
		t.Fatal(err)
	}
	if hid != nil {
		c.HospitalID = *hid
	}
	if did != nil {
		c.DonorID = *did
	}
	return httpx.WithClaims(context.Background(), &c)
}

const (
	nurse = "hemsire@damarbagis.local"
	lab1  = "lab@damarbagis.local"
	lab2  = "lab2@damarbagis.local"
	bank  = "bankasorumlusu@damarbagis.local"
	hosp  = "hastane@damarbagis.local"
)

var seq int

func newDonor(t *testing.T, a *App, sex, abo string, rh bool) Donor {
	t.Helper()
	seq++
	d, err := a.RegisterDonor(as(t, a, nurse), NewDonor{FirstName: "Test", LastName: fmt.Sprintf("Kişi%d", seq), Sex: sex,
		BirthDate: "1990-05-05", ABO: abo, RhPositive: &rh, Health: HealthInfo{Medications: []string{"gizli-ilac"}}})
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// donateAndRelease bağış + üretim + negatif test: stokta kullanılabilir ürünler oluşur.
func donateAndRelease(t *testing.T, a *App, d Donor) []Product {
	t.Helper()
	don, err := a.RecordDonation(as(t, a, nurse), NewDonation{DonorID: d.ID, VolumeML: 450, Hemoglobin: 14.5, WeightKg: 75})
	if err != nil {
		t.Fatal(err)
	}
	ps, err := a.ProduceComponents(as(t, a, lab1), don.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.EnterTestResult(as(t, a, lab1), don.ID, Markers{}); err != nil {
		t.Fatal(err)
	}
	return ps
}

func apiCode(err error) string {
	var e *httpx.Error
	if errors.As(err, &e) {
		return e.Code
	}
	return httpx.FromErr(err).Code
}

// Yarış durumu: aynı kıt stok için eşzamanlı 20 tahsis isteği; hiçbir ürün iki kez rezerve edilmez
// ve toplam rezervasyon stok miktarını aşmaz.
func TestConcurrentAllocationNeverDoubleBooks(t *testing.T) {
	a := setup(t)
	ctx := context.Background()
	// Yalnızca bu testte kullanılan grup: AB- trombosit.
	var stockIDs []int64
	for i := 0; i < 3; i++ {
		d := newDonor(t, a, "M", "AB", false)
		for _, p := range donateAndRelease(t, a, d) {
			if p.Component == "PLT" {
				stockIDs = append(stockIDs, p.ID)
			}
		}
	}
	if len(stockIDs) != 3 {
		t.Fatalf("3 trombosit bekleniyordu: %d", len(stockIDs))
	}
	hctx := as(t, a, hosp)
	var reqIDs []int64
	for i := 0; i < 20; i++ {
		var id int64
		if err := a.DB.QueryRow(ctx, `INSERT INTO requests (hospital_id, component, abo, rh_positive, units, patient_ref, auto_allocate)
			VALUES (1,'PLT','AB',false,1,$1,false) RETURNING id`, fmt.Sprintf("YARIS-%d", i)).Scan(&id); err != nil {
			t.Fatal(err)
		}
		reqIDs = append(reqIDs, id)
	}
	_ = hctx
	var wg sync.WaitGroup
	start := make(chan struct{})
	errs := make(chan error, len(reqIDs))
	for _, id := range reqIDs {
		wg.Add(1)
		go func(id int64) {
			defer wg.Done()
			<-start
			_, err := a.Allocate(as(t, a, bank), id, time.Minute)
			errs <- err
		}(id)
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("tahsis hatası: %v", err)
		}
	}
	var active, distinct int
	if err := a.DB.QueryRow(ctx, `SELECT count(*), count(DISTINCT product_id) FROM reservations
		WHERE status='active' AND request_id = ANY($1)`, reqIDs).Scan(&active, &distinct); err != nil {
		t.Fatal(err)
	}
	if active != 3 || distinct != 3 {
		t.Fatalf("3 ürün için tam 3 rezervasyon olmalı: aktif=%d farklı=%d", active, distinct)
	}
	var reservedProducts int
	_ = a.DB.QueryRow(ctx, `SELECT count(*) FROM products WHERE id = ANY($1) AND status='reserved'`, stockIDs).Scan(&reservedProducts)
	if reservedProducts != 3 {
		t.Fatalf("ürünler reserved olmalı: %d", reservedProducts)
	}
}

// Veritabanı düzeyindeki son savunma: aynı ürüne ikinci aktif rezervasyon eklenemez.
func TestUniqueActiveReservationIndex(t *testing.T) {
	a := setup(t)
	ctx := context.Background()
	d := newDonor(t, a, "M", "B", false)
	ps := donateAndRelease(t, a, d)
	var r1, r2 int64
	_ = a.DB.QueryRow(ctx, `INSERT INTO requests (hospital_id, component, abo, rh_positive, units, patient_ref) VALUES (1,'RBC','B',false,1,'U-1') RETURNING id`).Scan(&r1)
	_ = a.DB.QueryRow(ctx, `INSERT INTO requests (hospital_id, component, abo, rh_positive, units, patient_ref) VALUES (1,'RBC','B',false,1,'U-2') RETURNING id`).Scan(&r2)
	if _, err := a.DB.Exec(ctx, `INSERT INTO reservations (request_id, product_id, expires_at) VALUES ($1,$2, now()+interval '1 hour')`, r1, ps[0].ID); err != nil {
		t.Fatal(err)
	}
	_, err := a.DB.Exec(ctx, `INSERT INTO reservations (request_id, product_id, expires_at) VALUES ($1,$2, now()+interval '1 hour')`, r2, ps[0].ID)
	if err == nil || !strings.Contains(err.Error(), "reservations_one_active") {
		t.Fatalf("ikinci aktif rezervasyon reddedilmeli: %v", err)
	}
}

// Aynı bağışçı için eşzamanlı iki bağış kaydından yalnızca biri başarılı olur (satır kilidi).
func TestConcurrentDonationsSameDonor(t *testing.T) {
	a := setup(t)
	d := newDonor(t, a, "M", "A", true)
	var wg sync.WaitGroup
	results := make(chan error, 5)
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := a.RecordDonation(as(t, a, nurse), NewDonation{DonorID: d.ID, VolumeML: 450, Hemoglobin: 14, WeightKg: 70})
			results <- err
		}()
	}
	wg.Wait()
	close(results)
	ok, rejected := 0, 0
	for err := range results {
		switch {
		case err == nil:
			ok++
		case apiCode(err) == "donor_ineligible":
			rejected++
		default:
			t.Fatalf("beklenmeyen hata: %v", err)
		}
	}
	if ok != 1 || rejected != 4 {
		t.Fatalf("1 başarılı, 4 red bekleniyordu: %d/%d", ok, rejected)
	}
}

// FEFO + tam uyum önceliği: aynı gruptan en erken SKT'li ürün seçilir; tam uyum varken O verilmez.
func TestAllocationExactFirstThenFEFO(t *testing.T) {
	a := setup(t)
	ctx := context.Background()
	// B+ alıcı için: iki B+ ve bir O- RBC
	early := donateAndRelease(t, a, newDonor(t, a, "M", "B", true))
	time.Sleep(10 * time.Millisecond)
	donateAndRelease(t, a, newDonor(t, a, "M", "B", true))
	donateAndRelease(t, a, newDonor(t, a, "M", "O", false))
	var earlyRBC int64
	for _, p := range early {
		if p.Component == "RBC" {
			earlyRBC = p.ID
		}
	}
	// Erken ürünün SKT'sini (ürün kimliği değiştirilemez olduğundan) yeni bir üründe deneriz:
	// iki B+ arasında FEFO, toplama zamanına göre en erken olan ilk üründür.
	var reqID int64
	_ = a.DB.QueryRow(ctx, `INSERT INTO requests (hospital_id, component, abo, rh_positive, units, patient_ref, auto_allocate)
		VALUES (1,'RBC','B',true,1,'FEFO-1',false) RETURNING id`).Scan(&reqID)
	res, err := a.Allocate(as(t, a, bank), reqID, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	var got int64
	var abo string
	_ = a.DB.QueryRow(ctx, `SELECT p.id, p.abo FROM reservations r JOIN products p ON p.id=r.product_id WHERE r.request_id=$1`, reqID).Scan(&got, &abo)
	if abo != "B" {
		t.Fatalf("tam uyum varken %s grubu verilmemeli", abo)
	}
	var minExp time.Time
	_ = a.DB.QueryRow(ctx, `SELECT min(expires_at) FROM products WHERE component='RBC' AND abo='B' AND rh_positive AND status IN ('available','reserved')`).Scan(&minExp)
	var gotExp time.Time
	_ = a.DB.QueryRow(ctx, `SELECT expires_at FROM products WHERE id=$1`, got).Scan(&gotExp)
	if !gotExp.Equal(minExp) {
		t.Fatalf("FEFO: en erken SKT'li ürün seçilmeli (seçilen %v, en erken %v)", gotExp, minExp)
	}
	_ = earlyRBC
	if res.Status != "reserved" {
		t.Fatalf("talep reserved olmalı: %s", res.Status)
	}
}

// Acil talep, stok yetmediğinde rutin talebin rezervasyonunu devralır.
func TestEmergencyPreemptsRoutine(t *testing.T) {
	a := setup(t)
	ctx := context.Background()
	// Yalnızca bu testte kullanılan: B- trombosit, tek ünite.
	ps := donateAndRelease(t, a, newDonor(t, a, "M", "B", false))
	var plt int64
	for _, p := range ps {
		if p.Component == "PLT" {
			plt = p.ID
		}
	}
	_, _ = a.DB.Exec(ctx, `UPDATE products SET status='discarded' WHERE component='PLT' AND NOT rh_positive AND status='available' AND id<>$1`, plt)
	var routine, emergency int64
	_ = a.DB.QueryRow(ctx, `INSERT INTO requests (hospital_id, component, abo, rh_positive, units, urgency, patient_ref, auto_allocate)
		VALUES (1,'PLT','B',false,1,'routine','RUTIN-1',false) RETURNING id`).Scan(&routine)
	if r, err := a.Allocate(as(t, a, bank), routine, time.Hour); err != nil || len(r.Reserved) != 1 {
		t.Fatalf("rutin talep ürünü almalı: %+v %v", r, err)
	}
	_ = a.DB.QueryRow(ctx, `INSERT INTO requests (hospital_id, component, abo, rh_positive, units, urgency, patient_ref, auto_allocate)
		VALUES (2,'PLT','B',false,1,'emergency','ACIL-1',false) RETURNING id`).Scan(&emergency)
	r, err := a.Allocate(as(t, a, bank), emergency, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Preempted) != 1 || r.Status != "reserved" {
		t.Fatalf("acil talep rutin rezervasyonu devralmalı: %+v", r)
	}
	var st string
	_ = a.DB.QueryRow(ctx, `SELECT status FROM requests WHERE id=$1`, routine).Scan(&st)
	if st != "open" {
		t.Fatalf("rutin talep tekrar open olmalı: %s", st)
	}
}

// Rezervasyon süresi dolunca ürün otomatik olarak stoğa döner.
func TestReservationExpiryReleases(t *testing.T) {
	a := setup(t)
	ctx := context.Background()
	ps := donateAndRelease(t, a, newDonor(t, a, "F", "A", false))
	var reqID int64
	_ = a.DB.QueryRow(ctx, `INSERT INTO requests (hospital_id, component, abo, rh_positive, units, patient_ref, auto_allocate)
		VALUES (1,'FFP','A',false,1,'TTL-1',false) RETURNING id`).Scan(&reqID)
	r, err := a.Allocate(as(t, a, bank), reqID, time.Second)
	if err != nil || len(r.Reserved) != 1 {
		t.Fatalf("tahsis: %+v %v", r, err)
	}
	time.Sleep(1200 * time.Millisecond)
	if _, err := a.ReleaseExpired(ctx); err != nil {
		t.Fatal(err)
	}
	var rs, ps2 string
	_ = a.DB.QueryRow(ctx, `SELECT r.status, p.status FROM reservations r JOIN products p ON p.id=r.product_id WHERE r.request_id=$1`, reqID).Scan(&rs, &ps2)
	if rs != "released" || ps2 != "available" {
		t.Fatalf("rezervasyon released, ürün available olmalı: %s %s", rs, ps2)
	}
	_ = ps
}

// Pozitif test: bölünmüş alt ürünler dahil tüm türevler karantinaya alınır, bağışçı kalıcı reddedilir.
func TestPositiveTestQuarantinesAllDerivativesRecursively(t *testing.T) {
	a := setup(t)
	ctx := context.Background()
	d := newDonor(t, a, "M", "O", true)
	// Önceki bağış (look-back penceresinde): stokta, bölünmüş
	prev := donateAndRelease(t, a, d)
	var rbc int64
	for _, p := range prev {
		if p.Component == "RBC" {
			rbc = p.ID
		}
	}
	children, err := a.SplitProduct(as(t, a, bank), rbc, 2)
	if err != nil || len(children) != 2 {
		t.Fatalf("bölme: %v", err)
	}
	// Bekleme süresini aşmak için yeni bağışı test amaçlı geriye dönük yerleştiremeyiz; önceki bağışı
	// geçmişe kaydırıp yeni bağış alıyoruz.
	if _, err := a.DB.Exec(ctx, `UPDATE donations SET collected_at = now() - interval '100 days' WHERE donor_id=$1`, d.ID); err != nil {
		t.Fatal(err)
	}
	don, err := a.RecordDonation(as(t, a, nurse), NewDonation{DonorID: d.ID, VolumeML: 450, Hemoglobin: 15, WeightKg: 80})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.ProduceComponents(as(t, a, lab1), don.ID, nil); err != nil {
		t.Fatal(err)
	}
	out, err := a.EnterTestResult(as(t, a, lab1), don.ID, Markers{HCV: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Lookback) != 1 {
		t.Fatalf("look-back önceki bağışı bulmalı: %+v", out.Lookback)
	}
	var notQ int
	_ = a.DB.QueryRow(ctx, `SELECT count(*) FROM products p JOIN donations dn ON dn.id=p.donation_id
		WHERE dn.donor_id=$1 AND p.status NOT IN ('quarantined','split')`, d.ID).Scan(&notQ)
	if notQ != 0 {
		t.Fatalf("bağışçının tüm ürünleri karantinada olmalı, %d değil", notQ)
	}
	maxDepth := 0
	for _, p := range out.Quarantined {
		if p.Depth > maxDepth {
			maxDepth = p.Depth
		}
	}
	if maxDepth != 1 {
		t.Fatalf("recursive gezinti alt ürünleri (depth=1) bulmalı: %+v", out.Quarantined)
	}
	dec, err := a.CheckEligibility(ctx, d.ID, EligibilityRequest{})
	if err != nil || dec.Eligible || dec.NextEligibleAt != nil {
		t.Fatalf("bağışçı kalıcı olarak reddedilmeli: %+v %v", dec, err)
	}
	// Karantinadaki ürün tahsis edilemez / dağıtılamaz (geçersiz geçiş DB'de reddedilir).
	err = a.ForceTransition(as(t, a, bank), children[0].ID, "issued", "deneme")
	if apiCode(err) != "invalid_transition" {
		t.Fatalf("quarantined → issued reddedilmeli: %v", err)
	}
}

// Test sonucu değişikliği çift onay ister: aynı kişi onaylayamaz, ikinci uzman onaylar.
func TestDualApprovalForTestChange(t *testing.T) {
	a := setup(t)
	ctx := context.Background()
	d := newDonor(t, a, "F", "A", true)
	don, err := a.RecordDonation(as(t, a, nurse), NewDonation{DonorID: d.ID, VolumeML: 450, Hemoglobin: 13.2, WeightKg: 60})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.ProduceComponents(as(t, a, lab1), don.ID, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := a.EnterTestResult(as(t, a, lab1), don.ID, Markers{HBV: true}); err != nil {
		t.Fatal(err)
	}
	// Doğrudan UPDATE tetikleyicide reddedilir.
	if _, err := a.DB.Exec(ctx, `UPDATE test_results SET hbv=false, outcome='negative' WHERE donation_id=$1`, don.ID); err == nil {
		t.Fatal("onaysız test sonucu güncellemesi reddedilmeli")
	}
	tc, err := a.RequestTestChange(as(t, a, lab1), don.ID, Markers{}, "Tekrar test negatif (yanlış pozitif)")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := a.DecideTestChange(as(t, a, lab1), tc.ID, true); apiCode(err) != "self_approval_forbidden" {
		t.Fatalf("kendi talebini onaylama reddedilmeli: %v", err)
	}
	_, eff, err := a.DecideTestChange(as(t, a, lab2), tc.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	if eff.Outcome != "negative" || eff.Released != 3 {
		t.Fatalf("düzeltme sonrası ürünler serbest kalmalı: %+v", eff)
	}
	var ver int
	_ = a.DB.QueryRow(ctx, `SELECT version FROM test_results WHERE donation_id=$1`, don.ID).Scan(&ver)
	if ver != 2 {
		t.Fatalf("sürüm artmalı: %d", ver)
	}
	if dec, _ := a.CheckEligibility(ctx, d.ID, EligibilityRequest{At: time.Now().AddDate(0, 5, 0).Format("2006-01-02")}); !dec.Eligible {
		t.Fatalf("düzeltme sonrası kalıcı ret kapanmalı: %+v", dec)
	}
}

// Eklemeli-yalnız tablolar ve ürün kimlik alanları değiştirilemez.
func TestAppendOnlyAndImmutability(t *testing.T) {
	a := setup(t)
	ctx := context.Background()
	ps := donateAndRelease(t, a, newDonor(t, a, "M", "AB", true))
	for _, sql := range []string{
		`UPDATE product_events SET to_status='available'`,
		`DELETE FROM product_events`,
		`UPDATE audit_log SET action='x'`,
		`DELETE FROM audit_log`,
		`TRUNCATE audit_log`,
	} {
		if _, err := a.DB.Exec(ctx, sql); err == nil {
			t.Errorf("reddedilmeli: %s", sql)
		}
	}
	if _, err := a.DB.Exec(ctx, `UPDATE products SET expires_at = expires_at + interval '10 days' WHERE id=$1`, ps[0].ID); err == nil {
		t.Error("SKT değiştirilememeli")
	}
	if _, err := a.DB.Exec(ctx, `DELETE FROM products WHERE id=$1`, ps[0].ID); err == nil {
		t.Error("ürün silinememeli")
	}
	// Her geçiş olay olarak kaydedilir: pending_test → available
	var n int
	_ = a.DB.QueryRow(ctx, `SELECT count(*) FROM product_events WHERE product_id=$1`, ps[0].ID).Scan(&n)
	if n != 2 {
		t.Errorf("ürün için 2 olay (üretim + serbest bırakma) bekleniyordu: %d", n)
	}
}

// Sağlık verisi şifreli saklanır, gerekçesiz okunamaz, okuma denetime yazılır.
func TestHealthDataEncryptedAndAudited(t *testing.T) {
	a := setup(t)
	ctx := context.Background()
	d := newDonor(t, a, "F", "O", true)
	var raw []byte
	_ = a.DB.QueryRow(ctx, `SELECT health_enc FROM donors WHERE id=$1`, d.ID).Scan(&raw)
	if strings.Contains(string(raw), "gizli-ilac") {
		t.Fatal("sağlık verisi düz metin saklanmamalı")
	}
	if _, err := a.ReadHealth(as(t, a, nurse), d.ID, "kısa"); apiCode(err) != "access_reason_required" {
		t.Fatalf("gerekçesiz erişim reddedilmeli: %v", err)
	}
	h, err := a.ReadHealth(as(t, a, nurse), d.ID, "Bağış öncesi muayene kontrolü")
	if err != nil || len(h.Medications) != 1 || h.Medications[0] != "gizli-ilac" {
		t.Fatalf("sağlık verisi çözülmeli: %+v %v", h, err)
	}
	var reason string
	if err := a.DB.QueryRow(ctx, `SELECT reason FROM audit_log WHERE action='donor.health.read' AND entity_id=$1 ORDER BY id DESC LIMIT 1`,
		fmt.Sprint(d.ID)).Scan(&reason); err != nil || reason != "Bağış öncesi muayene kontrolü" {
		t.Fatalf("erişim gerekçesiyle denetime yazılmalı: %q %v", reason, err)
	}
}

// Kural sürümü ileri tarihten geçerli olur; o tarihteki karar yeni kurala göre verilir.
func TestRuleVersionTimeValidity(t *testing.T) {
	a := setup(t)
	ctx := as(t, a, "admin@damarbagis.local")
	rules, err := a.ListRules(ctx, "eligibility")
	if err != nil {
		t.Fatal(err)
	}
	params := strings.Replace(string(rules[len(rules)-1].Params), `"F": 120`, `"F": 60`, 1)
	if params == string(rules[len(rules)-1].Params) {
		t.Fatal("parametre değiştirilemedi")
	}
	from := time.Now().AddDate(0, 0, 200).Format("2006-01-02")
	if _, err := a.AddRuleVersion(ctx, "eligibility", from, "test", []byte(params)); err != nil {
		t.Fatal(err)
	}
	if _, err := a.AddRuleVersion(ctx, "eligibility", "2020-01-01", "geçmiş", []byte(params)); err == nil {
		t.Fatal("geçmiş tarihli sürüm reddedilmeli")
	}
	d := newDonor(t, a, "F", "B", true)
	at := time.Now().AddDate(0, 0, 200)
	if _, err := a.DB.Exec(context.Background(), `INSERT INTO donations (donor_id, donation_type, collected_at, volume_ml, hemoglobin, weight_kg, bag_no)
		VALUES ($1,'whole_blood',$2,450,13,60,$3)`, d.ID, at.AddDate(0, 0, -70), fmt.Sprintf("TV-%d", d.ID)); err != nil {
		t.Fatal(err)
	}
	dec, err := a.CheckEligibility(context.Background(), d.ID, EligibilityRequest{At: at.AddDate(0, 0, 1).Format("2006-01-02")})
	if err != nil || !dec.Eligible || dec.RuleVersion != from {
		t.Fatalf("yeni kural (60 gün) uygulanmalı: %+v %v", dec, err)
	}
	dec, _ = a.CheckEligibility(context.Background(), d.ID, EligibilityRequest{At: at.AddDate(0, 0, -1).Format("2006-01-02")})
	if dec.Eligible {
		t.Fatalf("eski kural döneminde (120 gün) uygun olmamalı: %+v", dec)
	}
}

// Hastane yalnızca ürün bilgisi görür; başka hastanenin talebini göremez.
func TestHospitalIsolation(t *testing.T) {
	a := setup(t)
	donateAndRelease(t, a, newDonor(t, a, "M", "A", true))
	req, err := a.CreateRequest(as(t, a, hosp), 1, NewRequest{Component: "RBC", Group: "A+", Units: 1, PatientRef: "IZOLE-1"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.GetRequest(context.Background(), req.ID, 3); apiCode(err) != "not_found" {
		t.Fatalf("başka hastanenin talebi görünmemeli: %v", err)
	}
	if len(req.Reservations) == 0 {
		t.Fatal("rezervasyon bekleniyordu")
	}
}

// Uçtan uca izleme: bağışçı → hasta ve hasta → bağışçı (bölünmüş ürün üzerinden).
func TestTraceForwardAndBack(t *testing.T) {
	a := setup(t)
	ctx := context.Background()
	d := newDonor(t, a, "M", "AB", false)
	ps := donateAndRelease(t, a, d)
	var rbc int64
	for _, p := range ps {
		if p.Component == "RBC" {
			rbc = p.ID
		}
	}
	kids, err := a.SplitProduct(as(t, a, bank), rbc, 2)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = a.DB.Exec(ctx, `UPDATE products SET status='discarded' WHERE component='RBC' AND abo IN ('AB','A','B','O') AND NOT rh_positive AND status='available' AND id <> ALL($1)`,
		[]int64{kids[0].ID, kids[1].ID})
	req, err := a.CreateRequest(as(t, a, hosp), 1, NewRequest{Component: "RBC", Group: "AB-", Units: 1, PatientRef: "IZ-HASTA-1"})
	if err != nil || len(req.Reservations) != 1 {
		t.Fatalf("talep: %+v %v", req, err)
	}
	if _, err := a.IssueRequest(as(t, a, bank), req.ID); err != nil {
		t.Fatal(err)
	}
	if err := a.RecordTransfusion(as(t, a, hosp), 1, req.Reservations[0].ProductNo, "IZ-HASTA-1"); err != nil {
		t.Fatal(err)
	}
	fw, err := a.TraceForward(ctx, d.ID)
	if err != nil {
		t.Fatal(err)
	}
	if pats := fw["patients"].([]string); len(pats) != 1 || pats[0] != "IZ-HASTA-1" {
		t.Fatalf("ileri izleme hastayı bulmalı: %v", pats)
	}
	back, err := a.TraceBack(ctx, "IZ-HASTA-1")
	if err != nil {
		t.Fatal(err)
	}
	links := fmt.Sprintf("%+v", back["links"])
	if !strings.Contains(links, d.DonorNo) || !strings.Contains(links, "Hops:1") {
		t.Fatalf("geri izleme bölünmüş üründen köke (1 adım) bağışçıya ulaşmalı: %s", links)
	}
}
