package auth

import (
	"strings"
	"testing"
	"time"
)

func TestPasswordHashAndVerify(t *testing.T) {
	h, err := HashPassword("Gizli-123")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(h, "$argon2id$") {
		t.Fatalf("argon2id biçimi bekleniyordu: %s", h)
	}
	if !VerifyPassword("Gizli-123", h) || VerifyPassword("gizli-123", h) {
		t.Fatal("parola doğrulaması hatalı")
	}
	h2, _ := HashPassword("Gizli-123")
	if h == h2 {
		t.Fatal("tuz her seferinde farklı olmalı")
	}
	for _, bad := range []string{"", "$bcrypt$x", "$argon2id$v=19$m=x$a$b", "$argon2id$v=19$m=1,t=1,p=1$!!$!!"} {
		if VerifyPassword("x", bad) {
			t.Errorf("bozuk özet kabul edilmemeli: %q", bad)
		}
	}
}

func TestJWTRoundTripAndTamper(t *testing.T) {
	iss := NewIssuer([]byte(strings.Repeat("k", 32)), time.Minute)
	tok, exp, err := iss.Issue(Claims{UserID: 7, Role: "bank"})
	if err != nil || exp.Before(time.Now()) {
		t.Fatal(err)
	}
	c, err := iss.Parse(tok)
	if err != nil || c.UserID != 7 || c.Role != "bank" {
		t.Fatalf("belirteç çözülemedi: %v %+v", err, c)
	}
	if _, err := iss.Parse(tok + "x"); err == nil {
		t.Fatal("bozulmuş belirteç reddedilmeli")
	}
	other := NewIssuer([]byte(strings.Repeat("z", 32)), time.Minute)
	if _, err := other.Parse(tok); err == nil {
		t.Fatal("farklı anahtarla imzalanmış belirteç reddedilmeli")
	}
}

func TestJWTExpired(t *testing.T) {
	iss := NewIssuer([]byte(strings.Repeat("k", 32)), -time.Minute)
	tok, _, _ := iss.Issue(Claims{UserID: 1, Role: "admin"})
	if _, err := iss.Parse(tok); err == nil {
		t.Fatal("süresi dolmuş belirteç reddedilmeli")
	}
}

func TestRefreshToken(t *testing.T) {
	a, ha, err := NewRefreshToken()
	b, _, _ := NewRefreshToken()
	if err != nil || a == b || len(ha) != 32 {
		t.Fatal("yenileme belirteci benzersiz olmalı")
	}
	if string(HashToken(a)) != string(ha) {
		t.Fatal("özet tutarlı olmalı")
	}
}
