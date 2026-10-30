package app

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/msboztepe/damarbagis/internal/domain/eligibility"
	"github.com/msboztepe/damarbagis/internal/domain/product"
	"github.com/msboztepe/damarbagis/internal/httpx"
)

// Appointment randevu.
type Appointment struct {
	ID           int64     `json:"id"`
	DonorID      int64     `json:"donor_id"`
	DonorName    string    `json:"donor_name,omitempty"`
	ScheduledAt  time.Time `json:"scheduled_at"`
	DonationType string    `json:"donation_type"`
	Status       string    `json:"status"`
}

// BookAppointment randevu oluşturur. Randevu gününde bağışçının uygun olması gerekir
// (bekleme süresi, erteleme vb.). Bağışçının en fazla bir açık randevusu olabilir.
func (a *App) BookAppointment(ctx context.Context, donorID int64, at time.Time, dtype string) (Appointment, error) {
	now := a.Now()
	if dtype == "" {
		dtype = string(eligibility.WholeBlood)
	}
	if !eligibility.DonationType(dtype).Valid() {
		return Appointment{}, httpx.Validation("geçersiz bağış türü")
	}
	if at.Before(now) || at.After(now.AddDate(0, 0, 90)) {
		return Appointment{}, httpx.Validation("randevu önümüzdeki 90 gün içinde olmalı")
	}
	dec, err := a.CheckEligibility(ctx, donorID, EligibilityRequest{DonationType: dtype, At: at.Format("2006-01-02")})
	if err != nil {
		return Appointment{}, err
	}
	if !dec.Eligible {
		return Appointment{}, httpx.E(http.StatusUnprocessableEntity, "donor_ineligible",
			"bağışçı randevu gününde uygun değil").WithDetails(dec)
	}
	var ap Appointment
	err = a.InTx(ctx, "randevu", func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `INSERT INTO appointments (donor_id, scheduled_at, donation_type) VALUES ($1,$2,$3)
			RETURNING id, donor_id, scheduled_at, donation_type, status`, donorID, at, dtype).
			Scan(&ap.ID, &ap.DonorID, &ap.ScheduledAt, &ap.DonationType, &ap.Status)
		if err != nil {
			if strings.Contains(err.Error(), "appointments_one_open") {
				return httpx.Conflict("bağışçının zaten açık bir randevusu var")
			}
			return err
		}
		return a.Audit(ctx, tx, "appointment.create", "appointment", ap.ID, nil, ap, "")
	})
	return ap, err
}

// CancelAppointment açık randevuyu iptal eder. donorID > 0 ise yalnızca o bağışçının randevusu.
func (a *App) CancelAppointment(ctx context.Context, id, donorID int64) error {
	return a.InTx(ctx, "randevu iptal", func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE appointments SET status='cancelled'
			WHERE id=$1 AND status='booked' AND ($2 = 0 OR donor_id=$2)`, id, donorID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return httpx.NotFound("açık randevu")
		}
		return a.Audit(ctx, tx, "appointment.cancel", "appointment", id, nil, nil, "")
	})
}

// ListAppointments bağışçıya ya da güne göre randevular.
func (a *App) ListAppointments(ctx context.Context, donorID int64, date string) ([]Appointment, error) {
	args := []any{}
	var where string
	if donorID > 0 {
		args = append(args, donorID)
		where = "a.donor_id=$1"
	} else {
		d := day(a.Now())
		if date != "" {
			t, err := time.Parse("2006-01-02", date)
			if err != nil {
				return nil, httpx.Validation("date YYYY-AA-GG olmalı")
			}
			d = t
		}
		args = append(args, d)
		where = "a.scheduled_at >= $1 AND a.scheduled_at < $1::date + 1"
	}
	rows, err := a.DB.Query(ctx, `SELECT a.id, a.donor_id, d.first_name || ' ' || d.last_name, a.scheduled_at, a.donation_type, a.status
		FROM appointments a JOIN donors d ON d.id=a.donor_id WHERE `+where+` ORDER BY a.scheduled_at DESC LIMIT 200`, args...)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[Appointment])
}

// NewDonation bağış kaydı girdisi.
type NewDonation struct {
	DonorID       int64           `json:"donor_id"`
	AppointmentID *int64          `json:"appointment_id"`
	DonationType  string          `json:"donation_type"`
	VolumeML      int             `json:"volume_ml"`
	Hemoglobin    float64         `json:"hemoglobin"`
	WeightKg      float64         `json:"weight_kg"`
	Declarations  []DeclarationIn `json:"declarations"`
	// CollectedAt dış merkezde yapılmış geçmiş bir bağışın sisteme işlenmesi içindir
	// (en fazla 400 gün geri). Boşsa şimdiki zaman kullanılır.
	CollectedAt *time.Time `json:"collected_at"`
}

// Donation bağış.
type Donation struct {
	ID            int64     `json:"id"`
	DonorID       int64     `json:"donor_id"`
	AppointmentID *int64    `json:"appointment_id,omitempty"`
	DonationType  string    `json:"donation_type"`
	CollectedAt   time.Time `json:"collected_at"`
	VolumeML      int       `json:"volume_ml"`
	Hemoglobin    float64   `json:"hemoglobin"`
	WeightKg      float64   `json:"weight_kg"`
	BagNo         string    `json:"bag_no"`
	TestOutcome   *string   `json:"test_outcome"`
	Products      int       `json:"products"`
}

const donationCols = `d.id, d.donor_id, d.appointment_id, d.donation_type, d.collected_at, d.volume_ml,
	d.hemoglobin::float8, d.weight_kg::float8, d.bag_no, t.outcome,
	(SELECT count(*) FROM products p WHERE p.donation_id=d.id AND p.parent_id IS NULL)::int`

func scanDonation(r pgx.Row) (Donation, error) {
	var d Donation
	err := r.Scan(&d.ID, &d.DonorID, &d.AppointmentID, &d.DonationType, &d.CollectedAt, &d.VolumeML,
		&d.Hemoglobin, &d.WeightKg, &d.BagNo, &d.TestOutcome, &d.Products)
	return d, err
}

// RecordDonation bağışı kaydeder. Uygunluk sunucu tarafında, bağışçı satırı kilitliyken
// yeniden değerlendirilir; uygun değilse 422 donor_ineligible ve gerekçeler döner ve red denetime yazılır.
func (a *App) RecordDonation(ctx context.Context, in NewDonation) (Donation, error) {
	if in.DonationType == "" {
		in.DonationType = string(eligibility.WholeBlood)
	}
	if !eligibility.DonationType(in.DonationType).Valid() {
		return Donation{}, httpx.Validation("geçersiz bağış türü")
	}
	if in.VolumeML < 150 || in.VolumeML > 700 {
		return Donation{}, httpx.Validation("volume_ml 150-700 aralığında olmalı")
	}
	now := a.Now()
	at := now
	if in.CollectedAt != nil {
		at = in.CollectedAt.UTC()
		if at.After(now.Add(time.Minute)) || at.Before(now.AddDate(0, 0, -400)) {
			return Donation{}, httpx.Validation("collected_at gelecekte ya da 400 günden eski olamaz")
		}
	}
	decl, err := parseDeclarations(in.Declarations)
	if err != nil {
		return Donation{}, err
	}
	var out Donation
	var rejected *eligibility.Decision
	err = a.InTx(ctx, "bağış kaydı", func(tx pgx.Tx) error {
		f, err := loadFacts(ctx, tx, in.DonorID, true)
		if err != nil {
			return err
		}
		book, err := a.RuleBook(ctx, tx)
		if err != nil {
			return err
		}
		dec := eligibility.Evaluate(eligibility.Input{
			Sex: f.sex, BirthDate: f.birth, Type: eligibility.DonationType(in.DonationType), At: at,
			History: f.history, Deferrals: f.deferrals, Declarations: decl,
			Measurements: &eligibility.Measurements{WeightKg: in.WeightKg, Hemoglobin: in.Hemoglobin},
		}, book)
		if !dec.Eligible {
			rejected = &dec
			return errRollback
		}
		if in.AppointmentID != nil {
			tag, err := tx.Exec(ctx, `UPDATE appointments SET status='completed' WHERE id=$1 AND donor_id=$2 AND status='booked'`,
				*in.AppointmentID, in.DonorID)
			if err != nil {
				return err
			}
			if tag.RowsAffected() == 0 {
				return httpx.Conflict("randevu bulunamadı ya da açık değil")
			}
		}
		var id int64
		if err := tx.QueryRow(ctx, `SELECT nextval(pg_get_serial_sequence('donations','id'))`).Scan(&id); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO donations (id, donor_id, appointment_id, donation_type, collected_at, volume_ml, hemoglobin, weight_kg, bag_no, nurse_id)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, id, in.DonorID, in.AppointmentID, in.DonationType, at,
			in.VolumeML, in.Hemoglobin, in.WeightKg, fmt.Sprintf("B%08d", id), actor(ctx)); err != nil {
			return err
		}
		out, err = scanDonation(tx.QueryRow(ctx, `SELECT `+donationCols+` FROM donations d LEFT JOIN test_results t ON t.donation_id=d.id WHERE d.id=$1`, id))
		if err != nil {
			return err
		}
		return a.Audit(ctx, tx, "donation.create", "donation", id, nil, out, "")
	})
	if rejected != nil {
		// Red kaydı ayrı bir transaction'da yazılır (bağış transaction'ı geri alındı).
		_ = a.InTx(ctx, "", func(tx pgx.Tx) error {
			return a.Audit(ctx, tx, "eligibility.rejected", "donor", in.DonorID, nil, rejected, "")
		})
		return Donation{}, httpx.E(http.StatusUnprocessableEntity, "donor_ineligible", "bağışçı bağış için uygun değil").WithDetails(rejected)
	}
	return out, err
}

var errRollback = fmt.Errorf("rollback")

// GetDonation tek bağış.
func (a *App) GetDonation(ctx context.Context, id int64) (Donation, error) {
	d, err := scanDonation(a.DB.QueryRow(ctx, `SELECT `+donationCols+` FROM donations d LEFT JOIN test_results t ON t.donation_id=d.id WHERE d.id=$1`, id))
	if err == pgx.ErrNoRows {
		return d, httpx.NotFound("bağış")
	}
	return d, err
}

// ListDonations filtre: pending=tests (sonucu girilmemiş) | components (üretilmemiş). Keyset sayfalı.
func (a *App) ListDonations(ctx context.Context, pending string, donorID int64, p httpx.Page) (httpx.List[Donation], error) {
	args := []any{p.Limit + 1}
	where := []string{"true"}
	if p.Cursor > 0 {
		args = append(args, p.Cursor)
		where = append(where, fmt.Sprintf("d.id < $%d", len(args)))
	}
	switch pending {
	case "tests":
		where = append(where, "t.donation_id IS NULL")
	case "components":
		where = append(where, "NOT EXISTS (SELECT 1 FROM products p WHERE p.donation_id=d.id) AND d.collected_at > now() - interval '24 hours'")
	case "":
	default:
		return httpx.List[Donation]{}, httpx.BadRequest("pending tests|components olmalı")
	}
	if donorID > 0 {
		args = append(args, donorID)
		where = append(where, fmt.Sprintf("d.donor_id=$%d", len(args)))
	}
	rows, err := a.DB.Query(ctx, `SELECT `+donationCols+` FROM donations d LEFT JOIN test_results t ON t.donation_id=d.id
		WHERE `+strings.Join(where, " AND ")+` ORDER BY d.id DESC LIMIT $1`, args...)
	if err != nil {
		return httpx.List[Donation]{}, err
	}
	items, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Donation, error) { return scanDonation(r) })
	if err != nil {
		return httpx.List[Donation]{}, err
	}
	return pageOf(items, p.Limit, func(d Donation) int64 { return d.ID }), nil
}

// Product ürün (personel görünümü).
type Product struct {
	ID         int64     `json:"id"`
	ProductNo  string    `json:"product_no"`
	Barcode    string    `json:"barcode"`
	DonationID int64     `json:"donation_id"`
	ParentID   *int64    `json:"parent_id,omitempty"`
	Component  string    `json:"component"`
	ABO        string    `json:"abo"`
	RhPositive bool      `json:"rh_positive"`
	VolumeML   int       `json:"volume_ml"`
	ProducedAt time.Time `json:"produced_at"`
	ExpiresAt  time.Time `json:"expires_at"`
	Status     string    `json:"status"`
}

const productCols = `id, product_no, donation_id, parent_id, component, abo, rh_positive, volume_ml, produced_at, expires_at, status`

func (a *App) scanProduct(r pgx.Row) (Product, error) {
	var p Product
	err := r.Scan(&p.ID, &p.ProductNo, &p.DonationID, &p.ParentID, &p.Component, &p.ABO, &p.RhPositive, &p.VolumeML, &p.ProducedAt, &p.ExpiresAt, &p.Status)
	p.Barcode = product.SignBarcode(a.Cfg.BarcodeSecret, p.ProductNo)
	return p, err
}

// ProduceComponents bağıştan bileşen üretir (tam kan → RBC+PLT+FFP, aferez → PLT).
// SKT toplama anından itibaren raf ömrüyle hesaplanır. Test sonucu yoksa ürün
// pending_test (karantina-öncesi bekleme), negatifse available, pozitifse quarantined başlar.
func (a *App) ProduceComponents(ctx context.Context, donationID int64, components []string) ([]Product, error) {
	var out []Product
	err := a.InTx(ctx, "bileşen üretimi", func(tx pgx.Tx) error {
		var dtype, abo string
		var rh bool
		var collected time.Time
		var outcome *string
		err := tx.QueryRow(ctx, `SELECT d.donation_type, d.collected_at, dn.abo, dn.rh_positive, t.outcome
			FROM donations d JOIN donors dn ON dn.id=d.donor_id LEFT JOIN test_results t ON t.donation_id=d.id
			WHERE d.id=$1 FOR UPDATE OF d`, donationID).Scan(&dtype, &collected, &abo, &rh, &outcome)
		if err == pgx.ErrNoRows {
			return httpx.NotFound("bağış")
		}
		if err != nil {
			return err
		}
		now := a.Now()
		if now.Sub(collected) > 24*time.Hour {
			return httpx.Validation("bileşen üretimi toplamadan sonraki 24 saat içinde yapılmalı")
		}
		allowed := product.ComponentsFor(dtype)
		if len(components) == 0 {
			components = allowed
		}
		shelf, err := a.ShelfAt(ctx, tx, collected)
		if err != nil {
			return err
		}
		status := product.PendingTest
		if outcome != nil {
			status = product.Available
			if *outcome == "positive" {
				status = product.Quarantined
			}
		}
		for _, c := range components {
			if !contains(allowed, c) {
				return httpx.Validation(fmt.Sprintf("%s bağışından %s üretilemez", dtype, c))
			}
			var exists bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM products WHERE donation_id=$1 AND component=$2 AND parent_id IS NULL)`,
				donationID, c).Scan(&exists); err != nil {
				return err
			}
			if exists {
				return httpx.Conflict(c + " bu bağıştan zaten üretilmiş")
			}
			exp, err := shelf.Days.Expiry(c, collected)
			if err != nil {
				return err
			}
			produced := collected.Add(time.Hour)
			if produced.After(now) {
				produced = now
			}
			var id int64
			if err := tx.QueryRow(ctx, `SELECT nextval(pg_get_serial_sequence('products','id'))`).Scan(&id); err != nil {
				return err
			}
			p, err := a.scanProduct(tx.QueryRow(ctx, `INSERT INTO products (id, product_no, donation_id, component, abo, rh_positive, volume_ml, produced_at, expires_at, status)
				VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) RETURNING `+productCols,
				id, fmt.Sprintf("P%08d", id), donationID, c, abo, rh, product.DefaultVolume(c, dtype), produced, exp, string(status)))
			if err != nil {
				return err
			}
			out = append(out, p)
		}
		return a.Audit(ctx, tx, "donation.produce", "donation", donationID, nil, out, "")
	})
	return out, err
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}
