// Package eligibility bağışçı uygunluk kurallarını saf (yan etkisiz) fonksiyonlarla uygular.
//
// Kurallar zaman geçerlilikli sürümler halinde gelir (RuleBook); bir karar, karar anında
// geçerli olan sürüme göre verilir ve hangi sürümün kullanıldığı karara yazılır.
package eligibility

import (
	"fmt"
	"sort"
	"time"
)

// Sex bağışçının biyolojik cinsiyeti (bekleme süreleri ve Hb sınırları için).
type Sex string

const (
	Male   Sex = "M"
	Female Sex = "F"
)

// DonationType bağış türü.
type DonationType string

const (
	WholeBlood DonationType = "whole_blood"
	Platelet   DonationType = "platelet_apheresis"
)

// Valid türün tanımlı olup olmadığını döner.
func (t DonationType) Valid() bool { return t == WholeBlood || t == Platelet }

// DeferralRule bir erteleme nedeninin süresini tanımlar.
type DeferralRule struct {
	Days      int    `json:"days,omitempty"`
	Permanent bool   `json:"permanent,omitempty"`
	Label     string `json:"label"`
	// Only boş değilse erteleme yalnızca bu bağış türü için geçerlidir (ör. aspirin → trombosit).
	Only DonationType `json:"only,omitempty"`
}

// Rules tek bir kural sürümüdür. JSON alanları rule_versions.params ile birebir eşleşir.
type Rules struct {
	MinAge            int                                           `json:"min_age"`
	MaxAge            int                                           `json:"max_age"`
	MinWeightKg       float64                                       `json:"min_weight_kg"`
	HbMin             map[Sex]float64                               `json:"hb_min"`
	HbMax             float64                                       `json:"hb_max"`
	IntervalsDays     map[DonationType]map[DonationType]map[Sex]int `json:"intervals_days"`
	MaxPlateletPer365 int                                           `json:"max_platelet_per_365"`
	Deferrals         map[string]DeferralRule                       `json:"deferrals"`
}

// Version geçerlilik aralığıyla birlikte bir kural sürümüdür. ValidTo nil ise açık uçludur.
type Version struct {
	ValidFrom time.Time
	ValidTo   *time.Time
	Rules     Rules
}

// RuleBook sürümlerin listesidir.
type RuleBook []Version

// At verilen anda geçerli sürümü döner.
func (b RuleBook) At(t time.Time) (Version, bool) {
	day := truncDay(t)
	for _, v := range b {
		if !day.Before(truncDay(v.ValidFrom)) && (v.ValidTo == nil || day.Before(truncDay(*v.ValidTo))) {
			return v, true
		}
	}
	return Version{}, false
}

// PriorDonation bağışçının geçmiş bir bağışı.
type PriorDonation struct {
	Type DonationType
	At   time.Time
}

// Deferral kayıtlı bir erteleme. End nil ise kalıcıdır.
type Deferral struct {
	Code  string
	Start time.Time
	End   *time.Time
}

// Declaration muayene sırasında beyan edilen (henüz kayda geçmemiş) olay: ör. "travel_malaria" 3 ay önce.
type Declaration struct {
	Code string    `json:"code"`
	Date time.Time `json:"date"`
}

// Measurements muayene ölçümleri.
type Measurements struct {
	WeightKg   float64 `json:"weight_kg"`
	Hemoglobin float64 `json:"hemoglobin"`
}

// Input bir uygunluk değerlendirmesinin tüm girdileri.
type Input struct {
	Sex          Sex
	BirthDate    time.Time
	Type         DonationType
	At           time.Time
	History      []PriorDonation
	Deferrals    []Deferral
	Declarations []Declaration
	Measurements *Measurements
}

// Reason kodları (API ve testler bu kodlara güvenir).
const (
	CodeNoRules        = "NO_RULES"
	CodeInvalidType    = "INVALID_TYPE"
	CodeTooYoung       = "AGE_TOO_YOUNG"
	CodeTooOld         = "AGE_TOO_OLD"
	CodeLowWeight      = "WEIGHT_TOO_LOW"
	CodeLowHb          = "HB_TOO_LOW"
	CodeHighHb         = "HB_TOO_HIGH"
	CodeInterval       = "INTERVAL_NOT_ELAPSED"
	CodePlateletLimit  = "PLATELET_YEARLY_LIMIT"
	CodeDeferral       = "DEFERRED"
	CodePermanent      = "PERMANENTLY_DEFERRED"
	CodeUnknownDeclare = "UNKNOWN_DECLARATION"
)

// Reason bir red gerekçesi. Until geçici retlerde bağışın yeniden mümkün olduğu gündür.
type Reason struct {
	Code      string     `json:"code"`
	Message   string     `json:"message"`
	Until     *time.Time `json:"until,omitempty"`
	Permanent bool       `json:"permanent,omitempty"`
}

// Decision değerlendirmenin sonucu.
type Decision struct {
	Eligible       bool       `json:"eligible"`
	Reasons        []Reason   `json:"reasons"`
	NextEligibleAt *time.Time `json:"next_eligible_at,omitempty"`
	RuleVersion    string     `json:"rule_version"`
	EvaluatedAt    time.Time  `json:"evaluated_at"`
}

// Evaluate kural defterinden karar anındaki sürümü seçip değerlendirir.
func Evaluate(in Input, book RuleBook) Decision {
	v, ok := book.At(in.At)
	if !ok {
		return Decision{
			Reasons:     []Reason{{Code: CodeNoRules, Message: "Bu tarih için geçerli kural sürümü yok"}},
			EvaluatedAt: in.At,
		}
	}
	d := EvaluateWith(in, v.Rules)
	d.RuleVersion = v.ValidFrom.Format("2006-01-02")
	return d
}

// EvaluateWith tek bir kural sürümüyle değerlendirir. Tüm kurallar kontrol edilir ve
// her ihlal ayrı bir gerekçe olarak döner (ilk hatada durulmaz).
func EvaluateWith(in Input, r Rules) Decision {
	d := Decision{EvaluatedAt: in.At, Reasons: []Reason{}}
	today := truncDay(in.At)
	add := func(rs Reason) { d.Reasons = append(d.Reasons, rs) }

	if !in.Type.Valid() {
		add(Reason{Code: CodeInvalidType, Message: fmt.Sprintf("Bilinmeyen bağış türü: %q", in.Type)})
		return finish(d)
	}

	// Yaş
	age := AgeOn(in.BirthDate, today)
	if age < r.MinAge {
		until := truncDay(in.BirthDate).AddDate(r.MinAge, 0, 0)
		add(Reason{Code: CodeTooYoung, Message: fmt.Sprintf("Yaş %d; en az %d olmalı", age, r.MinAge), Until: &until})
	}
	if r.MaxAge > 0 && age > r.MaxAge {
		add(Reason{Code: CodeTooOld, Message: fmt.Sprintf("Yaş %d; en fazla %d olabilir", age, r.MaxAge), Permanent: true})
	}

	// Ölçümler (verildiyse)
	if m := in.Measurements; m != nil {
		if m.WeightKg < r.MinWeightKg {
			add(Reason{Code: CodeLowWeight, Message: fmt.Sprintf("Kilo %.1f kg; en az %.0f kg olmalı", m.WeightKg, r.MinWeightKg)})
		}
		if min, ok := r.HbMin[in.Sex]; ok && m.Hemoglobin < min {
			add(Reason{Code: CodeLowHb, Message: fmt.Sprintf("Hemoglobin %.1f g/dL; en az %.1f olmalı", m.Hemoglobin, min)})
		}
		if r.HbMax > 0 && m.Hemoglobin > r.HbMax {
			add(Reason{Code: CodeHighHb, Message: fmt.Sprintf("Hemoglobin %.1f g/dL; en fazla %.1f olabilir", m.Hemoglobin, r.HbMax)})
		}
	}

	// Son bağıştan bu yana bekleme süresi: her önceki bağış türü için ayrı süre uygulanır.
	for _, prevType := range []DonationType{WholeBlood, Platelet} {
		last, ok := lastOf(in.History, prevType, today)
		if !ok {
			continue
		}
		days := r.IntervalsDays[prevType][in.Type][in.Sex]
		if days <= 0 {
			continue
		}
		until := truncDay(last).AddDate(0, 0, days)
		if today.Before(until) {
			add(Reason{
				Code: CodeInterval,
				Message: fmt.Sprintf("Son %s bağışından (%s) sonra %d gün beklenmeli; %d gün geçti",
					typeLabel(prevType), last.Format("2006-01-02"), days, daysBetween(truncDay(last), today)),
				Until: &until,
			})
		}
	}

	// Trombosit aferezi yıllık üst sınırı
	if in.Type == Platelet && r.MaxPlateletPer365 > 0 {
		var within []time.Time
		windowStart := today.AddDate(0, 0, -365)
		for _, h := range in.History {
			if h.Type == Platelet && h.At.After(windowStart) && !truncDay(h.At).After(today) {
				within = append(within, truncDay(h.At))
			}
		}
		if len(within) >= r.MaxPlateletPer365 {
			sort.Slice(within, func(i, j int) bool { return within[i].Before(within[j]) })
			// Penceredeki en eski bağış 365 günü doldurduğunda bir hak açılır.
			until := within[len(within)-r.MaxPlateletPer365].AddDate(0, 0, 365)
			add(Reason{Code: CodePlateletLimit,
				Message: fmt.Sprintf("Son 365 günde %d trombosit bağışı; üst sınır %d", len(within), r.MaxPlateletPer365),
				Until:   &until})
		}
	}

	// Kayıtlı ertelemeler
	for _, df := range in.Deferrals {
		rule, known := r.Deferrals[df.Code]
		if known && rule.Only != "" && rule.Only != in.Type {
			continue
		}
		label := df.Code
		if known {
			label = rule.Label
		}
		if df.End == nil {
			if truncDay(df.Start).After(today) {
				continue
			}
			add(Reason{Code: CodePermanent, Message: "Kalıcı red: " + label, Permanent: true})
			continue
		}
		end := truncDay(*df.End)
		if !today.Before(truncDay(df.Start)) && today.Before(end) {
			add(Reason{Code: CodeDeferral, Message: fmt.Sprintf("Süreli erteleme: %s", label), Until: &end})
		}
	}

	// Muayenede yapılan beyanlar: güncel kural sürümündeki sürelere göre değerlendirilir.
	for _, dec := range in.Declarations {
		rule, ok := r.Deferrals[dec.Code]
		if !ok {
			add(Reason{Code: CodeUnknownDeclare, Message: fmt.Sprintf("Tanımsız beyan kodu: %q", dec.Code)})
			continue
		}
		if rule.Only != "" && rule.Only != in.Type {
			continue
		}
		if rule.Permanent {
			add(Reason{Code: CodePermanent, Message: "Kalıcı red: " + rule.Label, Permanent: true})
			continue
		}
		until := truncDay(dec.Date).AddDate(0, 0, rule.Days)
		if today.Before(until) {
			add(Reason{Code: CodeDeferral, Message: fmt.Sprintf("Süreli erteleme: %s", rule.Label), Until: &until})
		}
	}

	return finish(d)
}

// DeferralEnd bir erteleme kaydının bitiş gününü, erteleme başlangıcında geçerli kurala göre hesaplar.
// Kalıcı ertelemelerde nil döner. Kural sonradan değişse de kayıtlı erteleme etkilenmez.
func DeferralEnd(book RuleBook, code string, start time.Time) (*time.Time, error) {
	v, ok := book.At(start)
	if !ok {
		return nil, fmt.Errorf("%s için geçerli kural sürümü yok", start.Format("2006-01-02"))
	}
	rule, ok := v.Rules.Deferrals[code]
	if !ok {
		return nil, fmt.Errorf("tanımsız erteleme kodu: %q", code)
	}
	if rule.Permanent {
		return nil, nil
	}
	end := truncDay(start).AddDate(0, 0, rule.Days)
	return &end, nil
}

func finish(d Decision) Decision {
	d.Eligible = len(d.Reasons) == 0
	if d.Eligible {
		return d
	}
	// Bir sonraki uygun gün: tüm ret gerekçeleri tarihli ve kalıcı değilse en geç tarih.
	var next *time.Time
	for _, r := range d.Reasons {
		if r.Permanent || r.Until == nil {
			return d
		}
		if next == nil || r.Until.After(*next) {
			u := *r.Until
			next = &u
		}
	}
	d.NextEligibleAt = next
	return d
}

// AgeOn verilen gündeki tam yaşı hesaplar.
func AgeOn(birth, day time.Time) int {
	birth, day = truncDay(birth), truncDay(day)
	age := day.Year() - birth.Year()
	if day.Month() < birth.Month() || (day.Month() == birth.Month() && day.Day() < birth.Day()) {
		age--
	}
	return age
}

func lastOf(h []PriorDonation, t DonationType, notAfter time.Time) (time.Time, bool) {
	var last time.Time
	found := false
	for _, d := range h {
		if d.Type != t || truncDay(d.At).After(notAfter) {
			continue
		}
		if !found || d.At.After(last) {
			last, found = d.At, true
		}
	}
	return last, found
}

func typeLabel(t DonationType) string {
	if t == Platelet {
		return "trombosit aferezi"
	}
	return "tam kan"
}

func daysBetween(a, b time.Time) int { return int(b.Sub(a).Hours() / 24) }

func truncDay(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}
