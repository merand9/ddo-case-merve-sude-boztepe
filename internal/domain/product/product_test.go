package product

import (
	"testing"
	"time"
)

func TestTransitions(t *testing.T) {
	ok := [][2]Status{{PendingTest, Available}, {Available, Reserved}, {Reserved, Issued}, {Issued, Transfused}, {Available, Split}}
	for _, p := range ok {
		if !CanTransition(p[0], p[1]) {
			t.Errorf("%s→%s izinli olmalı", p[0], p[1])
		}
	}
	bad := [][2]Status{{Transfused, Available}, {Discarded, Available}, {Issued, Available}, {Expired, Available}, {PendingTest, Issued}, {Split, Available}}
	for _, p := range bad {
		if CanTransition(p[0], p[1]) {
			t.Errorf("%s→%s yasak olmalı", p[0], p[1])
		}
	}
}

func TestTerminalStates(t *testing.T) {
	for _, s := range []Status{Transfused, Discarded, Expired, Split} {
		if !Terminal(s) {
			t.Errorf("%s terminal olmalı", s)
		}
	}
	if Terminal(Available) {
		t.Error("available terminal değil")
	}
	if len(AllTransitions()) != 17 {
		t.Errorf("geçiş sayısı DB tablosuyla aynı olmalı (17): %d", len(AllTransitions()))
	}
}

func TestShelfLife(t *testing.T) {
	s := Shelf{"RBC": 42, "PLT": 5, "FFP": 365}
	p := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)
	for c, want := range map[string]time.Time{"RBC": p.AddDate(0, 0, 42), "PLT": p.AddDate(0, 0, 5), "FFP": p.AddDate(0, 0, 365)} {
		got, err := s.Expiry(c, p)
		if err != nil || !got.Equal(want) {
			t.Errorf("%s SKT %v, beklenen %v", c, got, want)
		}
	}
	if _, err := s.Expiry("XXX", p); err == nil {
		t.Error("tanımsız bileşen hata vermeli")
	}
}

func TestComponentsAndVolumes(t *testing.T) {
	if len(ComponentsFor("whole_blood")) != 3 || len(ComponentsFor("platelet_apheresis")) != 1 || ComponentsFor("x") != nil {
		t.Fatal("bileşen listesi hatalı")
	}
	if DefaultVolume("PLT", "platelet_apheresis") <= DefaultVolume("PLT", "whole_blood") || DefaultVolume("X", "") != 0 {
		t.Fatal("hacimler hatalı")
	}
	if DefaultVolume("RBC", "whole_blood") == 0 || DefaultVolume("FFP", "whole_blood") == 0 {
		t.Fatal("hacim sıfır olmamalı")
	}
}

func TestBarcodeSignature(t *testing.T) {
	key := []byte("test-secret")
	code := SignBarcode(key, "P0000042")
	no, ok := VerifyBarcode(key, code)
	if !ok || no != "P0000042" {
		t.Fatalf("imza doğrulanmalı: %s", code)
	}
	if _, ok := VerifyBarcode(key, "P0000043"+code[len("P0000042"):]); ok {
		t.Fatal("başka ürün numarasıyla imza geçmemeli")
	}
	if _, ok := VerifyBarcode([]byte("other"), code); ok {
		t.Fatal("farklı anahtarla geçmemeli")
	}
	for _, bad := range []string{"", "nodot", ".abc", "P1."} {
		if _, ok := VerifyBarcode(key, bad); ok {
			t.Errorf("%q geçmemeli", bad)
		}
	}
}
