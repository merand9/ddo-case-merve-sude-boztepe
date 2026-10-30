// Package auth parola özeti (argon2id) ve JWT erişim belirteçlerini yönetir.
package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/argon2"
)

// argon2id parametreleri (OWASP önerisi: m=19 MiB, t=2, p=1).
const (
	argonTime    = 2
	argonMemory  = 19 * 1024
	argonThreads = 1
	argonKeyLen  = 32
	saltLen      = 16
)

// HashPassword parolayı PHC biçiminde argon2id özetine dönüştürür.
func HashPassword(pw string) (string, error) {
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	return hashWithSalt(pw, salt), nil
}

func hashWithSalt(pw string, salt []byte) string {
	sum := argon2.IDKey([]byte(pw), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	b := base64.RawStdEncoding
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemory, argonTime, argonThreads, b.EncodeToString(salt), b.EncodeToString(sum))
}

// VerifyPassword özeti sabit zamanlı karşılaştırır.
func VerifyPassword(pw, encoded string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false
	}
	var m, t uint32
	var p uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &m, &t, &p); err != nil {
		return false
	}
	b := base64.RawStdEncoding
	salt, err1 := b.DecodeString(parts[4])
	want, err2 := b.DecodeString(parts[5])
	if err1 != nil || err2 != nil {
		return false
	}
	got := argon2.IDKey([]byte(pw), salt, t, m, p, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1
}

// Claims erişim belirtecinin içeriği.
type Claims struct {
	UserID     int64  `json:"uid"`
	Role       string `json:"role"`
	HospitalID int64  `json:"hid,omitempty"`
	DonorID    int64  `json:"did,omitempty"`
	jwt.RegisteredClaims
}

// Issuer JWT üretir ve doğrular.
type Issuer struct {
	secret []byte
	ttl    time.Duration
}

// NewIssuer HS256 imzalı belirteç üreticisi.
func NewIssuer(secret []byte, ttl time.Duration) *Issuer { return &Issuer{secret: secret, ttl: ttl} }

// Issue erişim belirteci üretir.
func (i *Issuer) Issue(c Claims) (string, time.Time, error) {
	now := time.Now()
	exp := now.Add(i.ttl)
	c.RegisteredClaims = jwt.RegisteredClaims{
		Subject:   fmt.Sprint(c.UserID),
		IssuedAt:  jwt.NewNumericDate(now),
		ExpiresAt: jwt.NewNumericDate(exp),
		Issuer:    "damarbagis",
	}
	s, err := jwt.NewWithClaims(jwt.SigningMethodHS256, c).SignedString(i.secret)
	return s, exp, err
}

// ErrInvalidToken belirteç geçersiz ya da süresi dolmuş.
var ErrInvalidToken = errors.New("geçersiz ya da süresi dolmuş belirteç")

// Parse belirteci doğrular; yalnızca HS256 kabul edilir.
func (i *Issuer) Parse(tok string) (*Claims, error) {
	c := &Claims{}
	t, err := jwt.ParseWithClaims(tok, c, func(t *jwt.Token) (any, error) { return i.secret, nil },
		jwt.WithValidMethods([]string{"HS256"}), jwt.WithIssuer("damarbagis"))
	if err != nil || !t.Valid {
		return nil, ErrInvalidToken
	}
	return c, nil
}

// NewRefreshToken rastgele yenileme belirteci ve veritabanında saklanacak özetini döner.
func NewRefreshToken() (token string, hash []byte, err error) {
	b := make([]byte, 32)
	if _, err = rand.Read(b); err != nil {
		return "", nil, err
	}
	token = base64.RawURLEncoding.EncodeToString(b)
	return token, HashToken(token), nil
}

// HashToken yenileme belirtecinin SHA-256 özeti (belirtecin kendisi saklanmaz).
func HashToken(tok string) []byte {
	s := sha256.Sum256([]byte(tok))
	return s[:]
}
