package sim

import (
	"bytes"
	"testing"
	"time"
)

var anchor = time.Date(2026, 10, 30, 0, 0, 0, 0, time.UTC)

func small(seed uint64) Config {
	c := DefaultConfig(seed, anchor)
	c.Donors, c.HistoryDays, c.DailyDonations, c.DailyRBCDemand = 2000, 40, 40, 30
	return c
}

func TestSameSeedSameData(t *testing.T) {
	a := Generate(small(42)).Fingerprint()
	b := Generate(small(42)).Fingerprint()
	if a != b {
		t.Fatalf("aynı tohum farklı veri üretti: %s != %s", a, b)
	}
	if c := Generate(small(43)).Fingerprint(); c == a {
		t.Fatal("farklı tohum aynı veriyi üretmemeli")
	}
}

func TestGeneratedHistoryRespectsInvariants(t *testing.T) {
	ds := Generate(small(7))
	if len(ds.Donors) != 2000 || len(ds.Donations) == 0 || len(ds.Distributions) == 0 {
		t.Fatalf("beklenen hacim üretilmedi: %d bağışçı %d bağış %d dağıtım", len(ds.Donors), len(ds.Donations), len(ds.Distributions))
	}
	// Her şey çapa gününden önce olmalı.
	for _, e := range ds.Events {
		if !e.At.Before(anchor) {
			t.Fatalf("çapa sonrası olay: %+v", e)
		}
	}
	// Bir ürün en fazla bir kez dağıtılır.
	seen := map[int]bool{}
	for _, d := range ds.Distributions {
		if seen[d.Product] {
			t.Fatalf("ürün iki kez dağıtıldı: %d", d.Product)
		}
		seen[d.Product] = true
	}
	// Tam kan bekleme süresi (erkek 90 / kadın 120) ihlal edilmemeli.
	last := map[int]time.Time{}
	for _, d := range ds.Donations {
		if d.Type != "whole_blood" {
			continue
		}
		wait := 90
		if ds.Donors[d.Donor-1].Sex == "F" {
			wait = 120
		}
		if prev, ok := last[d.Donor]; ok && d.At.Before(day(prev).AddDate(0, 0, wait)) {
			t.Fatalf("bekleme süresi ihlali: bağışçı %d", d.Donor)
		}
		last[d.Donor] = d.At
	}
	// Dağıtılan ürün alıcı grupla uyumlu olmalı (RBC).
	reqs := map[int]Request{}
	for _, r := range ds.Requests {
		reqs[r.N] = r
	}
	for _, d := range ds.Distributions {
		p := ds.Products[d.Product-1]
		r := reqs[d.Request]
		if p.Component == "RBC" && !rbcOK(p.ABO, p.Rh, r.ABO, r.Rh) {
			t.Fatalf("uyumsuz dağıtım: ürün %s%v → alıcı %s%v", p.ABO, p.Rh, r.ABO, r.Rh)
		}
	}
	if ds.Ineligible == 0 {
		t.Error("uygunsuz bağışçı denemeleri üretilmeli")
	}
}

func rbcOK(dABO string, dRh bool, rABO string, rRh bool) bool {
	if dRh && !rRh {
		return false
	}
	switch dABO {
	case "O":
		return true
	case "A":
		return rABO == "A" || rABO == "AB"
	case "B":
		return rABO == "B" || rABO == "AB"
	}
	return rABO == "AB"
}

func TestNonceDeterministicAndDistinct(t *testing.T) {
	if !bytes.Equal(Nonce(1, 5), Nonce(1, 5)) || bytes.Equal(Nonce(1, 5), Nonce(1, 6)) || len(Nonce(1, 1)) != 12 {
		t.Fatal("nonce deterministik ve bağışçıya özgü olmalı")
	}
}
