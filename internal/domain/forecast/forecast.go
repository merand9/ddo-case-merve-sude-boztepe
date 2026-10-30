// Package forecast kan grubu bazında tüketim tahmini, SKT kaynaklı kayıp tahmini ve
// geriye dönük test (backtest) hesaplarını içerir. Fonksiyonlar saf ve deterministiktir.
package forecast

import (
	"math"
	"sort"
	"time"
)

// Model hareketli ortalama + haftanın günü etkisi modelidir.
//
//	tahmin(gün) = taban × çarpan(haftanın günü)
//	taban       = son Window günün ortalaması (Window 7'nin katı olduğundan haftalık etkiden arınmış)
//	çarpan(g)   = son Weeks haftada g gününün ortalaması / aynı dönemin genel ortalaması
type Model struct {
	Window int // hareketli ortalama penceresi (gün)
	Weeks  int // haftanın günü etkisi için bakılan hafta sayısı
}

// Default makul varsayılanlar: 28 günlük ortalama, 8 haftalık gün etkisi.
var Default = Model{Window: 28, Weeks: 8}

// Predict lastDay'de biten günlük geçmişten sonraki horizon günü tahmin eder.
// history[i] = lastDay-(len-1-i) gününün tüketimi.
func (m Model) Predict(history []float64, lastDay time.Time, horizon int) []float64 {
	out := make([]float64, horizon)
	if len(history) == 0 || horizon <= 0 {
		return out
	}
	w := m.Window
	if w <= 0 || w > len(history) {
		w = len(history)
	}
	base := mean(history[len(history)-w:])
	factors := m.WeekdayFactors(history, lastDay)
	for k := 0; k < horizon; k++ {
		wd := lastDay.AddDate(0, 0, k+1).Weekday()
		out[k] = base * factors[wd]
	}
	return out
}

// WeekdayFactors haftanın her günü için çarpanı döner (veri yoksa 1).
func (m Model) WeekdayFactors(history []float64, lastDay time.Time) [7]float64 {
	var f [7]float64
	for i := range f {
		f[i] = 1
	}
	n := m.Weeks * 7
	if n <= 0 || n > len(history) {
		n = len(history) - len(history)%7
	}
	if n < 7 {
		return f
	}
	seg := history[len(history)-n:]
	overall := mean(seg)
	if overall == 0 {
		return f
	}
	var sum [7]float64
	var cnt [7]int
	for i, v := range seg {
		d := lastDay.AddDate(0, 0, -(n - 1 - i)).Weekday()
		sum[d] += v
		cnt[d]++
	}
	for d := 0; d < 7; d++ {
		if cnt[d] == 0 {
			continue
		}
		v := (sum[d] / float64(cnt[d])) / overall
		f[d] = math.Max(0.2, math.Min(3, v))
	}
	return f
}

// DaysOfSupply eldeki stoğun tahmini tüketimle kaç gün yeteceğini (kesirli) döner.
// Tahmin ufku içinde tükenmezse ufkun ötesini son günün tüketimiyle uzatır; tüketim
// sıfırsa 999 döner.
func DaysOfSupply(available int, daily []float64) float64 {
	left := float64(available)
	if left <= 0 {
		return 0
	}
	for i, d := range daily {
		if d >= left {
			return float64(i) + left/d
		}
		left -= d
	}
	last := 0.0
	if len(daily) > 0 {
		last = mean(daily)
	}
	if last <= 0 {
		return 999
	}
	return math.Min(999, float64(len(daily))+left/last)
}

// ExpiryLoss FEFO tüketim varsayımıyla ufuk içinde SKT'si dolacak ürün sayısını tahmin eder.
// expiries stoktaki ürünlerin son kullanma anları; start ufkun ilk günü; demand günlük tahmin.
// Her gün önce tüketim (en erken SKT'den), sonra o gün sonunda süresi dolanlar kayıp sayılır.
func ExpiryLoss(expiries []time.Time, start time.Time, demand []float64) (total int, perDay []int) {
	exp := append([]time.Time(nil), expiries...)
	sort.Slice(exp, func(i, j int) bool { return exp[i].Before(exp[j]) })
	perDay = make([]int, len(demand))
	start = truncDay(start)
	carry := 0.0
	for d := range demand {
		dayEnd := start.AddDate(0, 0, d+1)
		// Gün içinde süresi dolacak ürünler tüketimden önce de dolabilir; gerçekçi olması için
		// tüketim yalnızca o gün hâlâ geçerli olan ürünlerden yapılır.
		carry += demand[d]
		take := int(carry)
		carry -= float64(take)
		for take > 0 && len(exp) > 0 {
			exp = exp[1:]
			take--
		}
		lost := 0
		for len(exp) > 0 && exp[0].Before(dayEnd) {
			exp = exp[1:]
			lost++
		}
		perDay[d] = lost
		total += lost
	}
	return total, perDay
}

// BacktestPoint bir günün gerçek ve tahmin değeri.
type BacktestPoint struct {
	Day      time.Time `json:"day"`
	Actual   float64   `json:"actual"`
	Forecast float64   `json:"forecast"`
}

// BacktestResult geriye dönük test özeti.
type BacktestResult struct {
	Days   int             `json:"days"`
	MAPE   float64         `json:"mape"` // yüzde; gerçek değeri 0 olan günler hariç
	WAPE   float64         `json:"wape"` // yüzde; Σ|A−F| / ΣA (düşük hacimde daha kararlı)
	Bias   float64         `json:"bias"` // ortalama (F−A)
	Points []BacktestPoint `json:"points,omitempty"`
}

// Backtest son `days` günün her biri için yalnızca o günden önceki veriyle bir günlük tahmin
// yapar ve gerçek değerle karşılaştırır (rolling-origin, sızıntısız).
func (m Model) Backtest(history []float64, lastDay time.Time, days int) BacktestResult {
	res := BacktestResult{}
	minTrain := 14
	if days > len(history)-minTrain {
		days = len(history) - minTrain
	}
	if days <= 0 {
		return res
	}
	var apeSum, absErr, actSum, biasSum float64
	apeN := 0
	for t := len(history) - days; t < len(history); t++ {
		day := lastDay.AddDate(0, 0, -(len(history) - 1 - t))
		pred := m.Predict(history[:t], day.AddDate(0, 0, -1), 1)[0]
		act := history[t]
		res.Points = append(res.Points, BacktestPoint{Day: day, Actual: act, Forecast: round2(pred)})
		absErr += math.Abs(act - pred)
		actSum += act
		biasSum += pred - act
		if act > 0 {
			apeSum += math.Abs(act-pred) / act
			apeN++
		}
	}
	res.Days = days
	if apeN > 0 {
		res.MAPE = round2(100 * apeSum / float64(apeN))
	}
	if actSum > 0 {
		res.WAPE = round2(100 * absErr / actSum)
	}
	res.Bias = round2(biasSum / float64(days))
	return res
}

// Level stok alarm seviyesini gün cinsinden yeterlilikten türetir.
func Level(daysOfSupply, critical, low float64) string {
	switch {
	case daysOfSupply < critical:
		return "critical"
	case daysOfSupply < low:
		return "low"
	}
	return ""
}

func mean(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	s := 0.0
	for _, x := range xs {
		s += x
	}
	return s / float64(len(xs))
}

func round2(x float64) float64 { return math.Round(x*100) / 100 }

func truncDay(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}
