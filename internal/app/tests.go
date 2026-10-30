package app

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/msboztepe/damarbagis/internal/httpx"
)

// Markers test sonuçları (true = reaktif/pozitif).
type Markers struct {
	HIV      bool `json:"hiv"`
	HBV      bool `json:"hbv"`
	HCV      bool `json:"hcv"`
	Syphilis bool `json:"syphilis"`
}

// Positive herhangi bir belirteç pozitif mi.
func (m Markers) Positive() bool { return m.HIV || m.HBV || m.HCV || m.Syphilis }

func (m Markers) outcome() string {
	if m.Positive() {
		return "positive"
	}
	return "negative"
}

func (m Markers) deferralCodes() []string {
	var out []string
	if m.HIV {
		out = append(out, "hiv_positive")
	}
	if m.HBV {
		out = append(out, "hbv_positive")
	}
	if m.HCV {
		out = append(out, "hcv_positive")
	}
	if m.Syphilis {
		out = append(out, "syphilis_positive")
	}
	return out
}

// AffectedProduct karantina/geri çağırma etkisindeki ürün.
type AffectedProduct struct {
	ProductID  int64   `json:"product_id"`
	ProductNo  string  `json:"product_no"`
	Component  string  `json:"component"`
	DonationID int64   `json:"donation_id"`
	Depth      int     `json:"depth"` // 0 = kök ürün, 1+ = bölünmüş alt ürün
	From       string  `json:"from"`
	To         string  `json:"to"`
	Hospital   *string `json:"hospital,omitempty"`
	PatientRef *string `json:"patient_ref,omitempty"`
}

// TestResultOutcome test girişi sonucu.
type TestResultOutcome struct {
	DonationID  int64             `json:"donation_id"`
	Outcome     string            `json:"outcome"`
	Released    int               `json:"released"`
	Quarantined []AffectedProduct `json:"quarantined"`
	// Recalls: hastaneye dağıtılmış ürünler (geri çağrılır) ve transfüze edilmişler (hasta bilgilendirilir).
	Recalls  []AffectedProduct `json:"recalls"`
	Lookback []int64           `json:"lookback_donations"`
}

// EnterTestResult bir bağışın ilk test sonucunu girer.
//   - Negatif: pending_test durumundaki tüm türev ürünler available olur.
//   - Pozitif: bağışın tüm türev ürünleri (bölünmüş alt ürünler dahil, recursive) karantinaya
//     alınır; aktif rezervasyonlar serbest bırakılır; dağıtılmış ürünler geri çağrılır;
//     bağışçıya kalıcı red eklenir; look-back penceresindeki önceki bağışlarının ürünleri de
//     karantinaya alınır.
func (a *App) EnterTestResult(ctx context.Context, donationID int64, m Markers) (TestResultOutcome, error) {
	out := TestResultOutcome{DonationID: donationID, Outcome: m.outcome(), Quarantined: []AffectedProduct{}, Recalls: []AffectedProduct{}, Lookback: []int64{}}
	reason := "test sonucu: " + out.Outcome
	err := a.InTx(ctx, reason, func(tx pgx.Tx) error {
		var donorID int64
		if err := tx.QueryRow(ctx, `SELECT donor_id FROM donations WHERE id=$1 FOR UPDATE`, donationID).Scan(&donorID); err != nil {
			if err == pgx.ErrNoRows {
				return httpx.NotFound("bağış")
			}
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO test_results (donation_id, hiv, hbv, hcv, syphilis, outcome, entered_by)
			VALUES ($1,$2,$3,$4,$5,$6,$7)`, donationID, m.HIV, m.HBV, m.HCV, m.Syphilis, out.Outcome, actor(ctx))
		if err != nil {
			if strings.Contains(err.Error(), "test_results_pkey") {
				return httpx.Conflict("bu bağışın test sonucu zaten girilmiş; değişiklik için çift onaylı talep oluşturun")
			}
			return err
		}
		if !m.Positive() {
			tag, err := tx.Exec(ctx, `UPDATE products SET status='available' WHERE donation_id=$1 AND status='pending_test'`, donationID)
			if err != nil {
				return err
			}
			out.Released = int(tag.RowsAffected())
		} else {
			if err := a.applyPositive(ctx, tx, donorID, donationID, m, &out); err != nil {
				return err
			}
		}
		return a.Audit(ctx, tx, "test_result.create", "donation", donationID, nil, map[string]any{"markers": m, "result": out}, "")
	})
	return out, err
}

func (a *App) applyPositive(ctx context.Context, tx pgx.Tx, donorID, donationID int64, m Markers, out *TestResultOutcome) error {
	shelf, err := a.ShelfAt(ctx, tx, a.Now())
	if err != nil {
		return err
	}
	// Look-back: aynı bağışçının pencere içindeki önceki bağışları.
	rows, err := tx.Query(ctx, `SELECT id FROM donations WHERE donor_id=$1 AND id<>$2
		AND collected_at >= now() - make_interval(days => $3) ORDER BY id`, donorID, donationID, shelf.LookbackDays)
	if err != nil {
		return err
	}
	prior, err := pgx.CollectRows(rows, pgx.RowTo[int64])
	if err != nil {
		return err
	}
	out.Lookback = append(out.Lookback, prior...)
	affected, err := quarantineDonations(ctx, tx, append([]int64{donationID}, prior...))
	if err != nil {
		return err
	}
	for _, p := range affected {
		if p.To == "quarantined" && p.From != "issued" {
			out.Quarantined = append(out.Quarantined, p)
		} else {
			out.Recalls = append(out.Recalls, p)
		}
	}
	for _, code := range m.deferralCodes() {
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM deferrals WHERE donor_id=$1 AND reason_code=$2 AND ends_on IS NULL)`,
			donorID, code).Scan(&exists); err != nil {
			return err
		}
		if exists {
			continue
		}
		if _, err := insertDeferral(ctx, tx, donorID, code, day(a.Now()), nil, fmt.Sprintf("test:%d", donationID), actor(ctx)); err != nil {
			return err
		}
	}
	return nil
}

// quarantineDonations verilen bağışların kök ürünlerinden başlayıp parent_id üzerinden tüm alt
// ürünleri recursive gezer; stoktaki/rezerve/dağıtılmış olanları karantinaya alır.
// Transfüze edilmiş ürünler değiştirilemez; hasta bilgilendirmesi için listelenir.
func quarantineDonations(ctx context.Context, tx pgx.Tx, donationIDs []int64) ([]AffectedProduct, error) {
	rows, err := tx.Query(ctx, `
		WITH RECURSIVE tree AS (
			SELECT p.id, 0 AS depth FROM products p
			WHERE p.donation_id = ANY($1) AND p.parent_id IS NULL
			UNION ALL
			SELECT c.id, t.depth + 1 FROM products c JOIN tree t ON c.parent_id = t.id
		)
		SELECT p.id, p.product_no, p.component::text, p.donation_id, t.depth, p.status::text, h.name, tr.patient_ref
		FROM tree t JOIN products p ON p.id = t.id
		LEFT JOIN distributions d ON d.product_id = p.id
		LEFT JOIN hospitals h ON h.id = d.hospital_id
		LEFT JOIN transfusions tr ON tr.product_id = p.id
		ORDER BY p.id
		FOR UPDATE OF p`, donationIDs)
	if err != nil {
		return nil, err
	}
	all, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (AffectedProduct, error) {
		var p AffectedProduct
		err := r.Scan(&p.ProductID, &p.ProductNo, &p.Component, &p.DonationID, &p.Depth, &p.From, &p.Hospital, &p.PatientRef)
		return p, err
	})
	if err != nil {
		return nil, err
	}
	var out []AffectedProduct
	var ids []int64
	for _, p := range all {
		switch p.From {
		case "pending_test", "available", "reserved", "issued":
			p.To = "quarantined"
			ids = append(ids, p.ProductID)
			out = append(out, p)
		case "transfused":
			p.To = "transfused"
			out = append(out, p)
		}
	}
	if len(ids) == 0 {
		return out, nil
	}
	rrows, err := tx.Query(ctx, `UPDATE reservations SET status='released', closed_at=now()
		WHERE product_id = ANY($1) AND status='active' RETURNING request_id`, ids)
	if err != nil {
		return nil, err
	}
	rids, err := pgx.CollectRows(rrows, pgx.RowTo[int64])
	if err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `UPDATE products SET status='quarantined' WHERE id = ANY($1)`, ids); err != nil {
		return nil, err
	}
	for _, r := range rids {
		if _, err := refreshRequestStatus(ctx, tx, r); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// TestChange çift onaylı test sonucu değişiklik talebi.
type TestChange struct {
	ID          int64      `json:"id"`
	DonationID  int64      `json:"donation_id"`
	Proposed    Markers    `json:"proposed"`
	Current     *Markers   `json:"current,omitempty"`
	Reason      string     `json:"reason"`
	RequestedBy int64      `json:"requested_by"`
	RequestedAt time.Time  `json:"requested_at"`
	Status      string     `json:"status"`
	DecidedBy   *int64     `json:"decided_by,omitempty"`
	DecidedAt   *time.Time `json:"decided_at,omitempty"`
}

// RequestTestChange değişiklik talebi oluşturur (ilk onay). Uygulanması için ikinci,
// farklı bir laboratuvar uzmanının onayı gerekir.
func (a *App) RequestTestChange(ctx context.Context, donationID int64, proposed Markers, reason string) (TestChange, error) {
	if len(strings.TrimSpace(reason)) < 5 {
		return TestChange{}, httpx.Validation("değişiklik gerekçesi en az 5 karakter olmalı")
	}
	var tc TestChange
	err := a.InTx(ctx, "", func(tx pgx.Tx) error {
		var cur Markers
		if err := tx.QueryRow(ctx, `SELECT hiv, hbv, hcv, syphilis FROM test_results WHERE donation_id=$1`, donationID).
			Scan(&cur.HIV, &cur.HBV, &cur.HCV, &cur.Syphilis); err != nil {
			if err == pgx.ErrNoRows {
				return httpx.NotFound("test sonucu")
			}
			return err
		}
		if cur == proposed {
			return httpx.Validation("önerilen değerler mevcut sonuçla aynı")
		}
		pj, _ := json.Marshal(proposed)
		var raw []byte
		err := tx.QueryRow(ctx, `INSERT INTO test_change_requests (donation_id, proposed, reason, requested_by)
			VALUES ($1,$2,$3,$4) RETURNING id, donation_id, proposed, reason, requested_by, requested_at, status`,
			donationID, pj, reason, actor(ctx)).Scan(&tc.ID, &tc.DonationID, &raw, &tc.Reason, &tc.RequestedBy, &tc.RequestedAt, &tc.Status)
		if err != nil {
			if strings.Contains(err.Error(), "test_change_one_pending") {
				return httpx.Conflict("bu bağış için bekleyen bir değişiklik talebi var")
			}
			return err
		}
		_ = json.Unmarshal(raw, &tc.Proposed)
		tc.Current = &cur
		return a.Audit(ctx, tx, "test_result.change_requested", "donation", donationID, cur, proposed, reason)
	})
	return tc, err
}

// DecideTestChange ikinci onay ya da red. Talebi oluşturan kişi kendi talebini onaylayamaz.
func (a *App) DecideTestChange(ctx context.Context, id int64, approve bool) (TestChange, TestResultOutcome, error) {
	var tc TestChange
	res := TestResultOutcome{Quarantined: []AffectedProduct{}, Recalls: []AffectedProduct{}, Lookback: []int64{}}
	me := actor(ctx)
	err := a.InTx(ctx, "test sonucu düzeltmesi (çift onay)", func(tx pgx.Tx) error {
		var raw []byte
		err := tx.QueryRow(ctx, `SELECT id, donation_id, proposed, reason, requested_by, requested_at, status
			FROM test_change_requests WHERE id=$1 FOR UPDATE`, id).
			Scan(&tc.ID, &tc.DonationID, &raw, &tc.Reason, &tc.RequestedBy, &tc.RequestedAt, &tc.Status)
		if err == pgx.ErrNoRows {
			return httpx.NotFound("değişiklik talebi")
		}
		if err != nil {
			return err
		}
		_ = json.Unmarshal(raw, &tc.Proposed)
		if tc.Status != "pending" {
			return httpx.Conflict("talep zaten sonuçlandırılmış")
		}
		if me == nil || *me == tc.RequestedBy {
			return httpx.E(403, "self_approval_forbidden", "talebi oluşturan kişi onaylayamaz; ikinci bir laboratuvar uzmanı gerekir")
		}
		newStatus := "rejected"
		if approve {
			newStatus = "approved"
		}
		if err := tx.QueryRow(ctx, `UPDATE test_change_requests SET status=$2, decided_by=$3, decided_at=now()
			WHERE id=$1 RETURNING status, decided_by, decided_at`, id, newStatus, *me).Scan(&tc.Status, &tc.DecidedBy, &tc.DecidedAt); err != nil {
			return err
		}
		if !approve {
			return a.Audit(ctx, tx, "test_result.change_rejected", "donation", tc.DonationID, nil, tc, "")
		}
		var old Markers
		var donorID int64
		if err := tx.QueryRow(ctx, `SELECT t.hiv, t.hbv, t.hcv, t.syphilis, d.donor_id FROM test_results t
			JOIN donations d ON d.id=t.donation_id WHERE t.donation_id=$1 FOR UPDATE OF t`, tc.DonationID).
			Scan(&old.HIV, &old.HBV, &old.HCV, &old.Syphilis, &donorID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `SELECT set_config('app.approved_change', $1, true)`, strconv.FormatInt(id, 10)); err != nil {
			return err
		}
		p := tc.Proposed
		if _, err := tx.Exec(ctx, `UPDATE test_results SET hiv=$2, hbv=$3, hcv=$4, syphilis=$5, outcome=$6 WHERE donation_id=$1`,
			tc.DonationID, p.HIV, p.HBV, p.HCV, p.Syphilis, p.outcome()); err != nil {
			return err
		}
		res.DonationID, res.Outcome = tc.DonationID, p.outcome()
		switch {
		case !old.Positive() && p.Positive():
			if err := a.applyPositive(ctx, tx, donorID, tc.DonationID, p, &res); err != nil {
				return err
			}
		case old.Positive() && !p.Positive():
			// Yanlış pozitif düzeltmesi: bu bağışın karantinadaki, süresi dolmamış ürünleri serbest
			// bırakılır; bu teste bağlı kalıcı ret kapatılır.
			tag, err := tx.Exec(ctx, `UPDATE products SET status='available'
				WHERE donation_id=$1 AND status='quarantined' AND expires_at > now()`, tc.DonationID)
			if err != nil {
				return err
			}
			res.Released = int(tag.RowsAffected())
			if _, err := tx.Exec(ctx, `UPDATE deferrals SET ends_on = greatest(starts_on + 1, current_date),
				note = coalesce(note,'') || ' | düzeltildi: talep ' || $2
				WHERE donor_id=$1 AND ends_on IS NULL AND note = $3`, donorID, strconv.FormatInt(id, 10), fmt.Sprintf("test:%d", tc.DonationID)); err != nil {
				return err
			}
		}
		return a.Audit(ctx, tx, "test_result.change_approved", "donation", tc.DonationID, old, p, tc.Reason)
	})
	return tc, res, err
}

// ListTestChanges taleplerin listesi.
func (a *App) ListTestChanges(ctx context.Context, status string) ([]TestChange, error) {
	rows, err := a.DB.Query(ctx, `SELECT c.id, c.donation_id, c.proposed, c.reason, c.requested_by, c.requested_at, c.status, c.decided_by, c.decided_at,
			t.hiv, t.hbv, t.hcv, t.syphilis
		FROM test_change_requests c JOIN test_results t ON t.donation_id=c.donation_id
		WHERE ($1 = '' OR c.status=$1) ORDER BY c.id DESC LIMIT 100`, status)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (TestChange, error) {
		var tc TestChange
		var raw []byte
		var cur Markers
		err := r.Scan(&tc.ID, &tc.DonationID, &raw, &tc.Reason, &tc.RequestedBy, &tc.RequestedAt, &tc.Status, &tc.DecidedBy, &tc.DecidedAt,
			&cur.HIV, &cur.HBV, &cur.HCV, &cur.Syphilis)
		_ = json.Unmarshal(raw, &tc.Proposed)
		tc.Current = &cur
		return tc, err
	})
}
