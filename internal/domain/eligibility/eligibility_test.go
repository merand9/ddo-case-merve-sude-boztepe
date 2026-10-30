package eligibility

import (
	"encoding/json"
	"testing"
	"time"
)

// Testler migrasyondaki kural setiyle aynı JSON'u kullanır.
const rulesJSON = `{
  "min_age": 18, "max_age": 65, "min_weight_kg": 50,
  "hb_min": {"M": 13.5, "F": 12.5}, "hb_max": 18.5,
  "intervals_days": {
    "whole_blood": {"whole_blood": {"M": 90, "F": 120}, "platelet_apheresis": {"M": 28, "F": 28}},
    "platelet_apheresis": {"whole_blood": {"M": 14, "F": 14}, "platelet_apheresis": {"M": 14, "F": 14}}
  },
  "max_platelet_per_365": 24,
  "deferrals": {
    "travel_malaria": {"days": 365, "label": "Sıtma"},
    "tattoo_piercing": {"days": 180, "label": "Dövme"},
    "antibiotic": {"days": 14, "label": "Antibiyotik"},
    "aspirin": {"days": 5, "label": "Aspirin", "only": "platelet_apheresis"},
    "hiv_positive": {"permanent": true, "label": "HIV"}
  }
}`

func rules(t *testing.T) Rules {
	t.Helper()
	var r Rules
	if err := json.Unmarshal([]byte(rulesJSON), &r); err != nil {
		t.Fatal(err)
	}
	return r
}

func day(s string) time.Time {
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		panic(err)
	}
	return t
}

func ptr(t time.Time) *time.Time { return &t }

var now = day("2026-10-30")

func baseInput() Input {
	return Input{
		Sex: Male, BirthDate: day("1995-05-10"), Type: WholeBlood, At: now,
		Measurements: &Measurements{WeightKg: 75, Hemoglobin: 14.5},
	}
}

func hasCode(d Decision, code string) bool {
	for _, r := range d.Reasons {
		if r.Code == code {
			return true
		}
	}
	return false
}

func TestEligibleHealthyFirstTimeDonor(t *testing.T) {
	d := EvaluateWith(baseInput(), rules(t))
	if !d.Eligible || len(d.Reasons) != 0 {
		t.Fatalf("uygun olmalıydı: %+v", d)
	}
}

func TestWaitingPeriodMale90Days(t *testing.T) {
	r := rules(t)
	in := baseInput()
	in.History = []PriorDonation{{Type: WholeBlood, At: now.AddDate(0, 0, -89)}}
	d := EvaluateWith(in, r)
	if d.Eligible || !hasCode(d, CodeInterval) {
		t.Fatalf("89. günde uygun olmamalı: %+v", d)
	}
	if want := now.AddDate(0, 0, 1); !d.NextEligibleAt.Equal(want) {
		t.Fatalf("sonraki uygun gün %v, beklenen %v", d.NextEligibleAt, want)
	}
	in.History = []PriorDonation{{Type: WholeBlood, At: now.AddDate(0, 0, -90)}}
	if d := EvaluateWith(in, r); !d.Eligible {
		t.Fatalf("90. günde uygun olmalı: %+v", d)
	}
}

func TestWaitingPeriodFemale120Days(t *testing.T) {
	r := rules(t)
	in := baseInput()
	in.Sex = Female
	in.Measurements.Hemoglobin = 13
	in.History = []PriorDonation{{Type: WholeBlood, At: now.AddDate(0, 0, -100)}}
	d := EvaluateWith(in, r)
	if d.Eligible || !hasCode(d, CodeInterval) {
		t.Fatalf("kadın bağışçı 100. günde uygun olmamalı: %+v", d)
	}
	in.History[0].At = now.AddDate(0, 0, -120)
	if d := EvaluateWith(in, r); !d.Eligible {
		t.Fatalf("120. günde uygun olmalı: %+v", d)
	}
}

func TestPlateletApheresis14Days(t *testing.T) {
	r := rules(t)
	in := baseInput()
	in.Type = Platelet
	in.History = []PriorDonation{{Type: Platelet, At: now.AddDate(0, 0, -13)}}
	if d := EvaluateWith(in, r); d.Eligible {
		t.Fatalf("trombosit sonrası 13. günde uygun olmamalı")
	}
	in.History[0].At = now.AddDate(0, 0, -14)
	if d := EvaluateWith(in, r); !d.Eligible {
		t.Fatalf("14. günde uygun olmalı: %+v", d)
	}
}

func TestWholeBloodAfterPlateletUsesPlateletInterval(t *testing.T) {
	r := rules(t)
	in := baseInput()
	in.History = []PriorDonation{{Type: Platelet, At: now.AddDate(0, 0, -15)}}
	if d := EvaluateWith(in, r); !d.Eligible {
		t.Fatalf("trombositten 15 gün sonra tam kan uygun olmalı: %+v", d)
	}
}

func TestPlateletAfterWholeBlood(t *testing.T) {
	r := rules(t)
	in := baseInput()
	in.Type = Platelet
	in.History = []PriorDonation{{Type: WholeBlood, At: now.AddDate(0, 0, -20)}}
	d := EvaluateWith(in, r)
	if d.Eligible || !hasCode(d, CodeInterval) {
		t.Fatalf("tam kandan 20 gün sonra trombosit uygun olmamalı: %+v", d)
	}
}

func TestPlateletYearlyLimit(t *testing.T) {
	r := rules(t)
	r.MaxPlateletPer365 = 3
	in := baseInput()
	in.Type = Platelet
	in.History = []PriorDonation{
		{Type: Platelet, At: now.AddDate(0, 0, -300)},
		{Type: Platelet, At: now.AddDate(0, 0, -200)},
		{Type: Platelet, At: now.AddDate(0, 0, -100)},
	}
	d := EvaluateWith(in, r)
	if d.Eligible || !hasCode(d, CodePlateletLimit) {
		t.Fatalf("yıllık sınır aşılmalı: %+v", d)
	}
	if want := now.AddDate(0, 0, -300).AddDate(0, 0, 365); !d.NextEligibleAt.Equal(want) {
		t.Fatalf("sonraki uygun gün %v, beklenen %v", d.NextEligibleAt, want)
	}
}

func TestAgeLimits(t *testing.T) {
	r := rules(t)
	in := baseInput()
	in.BirthDate = day("2008-11-15") // 17 yaş
	d := EvaluateWith(in, r)
	if d.Eligible || !hasCode(d, CodeTooYoung) {
		t.Fatalf("17 yaş uygun olmamalı: %+v", d)
	}
	if want := day("2026-11-15"); !d.NextEligibleAt.Equal(want) {
		t.Fatalf("18. doğum günü beklenirdi: %v", d.NextEligibleAt)
	}
	in.BirthDate = day("1960-01-01") // 66 yaş
	d = EvaluateWith(in, r)
	if d.Eligible || !hasCode(d, CodeTooOld) || d.NextEligibleAt != nil {
		t.Fatalf("66 yaş kalıcı olarak uygun olmamalı: %+v", d)
	}
}

func TestAgeOnBirthdayBoundary(t *testing.T) {
	if got := AgeOn(day("2008-10-30"), now); got != 18 {
		t.Fatalf("doğum gününde yaş 18 olmalı, %d", got)
	}
	if got := AgeOn(day("2008-10-31"), now); got != 17 {
		t.Fatalf("doğum gününden bir gün önce 17 olmalı, %d", got)
	}
}

func TestWeightAndHemoglobin(t *testing.T) {
	r := rules(t)
	in := baseInput()
	in.Measurements = &Measurements{WeightKg: 48, Hemoglobin: 13.0}
	d := EvaluateWith(in, r)
	if !hasCode(d, CodeLowWeight) || !hasCode(d, CodeLowHb) {
		t.Fatalf("kilo ve Hb gerekçeleri birlikte dönmeli: %+v", d)
	}
	if d.NextEligibleAt != nil {
		t.Fatalf("ölçüm retlerinde tarih verilmemeli")
	}
	in.Sex = Female
	in.Measurements = &Measurements{WeightKg: 60, Hemoglobin: 13.0}
	if d := EvaluateWith(in, r); !d.Eligible {
		t.Fatalf("kadında 13.0 Hb yeterli: %+v", d)
	}
	in.Measurements.Hemoglobin = 19
	if d := EvaluateWith(in, r); !hasCode(d, CodeHighHb) {
		t.Fatalf("yüksek Hb reddedilmeli")
	}
}

func TestTemporaryDeferralAndPermanent(t *testing.T) {
	r := rules(t)
	in := baseInput()
	in.Deferrals = []Deferral{{Code: "antibiotic", Start: now.AddDate(0, 0, -3), End: ptr(now.AddDate(0, 0, 11))}}
	d := EvaluateWith(in, r)
	if d.Eligible || !hasCode(d, CodeDeferral) || !d.NextEligibleAt.Equal(now.AddDate(0, 0, 11)) {
		t.Fatalf("süreli erteleme bekleniyordu: %+v", d)
	}
	in.Deferrals = []Deferral{{Code: "hiv_positive", Start: now.AddDate(-1, 0, 0)}}
	d = EvaluateWith(in, r)
	if d.Eligible || !hasCode(d, CodePermanent) || d.NextEligibleAt != nil {
		t.Fatalf("kalıcı red bekleniyordu: %+v", d)
	}
}

func TestExpiredDeferralIgnored(t *testing.T) {
	in := baseInput()
	in.Deferrals = []Deferral{{Code: "antibiotic", Start: now.AddDate(0, 0, -30), End: ptr(now.AddDate(0, 0, -16))}}
	if d := EvaluateWith(in, rules(t)); !d.Eligible {
		t.Fatalf("süresi bitmiş erteleme dikkate alınmamalı: %+v", d)
	}
}

func TestTypeSpecificDeferral(t *testing.T) {
	r := rules(t)
	in := baseInput()
	in.Declarations = []Declaration{{Code: "aspirin", Date: now.AddDate(0, 0, -1)}}
	if d := EvaluateWith(in, r); !d.Eligible {
		t.Fatalf("aspirin tam kan bağışını engellememeli: %+v", d)
	}
	in.Type = Platelet
	if d := EvaluateWith(in, r); d.Eligible {
		t.Fatalf("aspirin trombosit aferezini engellemeli")
	}
}

func TestTravelDeclaration(t *testing.T) {
	in := baseInput()
	in.Declarations = []Declaration{{Code: "travel_malaria", Date: now.AddDate(0, -3, 0)}}
	d := EvaluateWith(in, rules(t))
	if d.Eligible || !hasCode(d, CodeDeferral) {
		t.Fatalf("seyahat ertelemesi bekleniyordu: %+v", d)
	}
	if want := now.AddDate(0, -3, 0).AddDate(0, 0, 365); !d.NextEligibleAt.Equal(want) {
		t.Fatalf("bitiş %v, beklenen %v", d.NextEligibleAt, want)
	}
}

func TestUnknownDeclarationRejected(t *testing.T) {
	in := baseInput()
	in.Declarations = []Declaration{{Code: "nope", Date: now}}
	if d := EvaluateWith(in, rules(t)); d.Eligible || !hasCode(d, CodeUnknownDeclare) {
		t.Fatalf("tanımsız beyan reddedilmeli: %+v", d)
	}
}

func TestMultipleReasonsNextEligibleIsLatest(t *testing.T) {
	in := baseInput()
	in.History = []PriorDonation{{Type: WholeBlood, At: now.AddDate(0, 0, -80)}}              // +10 gün
	in.Declarations = []Declaration{{Code: "tattoo_piercing", Date: now.AddDate(0, 0, -100)}} // +80 gün
	d := EvaluateWith(in, rules(t))
	if len(d.Reasons) != 2 {
		t.Fatalf("iki gerekçe bekleniyordu: %+v", d.Reasons)
	}
	if want := now.AddDate(0, 0, 80); !d.NextEligibleAt.Equal(want) {
		t.Fatalf("en geç tarih seçilmeli: %v", d.NextEligibleAt)
	}
}

func TestInvalidType(t *testing.T) {
	in := baseInput()
	in.Type = "plasma"
	if d := EvaluateWith(in, rules(t)); d.Eligible || !hasCode(d, CodeInvalidType) {
		t.Fatalf("geçersiz tür reddedilmeli")
	}
}

func TestRuleBookTimeValidity(t *testing.T) {
	old := rules(t)
	newer := rules(t)
	newer.IntervalsDays[WholeBlood][WholeBlood][Female] = 100
	cut := day("2027-01-01")
	book := RuleBook{
		{ValidFrom: day("2020-01-01"), ValidTo: &cut, Rules: old},
		{ValidFrom: cut, Rules: newer},
	}
	in := baseInput()
	in.Sex = Female
	in.Measurements.Hemoglobin = 13
	last := day("2026-09-01")
	in.History = []PriorDonation{{Type: WholeBlood, At: last}}

	in.At = day("2026-12-20") // eski kural: 120 gün → uygun değil (110 gün)
	d := Evaluate(in, book)
	if d.Eligible || d.RuleVersion != "2020-01-01" {
		t.Fatalf("eski kural uygulanmalı: %+v", d)
	}
	in.At = day("2027-01-05") // yeni kural: 100 gün → uygun (126 gün)
	d = Evaluate(in, book)
	if !d.Eligible || d.RuleVersion != "2027-01-01" {
		t.Fatalf("yeni kural uygulanmalı: %+v", d)
	}
	in.At = day("2019-06-01")
	if d := Evaluate(in, book); d.Eligible || !hasCode(d, CodeNoRules) {
		t.Fatalf("kural öncesi tarih reddedilmeli")
	}
}

func TestDeferralEndUsesRuleAtStart(t *testing.T) {
	old := rules(t)
	newer := rules(t)
	newer.Deferrals["tattoo_piercing"] = DeferralRule{Days: 120, Label: "Dövme"}
	cut := day("2027-01-01")
	book := RuleBook{{ValidFrom: day("2020-01-01"), ValidTo: &cut, Rules: old}, {ValidFrom: cut, Rules: newer}}

	end, err := DeferralEnd(book, "tattoo_piercing", day("2026-12-01"))
	if err != nil || !end.Equal(day("2026-12-01").AddDate(0, 0, 180)) {
		t.Fatalf("eski kuralın 180 günü uygulanmalı: %v %v", end, err)
	}
	end, _ = DeferralEnd(book, "tattoo_piercing", day("2027-02-01"))
	if !end.Equal(day("2027-02-01").AddDate(0, 0, 120)) {
		t.Fatalf("yeni kuralın 120 günü uygulanmalı: %v", end)
	}
	if end, _ := DeferralEnd(book, "hiv_positive", now); end != nil {
		t.Fatalf("kalıcı ertelemenin bitişi olmamalı")
	}
	if _, err := DeferralEnd(book, "nope", now); err == nil {
		t.Fatalf("tanımsız kod hata vermeli")
	}
	if _, err := DeferralEnd(book, "antibiotic", day("2010-01-01")); err == nil {
		t.Fatalf("kural yokken hata vermeli")
	}
}

func TestFutureDonationInHistoryIgnored(t *testing.T) {
	in := baseInput()
	in.History = []PriorDonation{{Type: WholeBlood, At: now.AddDate(0, 0, 5)}}
	if d := EvaluateWith(in, rules(t)); !d.Eligible {
		t.Fatalf("karar anından sonraki kayıt geçmiş sayılmamalı: %+v", d)
	}
}
