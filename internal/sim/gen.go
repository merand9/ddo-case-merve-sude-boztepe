// Package sim tekrar üretilebilir (aynı tohum + aynı çapa günü → aynı veri) seed verisi üretir.
//
// Üretici geçmişi gün gün simüle eder: bağışlar (uygunluk kurallarına uyarak), testler, bileşen
// üretimi, hastane talepleri, FEFO + uyum sıralı tahsis, dağıtım, transfüzyon ve SKT kayıpları.
// Böylece tahmin modülünün öğreneceği gerçekçi bir tüketim geçmişi oluşur.
package sim

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"math/rand/v2"
	"sort"
	"time"

	"github.com/msboztepe/damarbagis/internal/domain/compat"
)

// Config üretici ayarları.
type Config struct {
	Seed           uint64
	Anchor         time.Time // geçmişin bittiği gün (00:00 UTC, dahil değil)
	Donors         int
	HistoryDays    int
	DailyDonations int
	DailyRBCDemand float64
	Hospitals      int
}

// DefaultConfig 30.000 bağışçı, 120 gün geçmiş.
func DefaultConfig(seed uint64, anchor time.Time) Config {
	return Config{Seed: seed, Anchor: anchor, Donors: 30000, HistoryDays: 120, DailyDonations: 130,
		DailyRBCDemand: 105, Hospitals: 5}
}

// Satır tipleri: kimlikler 1'den başlayan yerel numaralardır; yükleyici gerçek kimliklere kaydırır.
type (
	Donor struct {
		N          int
		First      string
		Last       string
		Sex        string
		Birth      time.Time
		ABO        string
		Rh         bool
		Phone      string
		HealthJSON []byte
		Created    time.Time
	}
	Deferral struct {
		Donor int
		Code  string
		Start time.Time
		End   *time.Time
		Note  string
	}
	Donation struct {
		N      int
		Donor  int
		Type   string
		At     time.Time
		Volume int
		Hb     float64
		Weight float64
	}
	Test struct {
		Donation int
		HIV      bool
		HBV      bool
		HCV      bool
		Syphilis bool
		At       time.Time
	}
	Product struct {
		N         int
		Donation  int
		Component string
		ABO       string
		Rh        bool
		Volume    int
		Produced  time.Time
		Expires   time.Time
		Status    string
	}
	Event struct {
		Product int
		From    string // "" = NULL
		To      string
		At      time.Time
		Reason  string
	}
	Request struct {
		N         int
		Hospital  int
		Component string
		ABO       string
		Rh        bool
		Units     int
		Urgency   string
		Patient   string
		Status    string
		At        time.Time
	}
	Reservation struct {
		N       int
		Request int
		Product int
		Status  string
		At      time.Time
		Expires time.Time
		Closed  *time.Time
	}
	Distribution struct {
		Request  int
		Product  int
		Hospital int
		At       time.Time
	}
	Transfusion struct {
		Product  int
		Hospital int
		Patient  string
		At       time.Time
	}
)

// Dataset üretilen tüm veri.
type Dataset struct {
	Donors        []Donor
	Deferrals     []Deferral
	Donations     []Donation
	Tests         []Test
	Products      []Product
	Events        []Event
	Requests      []Request
	Reservations  []Reservation
	Distributions []Distribution
	Transfusions  []Transfusion
	Ineligible    int // üretim sırasında kurala takılıp atlanan bağış denemeleri
}

// Türkiye için yaklaşık kan grubu dağılımı.
var groupShare = []struct {
	g compat.Group
	p float64
}{
	{compat.Group{ABO: "A", RhPositive: true}, 0.378}, {compat.Group{ABO: "O", RhPositive: true}, 0.298},
	{compat.Group{ABO: "B", RhPositive: true}, 0.142}, {compat.Group{ABO: "AB", RhPositive: true}, 0.072},
	{compat.Group{ABO: "A", RhPositive: false}, 0.047}, {compat.Group{ABO: "O", RhPositive: false}, 0.039},
	{compat.Group{ABO: "B", RhPositive: false}, 0.016}, {compat.Group{ABO: "AB", RhPositive: false}, 0.008},
}

// Haftanın günü etkisi: talep ve bağış hafta sonu düşer.
var demandDOW = map[time.Weekday]float64{time.Monday: 1.22, time.Tuesday: 1.12, time.Wednesday: 1.05,
	time.Thursday: 1.0, time.Friday: 0.95, time.Saturday: 0.70, time.Sunday: 0.55}
var donationDOW = map[time.Weekday]float64{time.Monday: 1.1, time.Tuesday: 1.1, time.Wednesday: 1.1,
	time.Thursday: 1.1, time.Friday: 1.0, time.Saturday: 0.8, time.Sunday: 0.4}

var firstM = []string{"Ahmet", "Mehmet", "Mustafa", "Ali", "Hüseyin", "Hasan", "İbrahim", "Murat", "Emre", "Burak", "Kerem", "Yusuf", "Ömer", "Can", "Eren", "Serkan", "Oğuz", "Kaan", "Barış", "Tolga"}
var firstF = []string{"Ayşe", "Fatma", "Zeynep", "Elif", "Emine", "Merve", "Esra", "Büşra", "Selin", "Derya", "Ece", "Damla", "Gizem", "Sude", "İrem", "Buse", "Nur", "Ceren", "Aslı", "Melis"}
var lastNames = []string{"Yılmaz", "Kaya", "Demir", "Şahin", "Çelik", "Yıldız", "Yıldırım", "Öztürk", "Aydın", "Özdemir", "Arslan", "Doğan", "Kılıç", "Aslan", "Çetin", "Kara", "Koç", "Kurt", "Özkan", "Şimşek", "Polat", "Korkmaz", "Erdoğan", "Güneş", "Aksoy", "Bulut", "Tekin", "Uçar", "Avcı", "Boztepe"}
var meds = []string{"levotiroksin", "metformin", "ramipril", "D vitamini", "demir takviyesi", "omeprazol"}

type stockItem struct {
	product int
	expires time.Time
}

type donorState struct {
	lastWB, lastPLT time.Time
	permanent       bool
	deferredUntil   time.Time
}

// Generate veri setini üretir. Saf fonksiyondur: aynı Config aynı Dataset'i verir.
func Generate(c Config) *Dataset {
	rng := rand.New(rand.NewPCG(c.Seed, c.Seed^0x9E3779B97F4A7C15))
	ds := &Dataset{}
	start := c.Anchor.AddDate(0, 0, -c.HistoryDays)

	// ---- bağışçılar ----
	states := make([]donorState, c.Donors+1)
	for i := 1; i <= c.Donors; i++ {
		sex := "M"
		first := firstM[rng.IntN(len(firstM))]
		if rng.Float64() < 0.4 {
			sex, first = "F", firstF[rng.IntN(len(firstF))]
		}
		g := pickGroup(rng)
		age := 18 + rng.IntN(47)
		birth := c.Anchor.AddDate(-age, -rng.IntN(12), -rng.IntN(28))
		h := map[string]any{"medications": []string{}, "conditions": []string{}}
		if rng.Float64() < 0.15 {
			h["medications"] = []string{meds[rng.IntN(len(meds))]}
		}
		hj, _ := json.Marshal(h)
		ds.Donors = append(ds.Donors, Donor{
			N: i, First: first, Last: lastNames[rng.IntN(len(lastNames))], Sex: sex, Birth: birth,
			ABO: g.ABO, Rh: g.RhPositive, Phone: fmt.Sprintf("+90 5%02d %03d %04d", 30+rng.IntN(25), rng.IntN(1000), rng.IntN(10000)),
			HealthJSON: hj, Created: start.AddDate(0, 0, -rng.IntN(700)),
		})
		// Kalıcı ret (%1.5) ve süreli ertelemeler (%4): bilerek uygunsuz bağışçılar.
		switch r := rng.Float64(); {
		case r < 0.015:
			states[i].permanent = true
			ds.Deferrals = append(ds.Deferrals, Deferral{Donor: i, Code: "iv_drug_use", Start: start.AddDate(0, 0, -rng.IntN(300)), Note: "seed"})
		case r < 0.055:
			code, days := "travel_malaria", 365
			if rng.Float64() < 0.5 {
				code, days = "tattoo_piercing", 180
			}
			s := c.Anchor.AddDate(0, 0, -rng.IntN(days))
			e := s.AddDate(0, 0, days)
			states[i].deferredUntil = e
			ds.Deferrals = append(ds.Deferrals, Deferral{Donor: i, Code: code, Start: s, End: &e, Note: "seed"})
		}
	}

	// ---- günlük simülasyon ----
	stock := map[compat.Component]map[compat.Group][]stockItem{"RBC": {}, "PLT": {}, "FFP": {}}
	productIdx := map[int]int{}   // product N → ds.Products index
	pending := []int{}            // test bekleyen bağışlar (N)
	byDonation := map[int][]int{} // bağış N → ürün indeksleri
	patientSeq := 0
	for d := 0; d < c.HistoryDays; d++ {
		today := start.AddDate(0, 0, d)
		dayEnd := today.AddDate(0, 0, 1)

		// Önceki günlerin testleri: toplamadan ~18 saat sonra sonuçlanır.
		var still []int
		for _, dn := range pending {
			don := ds.Donations[dn-1]
			at := don.At.Add(18 * time.Hour)
			if !at.Before(dayEnd) || !at.Before(c.Anchor) {
				still = append(still, dn)
				continue
			}
			t := Test{Donation: dn, At: at}
			if rng.Float64() < 0.004 {
				switch rng.IntN(4) {
				case 0:
					t.HIV = true
				case 1:
					t.HBV = true
				case 2:
					t.HCV = true
				default:
					t.Syphilis = true
				}
			}
			ds.Tests = append(ds.Tests, t)
			positive := t.HIV || t.HBV || t.HCV || t.Syphilis
			if positive {
				states[don.Donor].permanent = true
				ds.Deferrals = append(ds.Deferrals, Deferral{Donor: don.Donor, Code: markerCode(t), Start: day(at), Note: fmt.Sprintf("test:%d", dn)})
			}
			for _, pi := range byDonation[dn] {
				p := &ds.Products[pi]
				if p.Status != "pending_test" {
					continue
				}
				if positive {
					p.Status = "quarantined"
					ds.Events = append(ds.Events, Event{Product: p.N, From: "pending_test", To: "quarantined", At: at, Reason: "test sonucu: positive"})
					disc := at.Add(48 * time.Hour)
					if disc.Before(c.Anchor) {
						p.Status = "discarded"
						ds.Events = append(ds.Events, Event{Product: p.N, From: "quarantined", To: "discarded", At: disc, Reason: "pozitif test imha"})
					}
					continue
				}
				p.Status = "available"
				ds.Events = append(ds.Events, Event{Product: p.N, From: "pending_test", To: "available", At: at, Reason: "test sonucu: negative"})
				comp := compat.Component(p.Component)
				g := compat.Group{ABO: p.ABO, RhPositive: p.Rh}
				stock[comp][g] = append(stock[comp][g], stockItem{product: p.N, expires: p.Expires})
			}
		}
		pending = still

		// Bağışlar
		n := int(math.Round(float64(c.DailyDonations) * donationDOW[today.Weekday()] * (0.85 + 0.3*rng.Float64())))
		for k := 0; k < n; k++ {
			at := today.Add(time.Duration(8*60+rng.IntN(9*60)) * time.Minute)
			donorN, dtype, ok := pickDonor(rng, ds, states, at)
			if !ok {
				ds.Ineligible++
				continue
			}
			dn := len(ds.Donations) + 1
			don := ds.Donors[donorN-1]
			hb := 13.6 + rng.Float64()*2.5
			if don.Sex == "F" {
				hb = 12.6 + rng.Float64()*2.2
			}
			vol := 450
			if dtype == "platelet_apheresis" {
				vol = 250
				states[donorN].lastPLT = at
			} else {
				states[donorN].lastWB = at
			}
			ds.Donations = append(ds.Donations, Donation{N: dn, Donor: donorN, Type: dtype, At: at, Volume: vol,
				Hb: math.Round(hb*10) / 10, Weight: math.Round((55+rng.Float64()*45)*10) / 10})
			comps := []string{"PLT"}
			if dtype == "whole_blood" {
				comps = []string{"RBC", "FFP"}
				if rng.Float64() < 0.25 {
					comps = append(comps, "PLT")
				}
			}
			shelf := map[string]int{"RBC": 42, "PLT": 5, "FFP": 365}
			for _, cmp := range comps {
				pn := len(ds.Products) + 1
				volP := map[string]int{"RBC": 280, "FFP": 220, "PLT": 60}[cmp]
				if dtype == "platelet_apheresis" {
					volP = 250
				}
				p := Product{N: pn, Donation: dn, Component: cmp, ABO: don.ABO, Rh: don.Rh, Volume: volP,
					Produced: at.Add(time.Hour), Expires: at.AddDate(0, 0, shelf[cmp]), Status: "pending_test"}
				if !p.Produced.Before(c.Anchor) {
					continue // bileşen üretimi çapadan sonraya kalırsa canlı simülatör yapar
				}
				ds.Products = append(ds.Products, p)
				productIdx[pn] = len(ds.Products) - 1
				byDonation[dn] = append(byDonation[dn], len(ds.Products)-1)
				ds.Events = append(ds.Events, Event{Product: pn, To: "pending_test", At: p.Produced, Reason: "üretim"})
			}
			pending = append(pending, dn)
		}

		// Talepler ve tahsis
		demand := map[compat.Component]float64{"RBC": c.DailyRBCDemand, "PLT": c.DailyRBCDemand * 0.16, "FFP": c.DailyRBCDemand * 0.22}
		for _, comp := range []compat.Component{"RBC", "PLT", "FFP"} {
			for _, gs := range groupShare {
				share := gs.p
				if !gs.g.RhPositive && gs.g.ABO == "O" {
					share *= 1.6 // O- acil durumlarda evrensel verici olarak fazla talep görür
				}
				units := poisson(rng, demand[comp]*share*demandDOW[today.Weekday()])
				for units > 0 {
					u := 1 + rng.IntN(3)
					if u > units {
						u = units
					}
					units -= u
					reqAt := today.Add(time.Duration(7*60+rng.IntN(14*60)) * time.Minute)
					if !reqAt.Before(c.Anchor) {
						continue
					}
					urg := "routine"
					switch r := rng.Float64(); {
					case r < 0.03:
						urg = "emergency"
					case r < 0.15:
						urg = "urgent"
					}
					patientSeq++
					hosp := 1 + rng.IntN(c.Hospitals)
					rq := Request{N: len(ds.Requests) + 1, Hospital: hosp, Component: string(comp), ABO: gs.g.ABO, Rh: gs.g.RhPositive,
						Units: u, Urgency: urg, Patient: fmt.Sprintf("H%d-P%06d", hosp, patientSeq), At: reqAt}
					got := allocate(stock, comp, gs.g, u, reqAt)
					if len(got) == 0 {
						rq.Status = "cancelled"
						ds.Requests = append(ds.Requests, rq)
						continue
					}
					rq.Units = len(got)
					issueAt := reqAt.Add(time.Duration(10+rng.IntN(15)) * time.Minute)
					hold := reqAt.Add(30 * time.Minute)
					issued := issueAt.Before(c.Anchor)
					rq.Status = "reserved"
					if issued {
						rq.Status = "issued"
					}
					ds.Requests = append(ds.Requests, rq)
					for _, pn := range got {
						p := &ds.Products[productIdx[pn]]
						p.Status = "reserved"
						ds.Events = append(ds.Events, Event{Product: pn, From: "available", To: "reserved", At: reqAt, Reason: "tahsis"})
						res := Reservation{N: len(ds.Reservations) + 1, Request: rq.N, Product: pn, Status: "active", At: reqAt, Expires: hold}
						if issued {
							ia := issueAt
							res.Status, res.Closed = "issued", &ia
							p.Status = "issued"
							ds.Events = append(ds.Events, Event{Product: pn, From: "reserved", To: "issued", At: issueAt, Reason: "dağıtım"})
							ds.Distributions = append(ds.Distributions, Distribution{Request: rq.N, Product: pn, Hospital: hosp, At: issueAt})
							tAt := issueAt.Add(time.Duration(1+rng.IntN(20)) * time.Hour)
							if rng.Float64() < 0.85 && tAt.Before(c.Anchor) {
								p.Status = "transfused"
								ds.Events = append(ds.Events, Event{Product: pn, From: "issued", To: "transfused", At: tAt, Reason: "transfüzyon"})
								ds.Transfusions = append(ds.Transfusions, Transfusion{Product: pn, Hospital: hosp, Patient: rq.Patient, At: tAt})
							}
						}
						ds.Reservations = append(ds.Reservations, res)
					}
				}
			}
		}

		// SKT kayıpları (gün sonunda stokta kalıp süresi dolanlar)
		for _, comp := range []compat.Component{"RBC", "PLT", "FFP"} {
			byGroup := stock[comp]
			for _, g := range compat.AllGroups() {
				items := byGroup[g]
				keep := items[:0]
				for _, it := range items {
					if it.expires.Before(dayEnd) && it.expires.Before(c.Anchor) {
						p := &ds.Products[productIdx[it.product]]
						if p.Status == "available" {
							p.Status = "expired"
							ds.Events = append(ds.Events, Event{Product: it.product, From: "available", To: "expired", At: it.expires, Reason: "son kullanma tarihi geçti"})
						}
						continue
					}
					keep = append(keep, it)
				}
				byGroup[g] = keep
			}
		}
	}
	return ds
}

// allocate uyum sırası + FEFO ile stoktan ürün çeker (canlı sistemle aynı kural).
func allocate(stock map[compat.Component]map[compat.Group][]stockItem, comp compat.Component, g compat.Group, units int, at time.Time) []int {
	var got []int
	for _, cand := range compat.Candidates(comp, g) {
		items := stock[comp][cand.Group]
		sort.SliceStable(items, func(i, j int) bool { return items[i].expires.Before(items[j].expires) })
		keep := items[:0]
		for _, it := range items {
			if len(got) < units && it.expires.After(at.Add(time.Hour)) {
				got = append(got, it.product)
				continue
			}
			keep = append(keep, it)
		}
		stock[comp][cand.Group] = keep
		if len(got) >= units {
			break
		}
	}
	return got
}

func pickDonor(rng *rand.Rand, ds *Dataset, st []donorState, at time.Time) (int, string, bool) {
	for try := 0; try < 6; try++ {
		n := 1 + rng.IntN(len(ds.Donors))
		s := st[n]
		d := ds.Donors[n-1]
		if s.permanent || at.Before(s.deferredUntil) {
			continue
		}
		age := at.Year() - d.Birth.Year()
		if age < 18 || age > 65 {
			continue
		}
		dtype := "whole_blood"
		if rng.Float64() < 0.07 {
			dtype = "platelet_apheresis"
		}
		wbWait := 90
		if d.Sex == "F" {
			wbWait = 120
		}
		var ok bool
		if dtype == "whole_blood" {
			ok = (s.lastWB.IsZero() || !at.Before(day(s.lastWB).AddDate(0, 0, wbWait))) &&
				(s.lastPLT.IsZero() || !at.Before(day(s.lastPLT).AddDate(0, 0, 14)))
		} else {
			ok = (s.lastPLT.IsZero() || !at.Before(day(s.lastPLT).AddDate(0, 0, 14))) &&
				(s.lastWB.IsZero() || !at.Before(day(s.lastWB).AddDate(0, 0, 28)))
		}
		if ok {
			return n, dtype, true
		}
	}
	return 0, "", false
}

func pickGroup(rng *rand.Rand) compat.Group {
	r := rng.Float64()
	acc := 0.0
	for _, gs := range groupShare {
		acc += gs.p
		if r < acc {
			return gs.g
		}
	}
	return groupShare[0].g
}

func poisson(rng *rand.Rand, lambda float64) int {
	if lambda <= 0 {
		return 0
	}
	if lambda > 30 {
		return int(math.Max(0, math.Round(lambda+math.Sqrt(lambda)*rng.NormFloat64())))
	}
	l, k, p := math.Exp(-lambda), 0, 1.0
	for {
		k++
		p *= rng.Float64()
		if p <= l {
			return k - 1
		}
	}
}

func markerCode(t Test) string {
	switch {
	case t.HIV:
		return "hiv_positive"
	case t.HBV:
		return "hbv_positive"
	case t.HCV:
		return "hcv_positive"
	}
	return "syphilis_positive"
}

func day(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

// Fingerprint veri setinin özetini döner; determinizm testleri ve günlük için.
func (ds *Dataset) Fingerprint() string {
	h := sha256.New()
	w := func(v any) { b, _ := json.Marshal(v); h.Write(b) }
	w(ds.Donors)
	w(ds.Deferrals)
	w(ds.Donations)
	w(ds.Tests)
	w(ds.Products)
	w(ds.Events)
	w(ds.Requests)
	w(ds.Reservations)
	w(ds.Distributions)
	w(ds.Transfusions)
	return fmt.Sprintf("%x", h.Sum(nil))[:16]
}

// Nonce sağlık verisi şifrelemesi için bağışçıya özgü deterministik nonce (seed verisi tekrar üretilebilsin).
func Nonce(seed uint64, donor int) []byte {
	b := make([]byte, 16)
	binary.BigEndian.PutUint64(b, seed)
	binary.BigEndian.PutUint64(b[8:], uint64(donor))
	s := sha256.Sum256(append([]byte("damar-health-nonce"), b...))
	return s[:12]
}
