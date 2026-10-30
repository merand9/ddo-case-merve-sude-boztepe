package app

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/msboztepe/damarbagis/internal/domain/compat"
	"github.com/msboztepe/damarbagis/internal/domain/eligibility"
	"github.com/msboztepe/damarbagis/internal/httpx"
)

// HealthInfo şifreli saklanan sağlık beyanı.
type HealthInfo struct {
	Medications []string `json:"medications"`
	Conditions  []string `json:"conditions"`
	Travel      []string `json:"travel,omitempty"`
	Notes       string   `json:"notes,omitempty"`
}

// Donor bağışçı kimlik bilgileri (sağlık verisi hariç).
type Donor struct {
	ID         int64     `json:"id"`
	DonorNo    string    `json:"donor_no"`
	FirstName  string    `json:"first_name"`
	LastName   string    `json:"last_name"`
	Sex        string    `json:"sex"`
	BirthDate  string    `json:"birth_date"`
	ABO        string    `json:"abo"`
	RhPositive bool      `json:"rh_positive"`
	Group      string    `json:"group"`
	Phone      *string   `json:"phone,omitempty"`
	Email      *string   `json:"email,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
}

const donorCols = `id, donor_no, first_name, last_name, sex, birth_date, abo, rh_positive, phone, email, created_at`

func scanDonor(r pgx.Row) (Donor, error) {
	var d Donor
	var bd time.Time
	err := r.Scan(&d.ID, &d.DonorNo, &d.FirstName, &d.LastName, &d.Sex, &bd, &d.ABO, &d.RhPositive, &d.Phone, &d.Email, &d.CreatedAt)
	d.BirthDate = bd.Format("2006-01-02")
	d.Group = compat.Group{ABO: d.ABO, RhPositive: d.RhPositive}.String()
	return d, err
}

// NewDonor kayıt girdisi.
type NewDonor struct {
	FirstName  string     `json:"first_name"`
	LastName   string     `json:"last_name"`
	Sex        string     `json:"sex"`
	BirthDate  string     `json:"birth_date"`
	ABO        string     `json:"abo"`
	RhPositive *bool      `json:"rh_positive"`
	Phone      string     `json:"phone"`
	Email      string     `json:"email"`
	Health     HealthInfo `json:"health"`
}

func (in *NewDonor) validate(now time.Time) (time.Time, error) {
	var errs []string
	in.FirstName, in.LastName = strings.TrimSpace(in.FirstName), strings.TrimSpace(in.LastName)
	if in.FirstName == "" || len(in.FirstName) > 80 {
		errs = append(errs, "first_name 1-80 karakter olmalı")
	}
	if in.LastName == "" || len(in.LastName) > 80 {
		errs = append(errs, "last_name 1-80 karakter olmalı")
	}
	if in.Sex != "M" && in.Sex != "F" {
		errs = append(errs, "sex M ya da F olmalı")
	}
	bd, err := time.Parse("2006-01-02", in.BirthDate)
	if err != nil || bd.After(now) || bd.Year() < 1900 {
		errs = append(errs, "birth_date YYYY-AA-GG biçiminde geçmiş bir tarih olmalı")
	}
	if !compat.ValidABO(in.ABO) {
		errs = append(errs, "abo O, A, B ya da AB olmalı")
	}
	if in.RhPositive == nil {
		errs = append(errs, "rh_positive gerekli")
	}
	if in.Email != "" && !strings.Contains(in.Email, "@") {
		errs = append(errs, "email geçersiz")
	}
	if len(errs) > 0 {
		return bd, httpx.Validation("girdi doğrulanamadı").WithDetails(errs)
	}
	if in.Health.Medications == nil {
		in.Health.Medications = []string{}
	}
	if in.Health.Conditions == nil {
		in.Health.Conditions = []string{}
	}
	return bd, nil
}

// RegisterDonor bağışçı kaydeder; sağlık beyanı şifrelenir. Aynı ad-soyad-doğum tarihi
// ile ikinci kayıt engellenmez (gerçekte kimlik numarasıyla yapılır) ama denetime yazılır.
func (a *App) RegisterDonor(ctx context.Context, in NewDonor) (Donor, error) {
	bd, err := in.validate(a.Now())
	if err != nil {
		return Donor{}, err
	}
	var d Donor
	err = a.InTx(ctx, "bağışçı kaydı", func(tx pgx.Tx) error {
		var id int64
		if err := tx.QueryRow(ctx, `SELECT nextval(pg_get_serial_sequence('donors','id'))`).Scan(&id); err != nil {
			return err
		}
		no := fmt.Sprintf("D%07d", id)
		plain, _ := json.Marshal(in.Health)
		enc, err := a.Sealer.Seal(plain, []byte(no))
		if err != nil {
			return err
		}
		d, err = scanDonor(tx.QueryRow(ctx, `INSERT INTO donors (id, donor_no, first_name, last_name, sex, birth_date, abo, rh_positive, phone, email, health_enc)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,nullif($9,''),nullif($10,''),$11) RETURNING `+donorCols,
			id, no, in.FirstName, in.LastName, in.Sex, bd, in.ABO, *in.RhPositive, in.Phone, in.Email, enc))
		if err != nil {
			return err
		}
		return a.Audit(ctx, tx, "donor.create", "donor", d.ID, nil, d, "")
	})
	return d, err
}

// GetDonor bağışçı kimlik bilgileri.
func (a *App) GetDonor(ctx context.Context, id int64) (Donor, error) {
	d, err := scanDonor(a.DB.QueryRow(ctx, `SELECT `+donorCols+` FROM donors WHERE id=$1`, id))
	if err == pgx.ErrNoRows {
		return d, httpx.NotFound("bağışçı")
	}
	return d, err
}

// ListDonors ad/soyad/no ile arama, keyset sayfalı (id azalan).
func (a *App) ListDonors(ctx context.Context, q string, group string, p httpx.Page) (httpx.List[Donor], error) {
	args := []any{p.Limit + 1}
	where := []string{"true"}
	if p.Cursor > 0 {
		args = append(args, p.Cursor)
		where = append(where, fmt.Sprintf("id < $%d", len(args)))
	}
	if q = strings.TrimSpace(q); q != "" {
		args = append(args, strings.ToLower(q)+"%")
		n := len(args)
		where = append(where, fmt.Sprintf("(lower(last_name) LIKE $%d OR lower(first_name) LIKE $%d OR lower(donor_no) LIKE $%d)", n, n, n))
	}
	if group != "" {
		g, err := compat.ParseGroup(group)
		if err != nil {
			return httpx.List[Donor]{}, httpx.BadRequest(err.Error())
		}
		args = append(args, g.ABO, g.RhPositive)
		where = append(where, fmt.Sprintf("abo=$%d AND rh_positive=$%d", len(args)-1, len(args)))
	}
	rows, err := a.DB.Query(ctx, `SELECT `+donorCols+` FROM donors WHERE `+strings.Join(where, " AND ")+` ORDER BY id DESC LIMIT $1`, args...)
	if err != nil {
		return httpx.List[Donor]{}, err
	}
	items, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Donor, error) { return scanDonor(r) })
	if err != nil {
		return httpx.List[Donor]{}, err
	}
	return pageOf(items, p.Limit, func(d Donor) int64 { return d.ID }), nil
}

func pageOf[T any](items []T, limit int, id func(T) int64) httpx.List[T] {
	out := httpx.List[T]{Items: items}
	if len(items) > limit {
		out.Items = items[:limit]
		c := id(items[limit-1])
		out.NextCursor = &c
	}
	if out.Items == nil {
		out.Items = []T{}
	}
	return out
}

// EligibilityRequest uygunluk sorgusu girdisi.
type EligibilityRequest struct {
	DonationType string                    `json:"donation_type"`
	At           string                    `json:"at"` // isteğe bağlı YYYY-AA-GG: "bu tarihte uygun mu?"
	Measurements *eligibility.Measurements `json:"measurements"`
	Declarations []DeclarationIn           `json:"declarations"`
}

// DeclarationIn beyan girdisi.
type DeclarationIn struct {
	Code string `json:"code"`
	Date string `json:"date"`
}

func parseDeclarations(in []DeclarationIn) ([]eligibility.Declaration, error) {
	out := make([]eligibility.Declaration, 0, len(in))
	for _, d := range in {
		t, err := time.Parse("2006-01-02", d.Date)
		if err != nil {
			return nil, httpx.Validation("beyan tarihi YYYY-AA-GG olmalı: " + d.Code)
		}
		out = append(out, eligibility.Declaration{Code: d.Code, Date: t})
	}
	return out, nil
}

type donorFacts struct {
	sex       eligibility.Sex
	birth     time.Time
	history   []eligibility.PriorDonation
	deferrals []eligibility.Deferral
}

// loadFacts bağışçının uygunluk için gereken geçmişini yükler. lock=true ise bağışçı satırı
// kilitlenir; aynı bağışçı için eşzamanlı iki bağış kaydı sıraya girer.
func loadFacts(ctx context.Context, tx pgx.Tx, donorID int64, lock bool) (donorFacts, error) {
	var f donorFacts
	var sex string
	q := `SELECT sex, birth_date FROM donors WHERE id=$1`
	if lock {
		q += ` FOR UPDATE`
	}
	if err := tx.QueryRow(ctx, q, donorID).Scan(&sex, &f.birth); err != nil {
		if err == pgx.ErrNoRows {
			return f, httpx.NotFound("bağışçı")
		}
		return f, err
	}
	f.sex = eligibility.Sex(sex)
	rows, err := tx.Query(ctx, `SELECT donation_type, collected_at FROM donations WHERE donor_id=$1`, donorID)
	if err != nil {
		return f, err
	}
	f.history, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (eligibility.PriorDonation, error) {
		var p eligibility.PriorDonation
		var t string
		err := r.Scan(&t, &p.At)
		p.Type = eligibility.DonationType(t)
		return p, err
	})
	if err != nil {
		return f, err
	}
	rows, err = tx.Query(ctx, `SELECT reason_code, starts_on, ends_on FROM deferrals WHERE donor_id=$1`, donorID)
	if err != nil {
		return f, err
	}
	f.deferrals, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (eligibility.Deferral, error) {
		var d eligibility.Deferral
		err := r.Scan(&d.Code, &d.Start, &d.End)
		return d, err
	})
	return f, err
}

// CheckEligibility uygunluk kararını gerekçeleriyle döner (kayıt oluşturmaz).
func (a *App) CheckEligibility(ctx context.Context, donorID int64, req EligibilityRequest) (eligibility.Decision, error) {
	at := a.Now()
	if req.At != "" {
		t, err := time.Parse("2006-01-02", req.At)
		if err != nil {
			return eligibility.Decision{}, httpx.Validation("at YYYY-AA-GG olmalı")
		}
		at = t
	}
	if req.DonationType == "" {
		req.DonationType = string(eligibility.WholeBlood)
	}
	decl, err := parseDeclarations(req.Declarations)
	if err != nil {
		return eligibility.Decision{}, err
	}
	var dec eligibility.Decision
	err = pgx.BeginFunc(ctx, a.DB, func(tx pgx.Tx) error {
		f, err := loadFacts(ctx, tx, donorID, false)
		if err != nil {
			return err
		}
		book, err := a.RuleBook(ctx, tx)
		if err != nil {
			return err
		}
		dec = eligibility.Evaluate(eligibility.Input{
			Sex: f.sex, BirthDate: f.birth, Type: eligibility.DonationType(req.DonationType), At: at,
			History: f.history, Deferrals: f.deferrals, Declarations: decl, Measurements: req.Measurements,
		}, book)
		return nil
	})
	return dec, err
}

// Deferral erteleme kaydı.
type Deferral struct {
	ID         int64     `json:"id"`
	DonorID    int64     `json:"donor_id"`
	ReasonCode string    `json:"reason_code"`
	StartsOn   string    `json:"starts_on"`
	EndsOn     *string   `json:"ends_on"`
	Permanent  bool      `json:"permanent"`
	Note       *string   `json:"note,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
}

// AddDeferral erteleme ekler; bitiş günü başlangıç gününde geçerli kurala göre hesaplanır.
func (a *App) AddDeferral(ctx context.Context, donorID int64, code, startsOn, note string) (Deferral, error) {
	start := day(a.Now())
	if startsOn != "" {
		t, err := time.Parse("2006-01-02", startsOn)
		if err != nil {
			return Deferral{}, httpx.Validation("starts_on YYYY-AA-GG olmalı")
		}
		start = t
	}
	var out Deferral
	err := a.InTx(ctx, "erteleme", func(tx pgx.Tx) error {
		book, err := a.RuleBook(ctx, tx)
		if err != nil {
			return err
		}
		end, err := eligibility.DeferralEnd(book, code, start)
		if err != nil {
			return httpx.Validation(err.Error())
		}
		out, err = insertDeferral(ctx, tx, donorID, code, start, end, note, actor(ctx))
		if err != nil {
			return err
		}
		return a.Audit(ctx, tx, "donor.deferral.create", "donor", donorID, nil, out, note)
	})
	return out, err
}

func insertDeferral(ctx context.Context, tx pgx.Tx, donorID int64, code string, start time.Time, end *time.Time, note string, by *int64) (Deferral, error) {
	var d Deferral
	var s time.Time
	var e *time.Time
	err := tx.QueryRow(ctx, `INSERT INTO deferrals (donor_id, reason_code, starts_on, ends_on, note, created_by)
		VALUES ($1,$2,$3,$4,nullif($5,''),$6) RETURNING id, donor_id, reason_code, starts_on, ends_on, note, created_at`,
		donorID, code, start, end, note, by).Scan(&d.ID, &d.DonorID, &d.ReasonCode, &s, &e, &d.Note, &d.CreatedAt)
	if err != nil {
		return d, err
	}
	fillDeferral(&d, s, e)
	return d, nil
}

func fillDeferral(d *Deferral, s time.Time, e *time.Time) {
	d.StartsOn = s.Format("2006-01-02")
	if e != nil {
		v := e.Format("2006-01-02")
		d.EndsOn = &v
	}
	d.Permanent = e == nil
}

// ListDeferrals bağışçının ertelemeleri.
func (a *App) ListDeferrals(ctx context.Context, donorID int64) ([]Deferral, error) {
	rows, err := a.DB.Query(ctx, `SELECT id, donor_id, reason_code, starts_on, ends_on, note, created_at
		FROM deferrals WHERE donor_id=$1 ORDER BY starts_on DESC`, donorID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Deferral, error) {
		var d Deferral
		var s time.Time
		var e *time.Time
		err := r.Scan(&d.ID, &d.DonorID, &d.ReasonCode, &s, &e, &d.Note, &d.CreatedAt)
		fillDeferral(&d, s, e)
		return d, err
	})
}

// ReadHealth şifreli sağlık beyanını çözer. Gerekçe zorunludur ve erişim denetime yazılır.
func (a *App) ReadHealth(ctx context.Context, donorID int64, reason string) (HealthInfo, error) {
	reason = strings.TrimSpace(reason)
	if len([]rune(reason)) < 10 {
		return HealthInfo{}, httpx.E(400, "access_reason_required",
			"sağlık verisine erişim için en az 10 karakterlik gerekçe (X-Access-Reason başlığı) zorunlu")
	}
	var h HealthInfo
	err := a.InTx(ctx, "", func(tx pgx.Tx) error {
		var no string
		var enc []byte
		if err := tx.QueryRow(ctx, `SELECT donor_no, health_enc FROM donors WHERE id=$1`, donorID).Scan(&no, &enc); err != nil {
			if err == pgx.ErrNoRows {
				return httpx.NotFound("bağışçı")
			}
			return err
		}
		plain, err := a.Sealer.Open(enc, []byte(no))
		if err != nil {
			return fmt.Errorf("sağlık verisi çözülemedi: %w", err)
		}
		if err := json.Unmarshal(plain, &h); err != nil {
			return err
		}
		// Denetim kaydı aynı transaction'da: kayıt yazılamazsa veri de dönmez.
		return a.Audit(ctx, tx, "donor.health.read", "donor", donorID, nil, nil, reason)
	})
	return h, err
}

// MyDonorView bağışçının kendi ekranı.
type MyDonorView struct {
	Donor        Donor                `json:"donor"`
	Eligibility  eligibility.Decision `json:"eligibility"`
	Donations    []DonationSummary    `json:"donations"`
	Appointments []Appointment        `json:"appointments"`
	Deferrals    []Deferral           `json:"deferrals"`
}

// DonationSummary bağış özeti.
type DonationSummary struct {
	ID           int64     `json:"id"`
	DonationType string    `json:"donation_type"`
	CollectedAt  time.Time `json:"collected_at"`
	VolumeML     int       `json:"volume_ml"`
}

// MyDonor bağışçı rolündeki kullanıcının kendi bilgileri.
func (a *App) MyDonor(ctx context.Context) (MyDonorView, error) {
	c := httpx.ClaimsFrom(ctx)
	if c.DonorID == 0 {
		return MyDonorView{}, httpx.Forbidden("hesap bir bağışçıya bağlı değil")
	}
	var v MyDonorView
	var err error
	if v.Donor, err = a.GetDonor(ctx, c.DonorID); err != nil {
		return v, err
	}
	if v.Eligibility, err = a.CheckEligibility(ctx, c.DonorID, EligibilityRequest{}); err != nil {
		return v, err
	}
	rows, err := a.DB.Query(ctx, `SELECT id, donation_type, collected_at, volume_ml FROM donations
		WHERE donor_id=$1 ORDER BY collected_at DESC LIMIT 50`, c.DonorID)
	if err != nil {
		return v, err
	}
	v.Donations, err = pgx.CollectRows(rows, pgx.RowToStructByPos[DonationSummary])
	if err != nil {
		return v, err
	}
	if v.Appointments, err = a.ListAppointments(ctx, c.DonorID, ""); err != nil {
		return v, err
	}
	v.Deferrals, err = a.ListDeferrals(ctx, c.DonorID)
	return v, err
}
