// Package secure sağlık verisinin AES-256-GCM ile şifrelenmesini sağlar.
package secure

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"
)

// keyVersion şifreli verinin ilk baytıdır; anahtar rotasyonunda eski kayıtlar ayırt edilebilir.
const keyVersion byte = 1

// Sealer AES-GCM ile şifreleyip çözer. AAD olarak kayıt kimliği (ör. bağışçı no) kullanılır;
// böylece şifreli bir değer başka bir satıra kopyalanırsa çözülemez.
type Sealer struct{ aead cipher.AEAD }

// NewSealer 32 baytlık anahtarla bir Sealer oluşturur.
func NewSealer(key []byte) (*Sealer, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("anahtar 32 bayt olmalı, %d", len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Sealer{aead: aead}, nil
}

// Seal düz metni şifreler: [sürüm][nonce][şifreli+etiket].
func (s *Sealer) Seal(plain, aad []byte) ([]byte, error) {
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	out := append([]byte{keyVersion}, nonce...)
	return s.aead.Seal(out, nonce, plain, aad), nil
}

// SealWithNonce deterministik nonce ile şifreler; yalnızca simülatörün tekrar üretilebilir
// seed verisi için kullanılır (her kayıt için nonce farklıdır).
func (s *Sealer) SealWithNonce(plain, aad, nonce []byte) ([]byte, error) {
	if len(nonce) != s.aead.NonceSize() {
		return nil, errors.New("nonce boyutu hatalı")
	}
	out := append([]byte{keyVersion}, nonce...)
	return s.aead.Seal(out, nonce, plain, aad), nil
}

// Open şifreli veriyi çözer.
func (s *Sealer) Open(data, aad []byte) ([]byte, error) {
	n := s.aead.NonceSize()
	if len(data) < 1+n+s.aead.Overhead() {
		return nil, errors.New("şifreli veri çok kısa")
	}
	if data[0] != keyVersion {
		return nil, fmt.Errorf("bilinmeyen anahtar sürümü: %d", data[0])
	}
	return s.aead.Open(nil, data[1:1+n], data[1+n:], aad)
}
