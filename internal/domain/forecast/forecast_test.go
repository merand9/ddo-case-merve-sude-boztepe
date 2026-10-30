package forecast

import (
	"math"
	"testing"
	"time"
)

var last = time.Date(2026, 10, 29, 0, 0, 0, 0, time.UTC) // Perşembe

// weekly: Pazartesi yoğun, Pazar düşük deterministik seri.
func weekly(days int, lastDay time.Time) []float64 {
	pattern := map[time.Weekday]float64{
		time.Monday: 14, time.Tuesday: 12, time.Wednesday: 11, time.Thursday: 10,
		time.Friday: 10, time.Saturday: 7, time.Sunday: 6,
	}
	out := make([]float64, days)
	for i := range out {
		out[i] = pattern[lastDay.AddDate(0, 0, -(days-1-i)).Weekday()]
	}
	return out
}

func TestPredictConstantSeries(t *testing.T) {
	h := make([]float64, 56)
	for i := range h {
		h[i] = 5
	}
	for _, v := range Default.Predict(h, last, 7) {
		if math.Abs(v-5) > 1e-9 {
			t.Fatalf("sabit seride tahmin 5 olmalı: %v", v)
		}
	}
}

func TestPredictCapturesWeekdayEffect(t *testing.T) {
	h := weekly(56, last)
	p := Default.Predict(h, last, 7) // Cuma..Perşembe
	// 4. gün Pazartesi (Cuma+3), 3. gün Pazar
	mon, sun := p[3], p[2]
	if math.Abs(mon-14) > 0.01 || math.Abs(sun-6) > 0.01 {
		t.Fatalf("haftanın günü etkisi yakalanmalı: pzt=%.2f paz=%.2f", mon, sun)
	}
}

func TestPredictEmptyAndShortHistory(t *testing.T) {
	if p := Default.Predict(nil, last, 3); len(p) != 3 || p[0] != 0 {
		t.Fatalf("boş geçmiş sıfır tahmin vermeli: %v", p)
	}
	p := Default.Predict([]float64{2, 4}, last, 1)
	if math.Abs(p[0]-3) > 1e-9 {
		t.Fatalf("kısa geçmişte düz ortalama: %v", p)
	}
	if p := Default.Predict([]float64{1}, last, 0); len(p) != 0 {
		t.Fatal("ufuk 0 boş dönmeli")
	}
}

func TestWeekdayFactorsZeroSeries(t *testing.T) {
	f := Default.WeekdayFactors(make([]float64, 28), last)
	for _, v := range f {
		if v != 1 {
			t.Fatalf("sıfır seride çarpan 1 olmalı: %v", f)
		}
	}
}

func TestBacktestPerfectOnPeriodicSeries(t *testing.T) {
	h := weekly(84, last)
	r := Default.Backtest(h, last, 28)
	if r.Days != 28 || r.MAPE > 0.5 || r.WAPE > 0.5 {
		t.Fatalf("periyodik seride hata ~0 olmalı: %+v", r)
	}
	if len(r.Points) != 28 || !r.Points[27].Day.Equal(last) {
		t.Fatalf("noktalar son güne kadar uzanmalı")
	}
}

func TestBacktestNoLeakage(t *testing.T) {
	// Son günde ani sıçrama: sızıntı olmasaydı tahmin bunu göremez.
	h := weekly(84, last)
	h[len(h)-1] = 100
	r := Default.Backtest(h, last, 1)
	if r.Points[0].Forecast > 20 {
		t.Fatalf("tahmin gelecekteki veriyi kullanmamalı: %+v", r.Points[0])
	}
	if r.MAPE < 80 {
		t.Fatalf("sıçrama büyük hata üretmeli: %v", r.MAPE)
	}
}

func TestBacktestTooShort(t *testing.T) {
	if r := Default.Backtest(make([]float64, 10), last, 28); r.Days != 0 {
		t.Fatalf("yetersiz veride test yapılmamalı: %+v", r)
	}
}

func TestDaysOfSupply(t *testing.T) {
	if d := DaysOfSupply(10, []float64{4, 4, 4}); math.Abs(d-2.5) > 1e-9 {
		t.Fatalf("10 ünite / 4 günlük → 2.5 gün, %v", d)
	}
	if d := DaysOfSupply(0, []float64{4}); d != 0 {
		t.Fatal("stok yoksa 0")
	}
	if d := DaysOfSupply(5, []float64{0, 0}); d != 999 {
		t.Fatalf("tüketim yoksa 999, %v", d)
	}
	if d := DaysOfSupply(20, []float64{2, 2}); math.Abs(d-10) > 1e-9 {
		t.Fatalf("ufuk ötesi ortalamayla uzatılmalı: %v", d)
	}
}

func TestExpiryLossFEFO(t *testing.T) {
	start := last.AddDate(0, 0, 1)
	exp := []time.Time{
		start.Add(12 * time.Hour),             // 1. gün içinde dolar
		start.Add(12 * time.Hour),             // 1. gün içinde dolar
		start.AddDate(0, 0, 1).Add(time.Hour), // 2. gün
		start.AddDate(0, 0, 10),               // ufuk dışı
	}
	// Günde 1 ünite tüketim: 1. gün bir ürün tüketilir, diğeri kaybolur;
	// 2. gün 2. günün ürünü tüketilir.
	total, per := ExpiryLoss(exp, start, []float64{1, 1, 1})
	if total != 1 || per[0] != 1 || per[1] != 0 {
		t.Fatalf("FEFO kayıp hesabı hatalı: toplam=%d gün=%v", total, per)
	}
	// Tüketim yoksa ufuk içinde dolan tüm ürünler kayıp.
	total, _ = ExpiryLoss(exp, start, []float64{0, 0, 0})
	if total != 3 {
		t.Fatalf("tüketimsiz kayıp 3 olmalı: %d", total)
	}
}

func TestExpiryLossFractionalDemandAccumulates(t *testing.T) {
	start := last.AddDate(0, 0, 1)
	exp := []time.Time{start.AddDate(0, 0, 5), start.AddDate(0, 0, 5)}
	total, _ := ExpiryLoss(exp, start, []float64{0.5, 0.5, 0.5, 0.5, 0.5})
	if total != 0 {
		t.Fatalf("0.5/gün × 5 gün = 2 ünite tüketilmeli, kayıp 0: %d", total)
	}
}

func TestLevel(t *testing.T) {
	if Level(1, 2, 4) != "critical" || Level(3, 2, 4) != "low" || Level(5, 2, 4) != "" {
		t.Fatal("alarm seviyeleri hatalı")
	}
}
