package app

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/msboztepe/damarbagis/internal/domain/compat"
	"github.com/msboztepe/damarbagis/internal/httpx"
)

// Request hastane talebi.
type Request struct {
	ID           int64         `json:"id"`
	HospitalID   int64         `json:"hospital_id"`
	HospitalName string        `json:"hospital_name"`
	Component    string        `json:"component"`
	ABO          string        `json:"abo"`
	RhPositive   bool          `json:"rh_positive"`
	Group        string        `json:"group"`
	Units        int           `json:"units"`
	Urgency      string        `json:"urgency"`
	PatientRef   string        `json:"patient_ref"`
	NeededBy     *time.Time    `json:"needed_by,omitempty"`
	Status       string        `json:"status"`
	CreatedAt    time.Time     `json:"created_at"`
	Reservations []Reservation `json:"reservations"`
}

// Reservation rezervasyon; hastaneye yalnızca ürün bilgisi gösterilir (bağış/bağışçı yok).
type Reservation struct {
	ID         int64      `json:"id"`
	ProductID  int64      `json:"product_id"`
	ProductNo  string     `json:"product_no"`
	Component  string     `json:"component"`
	ABO        string     `json:"abo"`
	RhPositive bool       `json:"rh_positive"`
	Group      string     `json:"group"`
	ExpiresAt  time.Time  `json:"product_expires_at"`
	Status     string     `json:"status"`
	HoldUntil  time.Time  `json:"hold_until"`
	ClosedAt   *time.Time `json:"closed_at,omitempty"`
	Exact      bool       `json:"exact_match"`
}

// NewRequest talep girdisi.
type NewRequest struct {
	Component  string     `json:"component"`
	Group      string     `json:"group"`
	Units      int        `json:"units"`
	Urgency    string     `json:"urgency"`
	PatientRef string     `json:"patient_ref"`
	NeededBy   *time.Time `json:"needed_by"`
}

var urgencies = map[string]bool{"routine": true, "urgent": true, "emergency": true}

// CreateRequest hastane talebi oluşturur ve hemen tahsis dener.
func (a *App) CreateRequest(ctx context.Context, hospitalID int64, in NewRequest) (Request, error) {
	g, err := compat.ParseGroup(in.Group)
	if err != nil {
		return Request{}, httpx.Validation(err.Error())
	}
	if !compat.Component(in.Component).Valid() {
		return Request{}, httpx.Validation("component RBC, PLT ya da FFP olmalı")
	}
	if in.Units < 1 || in.Units > 20 {
		return Request{}, httpx.Validation("units 1-20 olmalı")
	}
	if in.Urgency == "" {
		in.Urgency = "routine"
	}
	if !urgencies[in.Urgency] {
		return Request{}, httpx.Validation("urgency routine, urgent ya da emergency olmalı")
	}
	in.PatientRef = strings.TrimSpace(in.PatientRef)
	if len(in.PatientRef) < 3 || len(in.PatientRef) > 64 {
		return Request{}, httpx.Validation("patient_ref 3-64 karakter olmalı (takma ad / protokol no)")
	}
	var id int64
	err = a.InTx(ctx, "hastane talebi", func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `INSERT INTO requests (hospital_id, component, abo, rh_positive, units, urgency, patient_ref, needed_by, created_by)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9) RETURNING id`, hospitalID, in.Component, g.ABO, g.RhPositive, in.Units, in.Urgency,
			in.PatientRef, in.NeededBy, actor(ctx)).Scan(&id); err != nil {
			return err
		}
		return a.Audit(ctx, tx, "request.create", "request", id, nil, in, "")
	})
	if err != nil {
		return Request{}, err
	}
	ttl, err := a.defaultTTL(ctx)
	if err != nil {
		return Request{}, err
	}
	if _, err := a.Allocate(ctx, id, ttl); err != nil {
		return Request{}, err
	}
	return a.GetRequest(ctx, id, 0)
}

func (a *App) defaultTTL(ctx context.Context) (time.Duration, error) {
	s, err := a.ShelfAt(ctx, nil, a.Now())
	if err != nil {
		return 0, err
	}
	return time.Duration(s.ReservationTTLMinutes) * time.Minute, nil
}

// AllocationResult tahsis sonucu.
type AllocationResult struct {
	RequestID int64   `json:"request_id"`
	Reserved  []int64 `json:"reserved_product_ids"`
	Preempted []int64 `json:"preempted_reservation_ids"`
	Missing   int     `json:"missing_units"`
	Status    string  `json:"status"`
}

// Allocate talep için ürün rezerve eder.
//
// Sıralama: compat.Candidates (önce tam uyum, sonra uyumlu alternatifler) ve her kademede FEFO
// (en erken SKT önce). Ürünler SELECT … FOR UPDATE SKIP LOCKED ile seçilir: eşzamanlı iki tahsis
// aynı ürünü göremez, birbirini beklemez. Ek güvence olarak reservations_one_active benzersiz
// dizini aynı ürünün iki aktif rezervasyonunu veritabanı düzeyinde imkânsız kılar.
//
// Acil talepte stok yetmezse, rutin taleplerin aktif rezervasyonları (yine uyum + FEFO
// sırasıyla) acil talebe devredilir; rutin talep tekrar kuyruğa girer.
func (a *App) Allocate(ctx context.Context, requestID int64, ttl time.Duration) (AllocationResult, error) {
	res := AllocationResult{RequestID: requestID, Reserved: []int64{}, Preempted: []int64{}}
	if ttl < time.Second || ttl > 24*time.Hour {
		return res, httpx.Validation("rezervasyon süresi 1 sn - 24 saat olmalı")
	}
	err := a.InTx(ctx, "tahsis", func(tx pgx.Tx) error {
		var comp, abo, urgency, status string
		var rh bool
		var units int
		err := tx.QueryRow(ctx, `SELECT component, abo, rh_positive, units, urgency, status FROM requests WHERE id=$1 FOR UPDATE`, requestID).
			Scan(&comp, &abo, &rh, &units, &urgency, &status)
		if err == pgx.ErrNoRows {
			return httpx.NotFound("talep")
		}
		if err != nil {
			return err
		}
		if status == "issued" || status == "cancelled" {
			return httpx.Conflict("talep " + status + " durumunda; tahsis yapılamaz")
		}
		var held int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM reservations WHERE request_id=$1 AND status IN ('active','issued')`, requestID).Scan(&held); err != nil {
			return err
		}
		need := units - held
		if need > 0 {
			cands := compat.Candidates(compat.Component(comp), compat.Group{ABO: abo, RhPositive: rh})
			abos, rhs, ranks := candArrays(cands)
			holdUntil := a.Now().Add(ttl)
			rows, err := tx.Query(ctx, `
				WITH c(abo, rh, rnk) AS (SELECT * FROM unnest($2::abo_group[], $3::bool[], $4::int[]))
				SELECT p.id FROM products p
				JOIN c ON c.abo = p.abo AND c.rh = p.rh_positive
				WHERE p.status = 'available' AND p.component = $1 AND p.expires_at > $5
				ORDER BY c.rnk, p.expires_at, p.id
				LIMIT $6
				FOR UPDATE OF p SKIP LOCKED`, comp, abos, rhs, ranks, holdUntil, need)
			if err != nil {
				return err
			}
			ids, err := pgx.CollectRows(rows, pgx.RowTo[int64])
			if err != nil {
				return err
			}
			if len(ids) > 0 {
				if _, err := tx.Exec(ctx, `UPDATE products SET status='reserved' WHERE id = ANY($1)`, ids); err != nil {
					return err
				}
				if _, err := tx.Exec(ctx, `INSERT INTO reservations (request_id, product_id, expires_at)
					SELECT $1, unnest($2::bigint[]), $3`, requestID, ids, holdUntil); err != nil {
					return err
				}
				res.Reserved = append(res.Reserved, ids...)
				need -= len(ids)
			}
			if need > 0 && urgency == "emergency" {
				taken, err := a.preempt(ctx, tx, requestID, comp, abos, rhs, ranks, need, holdUntil)
				if err != nil {
					return err
				}
				res.Preempted = taken
				need -= len(taken)
			}
		}
		res.Missing = max(need, 0)
		newStatus, err := refreshRequestStatus(ctx, tx, requestID)
		if err != nil {
			return err
		}
		res.Status = newStatus
		if len(res.Reserved) > 0 || len(res.Preempted) > 0 {
			return a.Audit(ctx, tx, "request.allocate", "request", requestID, nil, res, "")
		}
		return nil
	})
	return res, err
}

func (a *App) preempt(ctx context.Context, tx pgx.Tx, requestID int64, comp string, abos []string, rhs []bool, ranks []int, need int, holdUntil time.Time) ([]int64, error) {
	rows, err := tx.Query(ctx, `
		WITH c(abo, rh, rnk) AS (SELECT * FROM unnest($2::abo_group[], $3::bool[], $4::int[]))
		SELECT r.id, r.product_id, r.request_id FROM reservations r
		JOIN products p ON p.id = r.product_id
		JOIN requests q ON q.id = r.request_id
		JOIN c ON c.abo = p.abo AND c.rh = p.rh_positive
		WHERE r.status = 'active' AND q.urgency = 'routine' AND p.component = $1 AND p.expires_at > $5
		ORDER BY c.rnk, p.expires_at, r.id
		LIMIT $6
		FOR UPDATE OF r, p SKIP LOCKED`, comp, abos, rhs, ranks, holdUntil, need)
	if err != nil {
		return nil, err
	}
	type victim struct{ resID, productID, requestID int64 }
	victims, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (victim, error) {
		var v victim
		err := r.Scan(&v.resID, &v.productID, &v.requestID)
		return v, err
	})
	if err != nil {
		return nil, err
	}
	var taken []int64
	for _, v := range victims {
		if _, err := tx.Exec(ctx, `UPDATE reservations SET status='preempted', closed_at=now() WHERE id=$1`, v.resID); err != nil {
			return nil, err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO reservations (request_id, product_id, expires_at) VALUES ($1,$2,$3)`,
			requestID, v.productID, holdUntil); err != nil {
			return nil, err
		}
		if _, err := refreshRequestStatus(ctx, tx, v.requestID); err != nil {
			return nil, err
		}
		if err := a.Audit(ctx, tx, "reservation.preempted", "request", v.requestID, map[string]any{"reservation_id": v.resID, "product_id": v.productID},
			map[string]any{"emergency_request_id": requestID}, "acil talep önceliği"); err != nil {
			return nil, err
		}
		taken = append(taken, v.resID)
	}
	return taken, nil
}

func candArrays(cs []compat.Candidate) ([]string, []bool, []int) {
	abos := make([]string, len(cs))
	rhs := make([]bool, len(cs))
	ranks := make([]int, len(cs))
	for i, c := range cs {
		abos[i], rhs[i], ranks[i] = c.Group.ABO, c.Group.RhPositive, c.Rank
	}
	return abos, rhs, ranks
}

// refreshRequestStatus rezervasyon/dağıtım sayısına göre talep durumunu günceller.
func refreshRequestStatus(ctx context.Context, tx pgx.Tx, id int64) (string, error) {
	var s string
	err := tx.QueryRow(ctx, `
		UPDATE requests q SET status = CASE
			WHEN q.status = 'cancelled' THEN 'cancelled'
			WHEN x.issued >= q.units THEN 'issued'
			WHEN x.issued + x.active >= q.units THEN 'reserved'
			WHEN x.issued + x.active > 0 THEN 'partial'
			ELSE 'open' END
		FROM (SELECT count(*) FILTER (WHERE status='issued') AS issued,
		             count(*) FILTER (WHERE status='active') AS active
		      FROM reservations WHERE request_id=$1) x
		WHERE q.id=$1 RETURNING q.status`, id).Scan(&s)
	return s, err
}

// ReleaseExpired süresi dolan aktif rezervasyonları serbest bırakır; ürünler stoğa döner.
func (a *App) ReleaseExpired(ctx context.Context) (int, error) {
	n := 0
	err := a.InTx(ctx, "rezervasyon süresi doldu", func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `UPDATE reservations SET status='released', closed_at=now()
			WHERE id IN (SELECT id FROM reservations WHERE status='active' AND expires_at < now()
			             ORDER BY id FOR UPDATE SKIP LOCKED)
			RETURNING product_id, request_id`)
		if err != nil {
			return err
		}
		type pr struct{ p, r int64 }
		items, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (pr, error) {
			var x pr
			err := r.Scan(&x.p, &x.r)
			return x, err
		})
		if err != nil || len(items) == 0 {
			return err
		}
		var pids, rids []int64
		for _, it := range items {
			pids, rids = append(pids, it.p), append(rids, it.r)
		}
		if _, err := tx.Exec(ctx, `UPDATE products SET status='available' WHERE id = ANY($1) AND status='reserved'`, pids); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE requests SET auto_allocate=false WHERE id = ANY($1)`, rids); err != nil {
			return err
		}
		for _, r := range rids {
			if _, err := refreshRequestStatus(ctx, tx, r); err != nil {
				return err
			}
		}
		n = len(items)
		return a.Audit(ctx, tx, "reservation.expired", "reservation", nil, nil, map[string]any{"product_ids": pids, "request_ids": rids}, "")
	})
	return n, err
}

// ExpireProducts SKT'si geçen ürünleri expired yapar (rezervasyonları önce kapatılır).
func (a *App) ExpireProducts(ctx context.Context) (int, error) {
	n := 0
	err := a.InTx(ctx, "son kullanma tarihi geçti", func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT id FROM products WHERE status IN ('pending_test','available','reserved')
			AND expires_at < now() ORDER BY id LIMIT 5000 FOR UPDATE SKIP LOCKED`)
		if err != nil {
			return err
		}
		ids, err := pgx.CollectRows(rows, pgx.RowTo[int64])
		if err != nil || len(ids) == 0 {
			return err
		}
		rows, err = tx.Query(ctx, `UPDATE reservations SET status='released', closed_at=now()
			WHERE product_id = ANY($1) AND status='active' RETURNING request_id`, ids)
		if err != nil {
			return err
		}
		rids, err := pgx.CollectRows(rows, pgx.RowTo[int64])
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE products SET status='expired' WHERE id = ANY($1)`, ids); err != nil {
			return err
		}
		for _, r := range rids {
			if _, err := refreshRequestStatus(ctx, tx, r); err != nil {
				return err
			}
		}
		n = len(ids)
		return nil
	})
	return n, err
}

// ProcessQueue açık talepleri aciliyet ve oluşturulma sırasına göre tahsis eder.
func (a *App) ProcessQueue(ctx context.Context) (int, error) {
	rows, err := a.DB.Query(ctx, `SELECT id FROM requests WHERE status IN ('open','partial') AND auto_allocate
		ORDER BY urgency DESC, created_at, id LIMIT 100`)
	if err != nil {
		return 0, err
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[int64])
	if err != nil {
		return 0, err
	}
	ttl, err := a.defaultTTL(ctx)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, id := range ids {
		r, err := a.Allocate(ctx, id, ttl)
		if err != nil {
			a.Log.Warn("kuyruk tahsisi başarısız", "request_id", id, "err", err.Error())
			continue
		}
		n += len(r.Reserved) + len(r.Preempted)
	}
	return n, nil
}

// IssueRequest talebin aktif rezervasyonlarını dağıtır (ürün → issued, dağıtım kaydı).
func (a *App) IssueRequest(ctx context.Context, requestID int64) (Request, error) {
	err := a.InTx(ctx, "dağıtım", func(tx pgx.Tx) error {
		var hospitalID int64
		var status string
		if err := tx.QueryRow(ctx, `SELECT hospital_id, status FROM requests WHERE id=$1 FOR UPDATE`, requestID).Scan(&hospitalID, &status); err != nil {
			if err == pgx.ErrNoRows {
				return httpx.NotFound("talep")
			}
			return err
		}
		rows, err := tx.Query(ctx, `SELECT id, product_id FROM reservations WHERE request_id=$1 AND status='active'
			AND expires_at > now() FOR UPDATE`, requestID)
		if err != nil {
			return err
		}
		type rp struct{ r, p int64 }
		items, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (rp, error) {
			var x rp
			err := r.Scan(&x.r, &x.p)
			return x, err
		})
		if err != nil {
			return err
		}
		if len(items) == 0 {
			return httpx.Conflict("dağıtılacak aktif rezervasyon yok")
		}
		var pids, rids []int64
		for _, it := range items {
			pids, rids = append(pids, it.p), append(rids, it.r)
		}
		if _, err := tx.Exec(ctx, `UPDATE products SET status='issued' WHERE id = ANY($1)`, pids); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE reservations SET status='issued', closed_at=now() WHERE id = ANY($1)`, rids); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO distributions (request_id, product_id, hospital_id, issued_by)
			SELECT $1, unnest($2::bigint[]), $3, $4`, requestID, pids, hospitalID, actor(ctx)); err != nil {
			return err
		}
		if _, err := refreshRequestStatus(ctx, tx, requestID); err != nil {
			return err
		}
		return a.Audit(ctx, tx, "request.issue", "request", requestID, nil, map[string]any{"product_ids": pids}, "")
	})
	if err != nil {
		return Request{}, err
	}
	return a.GetRequest(ctx, requestID, 0)
}

// ReleaseReservation sorumlunun rezervasyonu elle bırakması.
func (a *App) ReleaseReservation(ctx context.Context, id int64) error {
	return a.InTx(ctx, "rezervasyon elle bırakıldı", func(tx pgx.Tx) error {
		var pid, rid int64
		err := tx.QueryRow(ctx, `UPDATE reservations SET status='released', closed_at=now() WHERE id=$1 AND status='active'
			RETURNING product_id, request_id`, id).Scan(&pid, &rid)
		if err == pgx.ErrNoRows {
			return httpx.NotFound("aktif rezervasyon")
		}
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE products SET status='available' WHERE id=$1 AND status='reserved'`, pid); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE requests SET auto_allocate=false WHERE id=$1`, rid); err != nil {
			return err
		}
		if _, err := refreshRequestStatus(ctx, tx, rid); err != nil {
			return err
		}
		return a.Audit(ctx, tx, "reservation.release", "reservation", id, nil, nil, "")
	})
}

// CancelRequest talebi iptal eder; aktif rezervasyonlar serbest kalır.
func (a *App) CancelRequest(ctx context.Context, id, hospitalID int64) error {
	return a.InTx(ctx, "talep iptal", func(tx pgx.Tx) error {
		var hid int64
		var status string
		if err := tx.QueryRow(ctx, `SELECT hospital_id, status FROM requests WHERE id=$1 FOR UPDATE`, id).Scan(&hid, &status); err != nil {
			if err == pgx.ErrNoRows {
				return httpx.NotFound("talep")
			}
			return err
		}
		if hospitalID > 0 && hid != hospitalID {
			return httpx.NotFound("talep")
		}
		if status == "issued" || status == "cancelled" {
			return httpx.Conflict("talep " + status + " durumunda")
		}
		rows, err := tx.Query(ctx, `UPDATE reservations SET status='released', closed_at=now() WHERE request_id=$1 AND status='active' RETURNING product_id`, id)
		if err != nil {
			return err
		}
		pids, err := pgx.CollectRows(rows, pgx.RowTo[int64])
		if err != nil {
			return err
		}
		if len(pids) > 0 {
			if _, err := tx.Exec(ctx, `UPDATE products SET status='available' WHERE id = ANY($1) AND status='reserved'`, pids); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, `UPDATE requests SET status='cancelled' WHERE id=$1`, id); err != nil {
			return err
		}
		return a.Audit(ctx, tx, "request.cancel", "request", id, nil, nil, "")
	})
}

const requestCols = `q.id, q.hospital_id, h.name, q.component, q.abo, q.rh_positive, q.units, q.urgency, q.patient_ref, q.needed_by, q.status, q.created_at`

func scanRequest(r pgx.Row) (Request, error) {
	var q Request
	err := r.Scan(&q.ID, &q.HospitalID, &q.HospitalName, &q.Component, &q.ABO, &q.RhPositive, &q.Units, &q.Urgency, &q.PatientRef, &q.NeededBy, &q.Status, &q.CreatedAt)
	q.Group = compat.Group{ABO: q.ABO, RhPositive: q.RhPositive}.String()
	q.Reservations = []Reservation{}
	return q, err
}

// GetRequest talep ve rezervasyonları. hospitalID > 0 ise yalnızca o hastanenin talebi.
func (a *App) GetRequest(ctx context.Context, id, hospitalID int64) (Request, error) {
	q, err := scanRequest(a.DB.QueryRow(ctx, `SELECT `+requestCols+` FROM requests q JOIN hospitals h ON h.id=q.hospital_id
		WHERE q.id=$1 AND ($2 = 0 OR q.hospital_id=$2)`, id, hospitalID))
	if err == pgx.ErrNoRows {
		return q, httpx.NotFound("talep")
	}
	if err != nil {
		return q, err
	}
	m, err := a.reservationsFor(ctx, []int64{id}, q.ABO, q.RhPositive)
	if err != nil {
		return q, err
	}
	q.Reservations = m[id]
	if q.Reservations == nil {
		q.Reservations = []Reservation{}
	}
	return q, nil
}

func (a *App) reservationsFor(ctx context.Context, ids []int64, _ string, _ bool) (map[int64][]Reservation, error) {
	// hospital_products görünümü bağış/bağışçı sütunu içermez.
	rows, err := a.DB.Query(ctx, `SELECT r.request_id, r.id, hp.id, hp.product_no, hp.component, hp.abo, hp.rh_positive, hp.expires_at,
			r.status, r.expires_at, r.closed_at, (hp.abo = q.abo AND hp.rh_positive = q.rh_positive)
		FROM reservations r JOIN hospital_products hp ON hp.id=r.product_id JOIN requests q ON q.id=r.request_id
		WHERE r.request_id = ANY($1) ORDER BY r.id`, ids)
	if err != nil {
		return nil, err
	}
	out := map[int64][]Reservation{}
	for rows.Next() {
		var reqID int64
		var r Reservation
		if err := rows.Scan(&reqID, &r.ID, &r.ProductID, &r.ProductNo, &r.Component, &r.ABO, &r.RhPositive, &r.ExpiresAt,
			&r.Status, &r.HoldUntil, &r.ClosedAt, &r.Exact); err != nil {
			return nil, err
		}
		r.Group = compat.Group{ABO: r.ABO, RhPositive: r.RhPositive}.String()
		out[reqID] = append(out[reqID], r)
	}
	return out, rows.Err()
}

// ListRequests talepler (keyset). hospitalID > 0 ise yalnızca o hastane.
func (a *App) ListRequests(ctx context.Context, hospitalID int64, status string, p httpx.Page) (httpx.List[Request], error) {
	args := []any{p.Limit + 1, hospitalID}
	where := []string{"($2 = 0 OR q.hospital_id = $2)"}
	if p.Cursor > 0 {
		args = append(args, p.Cursor)
		where = append(where, fmt.Sprintf("q.id < $%d", len(args)))
	}
	if status != "" {
		args = append(args, strings.Split(status, ","))
		where = append(where, fmt.Sprintf("q.status = ANY($%d)", len(args)))
	}
	rows, err := a.DB.Query(ctx, `SELECT `+requestCols+` FROM requests q JOIN hospitals h ON h.id=q.hospital_id
		WHERE `+strings.Join(where, " AND ")+` ORDER BY q.id DESC LIMIT $1`, args...)
	if err != nil {
		return httpx.List[Request]{}, err
	}
	items, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Request, error) { return scanRequest(r) })
	if err != nil {
		return httpx.List[Request]{}, err
	}
	page := pageOf(items, p.Limit, func(q Request) int64 { return q.ID })
	ids := make([]int64, len(page.Items))
	for i, q := range page.Items {
		ids[i] = q.ID
	}
	m, err := a.reservationsFor(ctx, ids, "", false)
	if err != nil {
		return page, err
	}
	for i := range page.Items {
		if rs := m[page.Items[i].ID]; rs != nil {
			page.Items[i].Reservations = rs
		}
	}
	return page, nil
}

// ActiveReservations tüm aktif rezervasyonlar (yarış durumu doğrulaması için).
func (a *App) ActiveReservations(ctx context.Context) ([]map[string]any, error) {
	rows, err := a.DB.Query(ctx, `SELECT r.id, r.request_id, r.product_id, r.expires_at FROM reservations r WHERE r.status='active' ORDER BY r.id`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (map[string]any, error) {
		var id, req, prod int64
		var exp time.Time
		err := r.Scan(&id, &req, &prod, &exp)
		return map[string]any{"id": id, "request_id": req, "product_id": prod, "expires_at": exp}, err
	})
}

// GetReservation tek rezervasyon durumu.
func (a *App) GetReservation(ctx context.Context, id int64) (Reservation, error) {
	var r Reservation
	err := a.DB.QueryRow(ctx, `SELECT r.id, hp.id, hp.product_no, hp.component, hp.abo, hp.rh_positive, hp.expires_at, r.status, r.expires_at, r.closed_at
		FROM reservations r JOIN hospital_products hp ON hp.id=r.product_id WHERE r.id=$1`, id).
		Scan(&r.ID, &r.ProductID, &r.ProductNo, &r.Component, &r.ABO, &r.RhPositive, &r.ExpiresAt, &r.Status, &r.HoldUntil, &r.ClosedAt)
	if err == pgx.ErrNoRows {
		return r, httpx.NotFound("rezervasyon")
	}
	r.Group = compat.Group{ABO: r.ABO, RhPositive: r.RhPositive}.String()
	return r, err
}

// RecordTransfusion hastanenin kendisine dağıtılmış ürünün transfüzyonunu kaydeder.
func (a *App) RecordTransfusion(ctx context.Context, hospitalID int64, productNo, patientRef string) error {
	patientRef = strings.TrimSpace(patientRef)
	if len(patientRef) < 3 {
		return httpx.Validation("patient_ref en az 3 karakter olmalı")
	}
	return a.InTx(ctx, "transfüzyon", func(tx pgx.Tx) error {
		var pid int64
		var status string
		err := tx.QueryRow(ctx, `SELECT p.id, p.status FROM products p JOIN distributions d ON d.product_id=p.id
			WHERE p.product_no=$1 AND d.hospital_id=$2 FOR UPDATE OF p`, productNo, hospitalID).Scan(&pid, &status)
		if err == pgx.ErrNoRows {
			return httpx.NotFound("hastanenize dağıtılmış ürün")
		}
		if err != nil {
			return err
		}
		if status != "issued" {
			return httpx.E(409, "invalid_transition", "ürün "+status+" durumunda; transfüzyon kaydedilemez")
		}
		if _, err := tx.Exec(ctx, `UPDATE products SET status='transfused' WHERE id=$1`, pid); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO transfusions (product_id, hospital_id, patient_ref, recorded_by) VALUES ($1,$2,$3,$4)`,
			pid, hospitalID, patientRef, actor(ctx)); err != nil {
			return err
		}
		return a.Audit(ctx, tx, "product.transfused", "product", pid, nil, map[string]any{"patient_ref": patientRef}, "")
	})
}
