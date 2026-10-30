package secure

import (
	"bytes"
	"testing"
)

func key() []byte { return bytes.Repeat([]byte{7}, 32) }

func TestSealOpenRoundTrip(t *testing.T) {
	s, err := NewSealer(key())
	if err != nil {
		t.Fatal(err)
	}
	plain := []byte(`{"medications":["aspirin"]}`)
	ct, err := s.Seal(plain, []byte("D-1"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(ct, []byte("aspirin")) {
		t.Fatal("şifreli veri düz metin içermemeli")
	}
	got, err := s.Open(ct, []byte("D-1"))
	if err != nil || !bytes.Equal(got, plain) {
		t.Fatalf("çözme başarısız: %v", err)
	}
}

func TestAADBindsRecord(t *testing.T) {
	s, _ := NewSealer(key())
	ct, _ := s.Seal([]byte("x"), []byte("D-1"))
	if _, err := s.Open(ct, []byte("D-2")); err == nil {
		t.Fatal("başka kayda kopyalanan veri çözülmemeli")
	}
}

func TestTamperAndBadInput(t *testing.T) {
	s, _ := NewSealer(key())
	ct, _ := s.Seal([]byte("hello"), nil)
	ct[len(ct)-1] ^= 1
	if _, err := s.Open(ct, nil); err == nil {
		t.Fatal("değiştirilmiş veri reddedilmeli")
	}
	if _, err := s.Open([]byte{1, 2}, nil); err == nil {
		t.Fatal("kısa veri reddedilmeli")
	}
	ct2, _ := s.Seal([]byte("hello"), nil)
	ct2[0] = 9
	if _, err := s.Open(ct2, nil); err == nil {
		t.Fatal("bilinmeyen sürüm reddedilmeli")
	}
	if _, err := NewSealer([]byte("short")); err == nil {
		t.Fatal("kısa anahtar reddedilmeli")
	}
}

func TestDeterministicNonce(t *testing.T) {
	s, _ := NewSealer(key())
	n := make([]byte, 12)
	a, _ := s.SealWithNonce([]byte("x"), nil, n)
	b, _ := s.SealWithNonce([]byte("x"), nil, n)
	if !bytes.Equal(a, b) {
		t.Fatal("aynı nonce aynı çıktıyı vermeli")
	}
	if _, err := s.SealWithNonce([]byte("x"), nil, []byte{1}); err == nil {
		t.Fatal("hatalı nonce boyutu reddedilmeli")
	}
	if got, err := s.Open(a, nil); err != nil || string(got) != "x" {
		t.Fatal("deterministik şifre çözülmeli")
	}
}
