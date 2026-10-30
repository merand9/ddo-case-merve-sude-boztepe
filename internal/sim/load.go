package sim

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/msboztepe/damarbagis/internal/secure"
)

// Seeded veritabanının daha önce seed edilip edilmediğini döner.
func Seeded(ctx context.Context, db *pgxpool.Pool) (bool, uint64, time.Time, error) {
	var seed int64
	var anchor time.Time
	err := db.QueryRow(ctx, `SELECT seed, anchor FROM sim_state WHERE id=1`).Scan(&seed, &anchor)
	if err == pgx.ErrNoRows {
		return false, 0, time.Time{}, nil
	}
	return err == nil, uint64(seed), anchor, err
}

// Load veri setini tek bir transaction içinde COPY ile yükler. Yerel numaralar, her tablonun
// kimlik dizisinden ayrılan bloğa kaydırılır (API'nin daha önce oluşturduğu kayıtlarla çakışmaz).
func Load(ctx context.Context, db *pgxpool.Pool, ds *Dataset, c Config, sealer *secure.Sealer) error {
	return pgx.BeginFunc(ctx, db, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT set_config('app.bulk_load','on',true)`); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `LOCK TABLE sim_state IN EXCLUSIVE MODE`); err != nil {
			return err
		}
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM sim_state)`).Scan(&exists); err != nil {
			return err
		}
		if exists {
			return nil
		}
		base := map[string]int64{}
		for tbl, n := range map[string]int{"donors": len(ds.Donors), "donations": len(ds.Donations), "products": len(ds.Products),
			"requests": len(ds.Requests), "reservations": len(ds.Reservations)} {
			b, err := reserve(ctx, tx, tbl, n)
			if err != nil {
				return err
			}
			base[tbl] = b
		}
		did := func(n int) int64 { return base["donors"] + int64(n) }
		dnid := func(n int) int64 { return base["donations"] + int64(n) }
		pid := func(n int) int64 { return base["products"] + int64(n) }
		rid := func(n int) int64 { return base["requests"] + int64(n) }

		copyRows := func(table string, cols []string, n int, row func(i int) ([]any, error)) error {
			_, err := tx.CopyFrom(ctx, pgx.Identifier{table}, cols, pgx.CopyFromFunc(func() func() ([]any, error) {
				i := 0
				return func() ([]any, error) {
					if i >= n {
						return nil, nil
					}
					r, err := row(i)
					i++
					return r, err
				}
			}()))
			if err != nil {
				return fmt.Errorf("%s yüklenemedi: %w", table, err)
			}
			return nil
		}

		if err := copyRows("donors", []string{"id", "donor_no", "first_name", "last_name", "sex", "birth_date", "abo", "rh_positive", "phone", "health_enc", "created_at"},
			len(ds.Donors), func(i int) ([]any, error) {
				d := ds.Donors[i]
				id := did(d.N)
				no := fmt.Sprintf("D%07d", id)
				enc, err := sealer.SealWithNonce(d.HealthJSON, []byte(no), Nonce(c.Seed, d.N))
				return []any{id, no, d.First, d.Last, d.Sex, d.Birth, d.ABO, d.Rh, d.Phone, enc, d.Created}, err
			}); err != nil {
			return err
		}
		if err := copyRows("deferrals", []string{"donor_id", "reason_code", "starts_on", "ends_on", "note"}, len(ds.Deferrals), func(i int) ([]any, error) {
			f := ds.Deferrals[i]
			return []any{did(f.Donor), f.Code, f.Start, f.End, f.Note}, nil
		}); err != nil {
			return err
		}
		if err := copyRows("donations", []string{"id", "donor_id", "donation_type", "collected_at", "volume_ml", "hemoglobin", "weight_kg", "bag_no", "created_at"},
			len(ds.Donations), func(i int) ([]any, error) {
				d := ds.Donations[i]
				id := dnid(d.N)
				return []any{id, did(d.Donor), d.Type, d.At, d.Volume, d.Hb, d.Weight, fmt.Sprintf("B%08d", id), d.At}, nil
			}); err != nil {
			return err
		}
		if err := copyRows("test_results", []string{"donation_id", "hiv", "hbv", "hcv", "syphilis", "outcome", "entered_at"}, len(ds.Tests), func(i int) ([]any, error) {
			t := ds.Tests[i]
			out := "negative"
			if t.HIV || t.HBV || t.HCV || t.Syphilis {
				out = "positive"
			}
			return []any{dnid(t.Donation), t.HIV, t.HBV, t.HCV, t.Syphilis, out, t.At}, nil
		}); err != nil {
			return err
		}
		if err := copyRows("products", []string{"id", "product_no", "donation_id", "component", "abo", "rh_positive", "volume_ml", "produced_at", "expires_at", "status", "updated_at"},
			len(ds.Products), func(i int) ([]any, error) {
				p := ds.Products[i]
				id := pid(p.N)
				return []any{id, fmt.Sprintf("P%08d", id), dnid(p.Donation), p.Component, p.ABO, p.Rh, p.Volume, p.Produced, p.Expires, p.Status, p.Produced}, nil
			}); err != nil {
			return err
		}
		if err := copyRows("product_events", []string{"product_id", "from_status", "to_status", "reason", "at"}, len(ds.Events), func(i int) ([]any, error) {
			e := ds.Events[i]
			var from any
			if e.From != "" {
				from = e.From
			}
			return []any{pid(e.Product), from, e.To, e.Reason, e.At}, nil
		}); err != nil {
			return err
		}
		if err := copyRows("requests", []string{"id", "hospital_id", "component", "abo", "rh_positive", "units", "urgency", "patient_ref", "status", "auto_allocate", "created_at"},
			len(ds.Requests), func(i int) ([]any, error) {
				r := ds.Requests[i]
				return []any{rid(r.N), int64(r.Hospital), r.Component, r.ABO, r.Rh, r.Units, r.Urgency, r.Patient, r.Status, false, r.At}, nil
			}); err != nil {
			return err
		}
		if err := copyRows("reservations", []string{"id", "request_id", "product_id", "status", "reserved_at", "expires_at", "closed_at"},
			len(ds.Reservations), func(i int) ([]any, error) {
				r := ds.Reservations[i]
				return []any{base["reservations"] + int64(r.N), rid(r.Request), pid(r.Product), r.Status, r.At, r.Expires, r.Closed}, nil
			}); err != nil {
			return err
		}
		if err := copyRows("distributions", []string{"request_id", "product_id", "hospital_id", "issued_at"}, len(ds.Distributions), func(i int) ([]any, error) {
			d := ds.Distributions[i]
			return []any{rid(d.Request), pid(d.Product), int64(d.Hospital), d.At}, nil
		}); err != nil {
			return err
		}
		if err := copyRows("transfusions", []string{"product_id", "hospital_id", "patient_ref", "transfused_at"}, len(ds.Transfusions), func(i int) ([]any, error) {
			t := ds.Transfusions[i]
			return []any{pid(t.Product), int64(t.Hospital), t.Patient, t.At}, nil
		}); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO sim_state (id, seed, anchor, donors) VALUES (1,$1,$2,$3)`, int64(c.Seed), c.Anchor, c.Donors); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO audit_log (action, entity, new_value) VALUES ('sim.seed','system',$1)`,
			map[string]any{"seed": c.Seed, "anchor": c.Anchor.Format("2006-01-02"), "fingerprint": ds.Fingerprint(),
				"donors": len(ds.Donors), "donations": len(ds.Donations), "products": len(ds.Products)})
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `ANALYZE`)
		return err
	})
}

// reserve tablonun kimlik dizisinden n'lik blok ayırır ve bloğun başlangıç-1 değerini döner.
func reserve(ctx context.Context, tx pgx.Tx, table string, n int) (int64, error) {
	var last int64
	err := tx.QueryRow(ctx, `SELECT setval(pg_get_serial_sequence($1,'id'),
		greatest((SELECT coalesce(max(id),0) FROM `+pgx.Identifier{table}.Sanitize()+`),
		         (SELECT last_value FROM `+seqName(table)+`)) + $2)`, table, n).Scan(&last)
	return last - int64(n), err
}

func seqName(table string) string { return table + "_id_seq" }
