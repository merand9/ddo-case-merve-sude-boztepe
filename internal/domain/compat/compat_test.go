package compat

import (
	"reflect"
	"testing"
)

func g(s string) Group {
	gr, err := ParseGroup(s)
	if err != nil {
		panic(err)
	}
	return gr
}

func names(cs []Candidate) []string {
	var out []string
	for _, c := range cs {
		out = append(out, c.Group.String())
	}
	return out
}

// Klasik eritrosit uyum matrisi: satır alıcı, sütun verici.
func TestRBCMatrix(t *testing.T) {
	donors := []string{"O-", "O+", "A-", "A+", "B-", "B+", "AB-", "AB+"}
	matrix := map[string]string{
		"O-":  "10000000",
		"O+":  "11000000",
		"A-":  "10100000",
		"A+":  "11110000",
		"B-":  "10001000",
		"B+":  "11001100",
		"AB-": "10101010",
		"AB+": "11111111",
	}
	for rec, row := range matrix {
		for i, d := range donors {
			want := row[i] == '1'
			if got := Compatible(RBC, g(d), g(rec)); got != want {
				t.Errorf("RBC %s → %s: %v, beklenen %v", d, rec, got, want)
			}
		}
	}
}

func TestPlasmaIsReverseAndIgnoresRh(t *testing.T) {
	if !Compatible(FFP, g("AB+"), g("O-")) {
		t.Error("AB plazma herkese verilebilir")
	}
	if Compatible(FFP, g("O+"), g("A+")) {
		t.Error("O plazma A alıcıya verilemez")
	}
	if !Compatible(FFP, g("A-"), g("O+")) {
		t.Error("A plazma O alıcıya verilebilir; Rh önemsiz")
	}
}

func TestPlateletRhNegativeProtected(t *testing.T) {
	if Compatible(PLT, g("A+"), g("A-")) {
		t.Error("Rh negatif alıcıya Rh pozitif trombosit verilmemeli")
	}
	if !Compatible(PLT, g("B-"), g("A+")) {
		t.Error("ABO uyumsuz trombosit son kademe olarak verilebilir")
	}
}

func TestCandidateOrderExactFirstOLast(t *testing.T) {
	got := names(Candidates(RBC, g("A+")))
	want := []string{"A+", "A-", "O+", "O-"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("A+ için sıra %v, beklenen %v", got, want)
	}
	if c := Candidates(RBC, g("A+")); !c[0].Exact || c[1].Exact {
		t.Fatal("yalnızca ilk aday tam uyum olmalı")
	}
}

func TestCandidateUniversalRecipient(t *testing.T) {
	got := names(Candidates(RBC, g("AB+")))
	if got[0] != "AB+" || got[len(got)-1] != "O-" || len(got) != 8 {
		t.Fatalf("AB+ için sıra beklenmedik: %v", got)
	}
}

func TestCandidateONegativeOnlyONegative(t *testing.T) {
	if got := names(Candidates(RBC, g("O-"))); !reflect.DeepEqual(got, []string{"O-"}) {
		t.Fatalf("O- yalnızca O- alabilir: %v", got)
	}
}

func TestPlasmaCandidatesABLast(t *testing.T) {
	got := names(Candidates(FFP, g("A+")))
	if got[0] != "A+" || got[len(got)-1] != "AB-" && got[len(got)-1] != "AB+" {
		t.Fatalf("plazmada AB en sonda olmalı: %v", got)
	}
}

func TestPlateletIncompatibleLast(t *testing.T) {
	got := Candidates(PLT, g("O+"))
	last := got[len(got)-1]
	if last.Group.ABO == O {
		t.Fatalf("ABO uyumsuz trombosit sonda olmalı: %v", names(got))
	}
}

func TestParseGroup(t *testing.T) {
	for _, bad := range []string{"", "C+", "A", "AB*", "+"} {
		if _, err := ParseGroup(bad); err == nil {
			t.Errorf("%q reddedilmeli", bad)
		}
	}
	if g := g("AB-"); g.ABO != AB || g.RhPositive {
		t.Fatal("AB- çözümlenemedi")
	}
	if !RBC.Valid() || Component("X").Valid() {
		t.Fatal("bileşen doğrulaması hatalı")
	}
	if Compatible(Component("X"), g("O-"), g("O-")) {
		t.Fatal("bilinmeyen bileşen uyumlu sayılmamalı")
	}
}
