// Package product kan ürünü yaşam döngüsü, raf ömrü ve imzalı barkod kurallarını içerir.
package product

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base32"
	"fmt"
	"strings"
	"time"
)

// Status ürün durumu.
type Status string

const (
	PendingTest Status = "pending_test"
	Available   Status = "available"
	Reserved    Status = "reserved"
	Issued      Status = "issued"
	Transfused  Status = "transfused"
	Quarantined Status = "quarantined"
	Discarded   Status = "discarded"
	Expired     Status = "expired"
	Split       Status = "split"
)

// transitions veritabanındaki product_transitions tablosunun aynısıdır; API katmanı
// veritabanına gitmeden anlamlı hata dönebilsin diye burada da tutulur.
var transitions = map[Status][]Status{
	PendingTest: {Available, Quarantined, Discarded, Expired},
	Available:   {Reserved, Quarantined, Discarded, Expired, Split},
	Reserved:    {Available, Issued, Quarantined, Expired},
	Issued:      {Transfused, Quarantined},
	Quarantined: {Discarded, Available},
}

// CanTransition geçişin izinli olup olmadığını döner.
func CanTransition(from, to Status) bool {
	for _, s := range transitions[from] {
		if s == to {
			return true
		}
	}
	return false
}

// Terminal durumdan çıkış yoktur.
func Terminal(s Status) bool { return len(transitions[s]) == 0 }

// AllTransitions tüm izinli geçişleri döner (DB ile tutarlılık testi için).
func AllTransitions() [][2]Status {
	var out [][2]Status
	for from, tos := range transitions {
		for _, to := range tos {
			out = append(out, [2]Status{from, to})
		}
	}
	return out
}

// Shelf raf ömrü (gün) ve bileşen tanımları.
type Shelf map[string]int

// Expiry üretim anından son kullanma anını hesaplar.
func (s Shelf) Expiry(component string, producedAt time.Time) (time.Time, error) {
	days, ok := s[component]
	if !ok || days <= 0 {
		return time.Time{}, fmt.Errorf("bileşen için raf ömrü tanımlı değil: %s", component)
	}
	return producedAt.AddDate(0, 0, days), nil
}

// ComponentsFor bir bağış türünden üretilebilecek bileşenleri döner.
func ComponentsFor(donationType string) []string {
	switch donationType {
	case "whole_blood":
		return []string{"RBC", "PLT", "FFP"}
	case "platelet_apheresis":
		return []string{"PLT"}
	}
	return nil
}

// DefaultVolume bileşen başına tipik hacim (ml).
func DefaultVolume(component, donationType string) int {
	switch component {
	case "RBC":
		return 280
	case "FFP":
		return 220
	case "PLT":
		if donationType == "platelet_apheresis" {
			return 250
		}
		return 60
	}
	return 0
}

var b32 = base32.StdEncoding.WithPadding(base32.NoPadding)

// SignBarcode ürün numarasına HMAC-SHA256 tabanlı kısa imza ekler: "P0000123.ABCDEFGH23".
func SignBarcode(secret []byte, productNo string) string {
	return productNo + "." + sig(secret, productNo)
}

// VerifyBarcode imzayı sabit zamanlı karşılaştırır ve ürün numarasını döner.
func VerifyBarcode(secret []byte, code string) (string, bool) {
	i := strings.LastIndexByte(code, '.')
	if i <= 0 || i == len(code)-1 {
		return "", false
	}
	no, s := code[:i], code[i+1:]
	return no, hmac.Equal([]byte(s), []byte(sig(secret, no)))
}

func sig(secret []byte, productNo string) string {
	m := hmac.New(sha256.New, secret)
	m.Write([]byte(productNo))
	return b32.EncodeToString(m.Sum(nil))[:10]
}
