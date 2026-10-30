package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/msboztepe/damarbagis/internal/auth"
	"github.com/msboztepe/damarbagis/internal/httpx"
)

// User kullanıcı bilgisi (parola özeti asla dışarı verilmez).
type User struct {
	ID         int64     `json:"id"`
	Email      string    `json:"email"`
	Role       string    `json:"role"`
	FullName   string    `json:"full_name"`
	HospitalID *int64    `json:"hospital_id,omitempty"`
	DonorID    *int64    `json:"donor_id,omitempty"`
	Active     bool      `json:"active"`
	CreatedAt  time.Time `json:"created_at"`
}

// Tokens giriş yanıtı.
type Tokens struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	ExpiresAt    time.Time `json:"expires_at"`
	User         User      `json:"user"`
}

const userCols = `id, email, role, full_name, hospital_id, donor_id, active, created_at`

func scanUser(r pgx.Row) (User, error) {
	var u User
	err := r.Scan(&u.ID, &u.Email, &u.Role, &u.FullName, &u.HospitalID, &u.DonorID, &u.Active, &u.CreatedAt)
	return u, err
}

var errBadLogin = httpx.E(http.StatusUnauthorized, "invalid_credentials", "e-posta ya da parola hatalı")

// Login parolayı doğrular ve erişim + yenileme belirteci üretir.
func (a *App) Login(ctx context.Context, email, password string) (Tokens, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	var hash string
	var u User
	err := a.DB.QueryRow(ctx, `SELECT password_hash, `+userCols+` FROM users WHERE email=$1`, email).
		Scan(&hash, &u.ID, &u.Email, &u.Role, &u.FullName, &u.HospitalID, &u.DonorID, &u.Active, &u.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		// Kullanıcı yokken de özet hesaplanır: yanıt süresi kullanıcı varlığını sızdırmasın.
		auth.VerifyPassword(password, dummyHash)
		return Tokens{}, errBadLogin
	}
	if err != nil {
		return Tokens{}, err
	}
	if !auth.VerifyPassword(password, hash) || !u.Active {
		_ = a.InTx(ctx, "", func(tx pgx.Tx) error {
			return a.Audit(ctx, tx, "auth.login_failed", "user", u.ID, nil, nil, "")
		})
		return Tokens{}, errBadLogin
	}
	return a.issue(ctx, u)
}

var dummyHash, _ = auth.HashPassword("dummy-password-for-timing")

func (a *App) issue(ctx context.Context, u User) (Tokens, error) {
	c := auth.Claims{UserID: u.ID, Role: u.Role}
	if u.HospitalID != nil {
		c.HospitalID = *u.HospitalID
	}
	if u.DonorID != nil {
		c.DonorID = *u.DonorID
	}
	access, exp, err := a.Issuer.Issue(c)
	if err != nil {
		return Tokens{}, err
	}
	refresh, h, err := auth.NewRefreshToken()
	if err != nil {
		return Tokens{}, err
	}
	if _, err := a.DB.Exec(ctx, `INSERT INTO refresh_tokens (user_id, token_hash, expires_at) VALUES ($1,$2,$3)`,
		u.ID, h, a.Now().Add(a.Cfg.RefreshTTL)); err != nil {
		return Tokens{}, err
	}
	return Tokens{AccessToken: access, RefreshToken: refresh, ExpiresAt: exp, User: u}, nil
}

// Refresh yenileme belirtecini döndürür (rotation): eskisi iptal edilir, yenisi verilir.
// İptal edilmiş bir belirteç yeniden kullanılırsa kullanıcının tüm belirteçleri iptal edilir.
func (a *App) Refresh(ctx context.Context, token string) (Tokens, error) {
	h := auth.HashToken(token)
	var u User
	var tokID int64
	var revoked *time.Time
	var exp time.Time
	err := a.DB.QueryRow(ctx, `SELECT t.id, t.revoked_at, t.expires_at, u.id, u.email, u.role, u.full_name,
			u.hospital_id, u.donor_id, u.active, u.created_at
		FROM refresh_tokens t JOIN users u ON u.id=t.user_id WHERE t.token_hash=$1`, h).
		Scan(&tokID, &revoked, &exp, &u.ID, &u.Email, &u.Role, &u.FullName, &u.HospitalID, &u.DonorID, &u.Active, &u.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Tokens{}, httpx.Unauthorized("geçersiz yenileme belirteci")
	}
	if err != nil {
		return Tokens{}, err
	}
	if revoked != nil {
		_, _ = a.DB.Exec(ctx, `UPDATE refresh_tokens SET revoked_at=now() WHERE user_id=$1 AND revoked_at IS NULL`, u.ID)
		return Tokens{}, httpx.Unauthorized("yenileme belirteci yeniden kullanıldı; tüm oturumlar kapatıldı")
	}
	if exp.Before(a.Now()) || !u.Active {
		return Tokens{}, httpx.Unauthorized("yenileme belirtecinin süresi dolmuş")
	}
	tag, err := a.DB.Exec(ctx, `UPDATE refresh_tokens SET revoked_at=now() WHERE id=$1 AND revoked_at IS NULL`, tokID)
	if err != nil {
		return Tokens{}, err
	}
	if tag.RowsAffected() == 0 {
		return Tokens{}, httpx.Unauthorized("yenileme belirteci zaten kullanıldı")
	}
	return a.issue(ctx, u)
}

// Logout yenileme belirtecini iptal eder.
func (a *App) Logout(ctx context.Context, token string) error {
	_, err := a.DB.Exec(ctx, `UPDATE refresh_tokens SET revoked_at=now() WHERE token_hash=$1 AND revoked_at IS NULL`, auth.HashToken(token))
	return err
}

// Me oturumdaki kullanıcı.
func (a *App) Me(ctx context.Context) (User, error) {
	c := httpx.ClaimsFrom(ctx)
	return scanUser(a.DB.QueryRow(ctx, `SELECT `+userCols+` FROM users WHERE id=$1`, c.UserID))
}

// ListUsers yönetici için kullanıcı listesi.
func (a *App) ListUsers(ctx context.Context) ([]User, error) {
	rows, err := a.DB.Query(ctx, `SELECT `+userCols+` FROM users ORDER BY id`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (User, error) { return scanUser(r) })
}

// NewUser kullanıcı oluşturma girdisi.
type NewUser struct {
	Email      string `json:"email"`
	Password   string `json:"password"`
	Role       string `json:"role"`
	FullName   string `json:"full_name"`
	HospitalID *int64 `json:"hospital_id"`
	DonorID    *int64 `json:"donor_id"`
}

var roles = map[string]bool{"donor": true, "nurse": true, "lab": true, "bank": true, "hospital": true, "admin": true}

// CreateUser yönetici tarafından kullanıcı ekler.
func (a *App) CreateUser(ctx context.Context, in NewUser) (User, error) {
	in.Email = strings.ToLower(strings.TrimSpace(in.Email))
	if !strings.Contains(in.Email, "@") || len(in.Email) > 200 {
		return User{}, httpx.Validation("geçerli bir e-posta girin")
	}
	if len(in.Password) < 8 {
		return User{}, httpx.Validation("parola en az 8 karakter olmalı")
	}
	if !roles[in.Role] {
		return User{}, httpx.Validation("geçersiz rol")
	}
	if strings.TrimSpace(in.FullName) == "" {
		return User{}, httpx.Validation("ad soyad gerekli")
	}
	hash, err := auth.HashPassword(in.Password)
	if err != nil {
		return User{}, err
	}
	var u User
	err = a.InTx(ctx, "", func(tx pgx.Tx) error {
		u, err = scanUser(tx.QueryRow(ctx, `INSERT INTO users (email, password_hash, role, full_name, hospital_id, donor_id)
			VALUES ($1,$2,$3,$4,$5,$6) RETURNING `+userCols, in.Email, hash, in.Role, in.FullName, in.HospitalID, in.DonorID))
		if err != nil {
			return err
		}
		return a.Audit(ctx, tx, "user.create", "user", u.ID, nil, u, "")
	})
	return u, err
}

// DemoAccount README ve TESTING.md'de listelenen hesaplar.
type DemoAccount struct {
	Email, Role, Name string
	HospitalID        int64
	Donor             bool
}

// DemoAccounts her rol için bir (laboratuvar için iki) hesap.
var DemoAccounts = []DemoAccount{
	{Email: "admin@damarbagis.local", Role: "admin", Name: "Yönetici Demo"},
	{Email: "hemsire@damarbagis.local", Role: "nurse", Name: "Hemşire Demo"},
	{Email: "lab@damarbagis.local", Role: "lab", Name: "Laboratuvar Uzmanı Demo"},
	{Email: "lab2@damarbagis.local", Role: "lab", Name: "Laboratuvar Uzmanı İkinci Onay"},
	{Email: "bankasorumlusu@damarbagis.local", Role: "bank", Name: "Kan Bankası Sorumlusu Demo"},
	{Email: "hastane@damarbagis.local", Role: "hospital", Name: "Bandırma Devlet Hastanesi", HospitalID: 1},
	{Email: "hastane2@damarbagis.local", Role: "hospital", Name: "Bursa Şehir Hastanesi", HospitalID: 3},
	{Email: "bagisci@damarbagis.local", Role: "donor", Name: "Ayşe Demo", Donor: true},
}

// EnsureDemoData demo kullanıcılarını ve demo bağışçısını oluşturur (idempotent).
func (a *App) EnsureDemoData(ctx context.Context) error {
	hash, err := auth.HashPassword(a.Cfg.DemoPassword)
	if err != nil {
		return err
	}
	return a.InTx(ctx, "demo verisi", func(tx pgx.Tx) error {
		for _, d := range DemoAccounts {
			var donorID *int64
			if d.Donor {
				id, err := a.ensureDemoDonor(ctx, tx)
				if err != nil {
					return err
				}
				donorID = &id
			}
			var hid *int64
			if d.HospitalID > 0 {
				hid = &d.HospitalID
			}
			if _, err := tx.Exec(ctx, `INSERT INTO users (email, password_hash, role, full_name, hospital_id, donor_id)
				VALUES ($1,$2,$3,$4,$5,$6) ON CONFLICT (email) DO NOTHING`, d.Email, hash, d.Role, d.Name, hid, donorID); err != nil {
				return fmt.Errorf("demo kullanıcı %s: %w", d.Email, err)
			}
		}
		return nil
	})
}

func (a *App) ensureDemoDonor(ctx context.Context, tx pgx.Tx) (int64, error) {
	var id int64
	err := tx.QueryRow(ctx, `SELECT id FROM donors WHERE donor_no='D-DEMO-1'`).Scan(&id)
	if err == nil {
		return id, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return 0, err
	}
	if err := tx.QueryRow(ctx, `SELECT nextval(pg_get_serial_sequence('donors','id'))`).Scan(&id); err != nil {
		return 0, err
	}
	health, _ := json.Marshal(HealthInfo{Medications: []string{}, Conditions: []string{}, Notes: "Demo bağışçı"})
	enc, err := a.Sealer.Seal(health, []byte("D-DEMO-1"))
	if err != nil {
		return 0, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO donors (id, donor_no, first_name, last_name, sex, birth_date, abo, rh_positive, phone, email, health_enc)
		VALUES ($1,'D-DEMO-1','Ayşe','Demo','F','1994-04-12','A',true,'+90 555 000 0001','bagisci@damarbagis.local',$2)`, id, enc)
	return id, err
}
