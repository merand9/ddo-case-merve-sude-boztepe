// Package compat ABO/Rh uyum matrisini ve tahsis sıralamasını tanımlar.
package compat

import (
	"fmt"
	"sort"
)

// ABO kan grupları.
const (
	O  = "O"
	A  = "A"
	B  = "B"
	AB = "AB"
)

// Component kan ürünü bileşeni.
type Component string

const (
	RBC Component = "RBC" // eritrosit süspansiyonu
	PLT Component = "PLT" // trombosit
	FFP Component = "FFP" // taze donmuş plazma
)

// Valid bileşenin tanımlı olup olmadığını döner.
func (c Component) Valid() bool { return c == RBC || c == PLT || c == FFP }

// Group ABO + Rh.
type Group struct {
	ABO        string `json:"abo"`
	RhPositive bool   `json:"rh_positive"`
}

// String "A+" biçiminde yazar.
func (g Group) String() string {
	if g.RhPositive {
		return g.ABO + "+"
	}
	return g.ABO + "-"
}

// ParseGroup "AB-" gibi bir metni çözer.
func ParseGroup(s string) (Group, error) {
	if len(s) < 2 {
		return Group{}, fmt.Errorf("geçersiz kan grubu: %q", s)
	}
	abo, rh := s[:len(s)-1], s[len(s)-1]
	if !ValidABO(abo) || (rh != '+' && rh != '-') {
		return Group{}, fmt.Errorf("geçersiz kan grubu: %q", s)
	}
	return Group{ABO: abo, RhPositive: rh == '+'}, nil
}

// ValidABO ABO değerini doğrular.
func ValidABO(s string) bool { return s == O || s == A || s == B || s == AB }

// AllGroups sekiz kan grubunu sabit sırayla döner.
func AllGroups() []Group {
	var out []Group
	for _, abo := range []string{O, A, B, AB} {
		for _, rh := range []bool{false, true} {
			out = append(out, Group{ABO: abo, RhPositive: rh})
		}
	}
	return out
}

// antigens bir ABO grubunun eritrosit antijenleri.
func antigens(abo string) map[byte]bool {
	switch abo {
	case A:
		return map[byte]bool{'A': true}
	case B:
		return map[byte]bool{'B': true}
	case AB:
		return map[byte]bool{'A': true, 'B': true}
	}
	return map[byte]bool{}
}

// redCellABO: verici eritrositindeki her antijen alıcıda da bulunmalı.
func redCellABO(donor, recipient string) bool {
	ra := antigens(recipient)
	for ag := range antigens(donor) {
		if !ra[ag] {
			return false
		}
	}
	return true
}

// plasmaABO: plazmadaki antikorlar alıcı antijenleriyle çatışmamalı (eritrositin tersi).
func plasmaABO(donor, recipient string) bool { return redCellABO(recipient, donor) }

// Compatible verilen bileşen için verici grubunun alıcıya verilebilir olup olmadığını döner.
//   - RBC: ABO eritrosit uyumu; Rh negatif alıcıya yalnızca Rh negatif.
//   - FFP: ABO plazma uyumu; Rh önemsiz.
//   - PLT: plazma uyumlu tercih edilir; ABO uyumsuz trombosit de verilebilir (son kademe).
//     Rh negatif alıcıya yalnızca Rh negatif (alloimmünizasyonu önlemek için).
func Compatible(c Component, donor, recipient Group) bool {
	switch c {
	case RBC:
		return redCellABO(donor.ABO, recipient.ABO) && (recipient.RhPositive || !donor.RhPositive)
	case FFP:
		return plasmaABO(donor.ABO, recipient.ABO)
	case PLT:
		return recipient.RhPositive || !donor.RhPositive
	}
	return false
}

// Candidate tahsis için uygun bir verici grubu ve öncelik sırası (0 = tam uyum).
type Candidate struct {
	Group Group `json:"group"`
	Rank  int   `json:"rank"`
	Exact bool  `json:"exact"`
}

// Candidates alıcı için uygun verici gruplarını tercih sırasıyla döner.
// Sıralama: önce tam uyum; sonra aynı ABO farklı Rh; sonra farklı ABO. O grubu (evrensel
// verici) eritrositte en sona bırakılır ki kıt O stoğu gerçekten gereken hastaya kalsın.
// Plazmada AB (evrensel plazma) aynı mantıkla en sona bırakılır.
func Candidates(c Component, recipient Group) []Candidate {
	var out []Candidate
	for _, g := range AllGroups() {
		if !Compatible(c, g, recipient) {
			continue
		}
		out = append(out, Candidate{Group: g, Rank: rank(c, g, recipient), Exact: g == recipient})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Rank < out[j].Rank })
	return out
}

func rank(c Component, donor, recipient Group) int {
	if donor == recipient {
		return 0
	}
	r := 1
	if donor.ABO != recipient.ABO {
		r += 2
		if c == RBC && donor.ABO == O {
			r += 2
		}
		if c == FFP && donor.ABO == AB {
			r += 2
		}
		if c == PLT && !plasmaABO(donor.ABO, recipient.ABO) {
			r += 4 // ABO uyumsuz trombosit son çare
		}
	}
	if donor.RhPositive != recipient.RhPositive {
		r++
	}
	return r
}
