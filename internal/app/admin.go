package app

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/msboztepe/damarbagis/internal/domain/eligibility"
	"github.com/msboztepe/damarbagis/internal/httpx"
)

// RuleVersion kural sürümü.
type RuleVersion struct {
	ID        int64           `json:"id"`
	Code      string          `json:"code"`
	ValidFrom string          `json:"valid_from"`
	ValidTo   *string         `json:"valid_to"`
	Note      *string         `json:"note,omitempty"`
	Params    json.RawMessage `json:"params"`
	CreatedAt time.Time       `json:"created_at"`
}

// ListRules tüm kural sürümleri.
func (a *App) ListRules(ctx context.Context, code string) ([]RuleVersion, error) {
	rows, err := a.DB.Query(ctx, `SELECT id, code, valid_from, valid_to, note, params, created_at FROM rule_versions
		WHERE ($1='' OR code=$1) ORDER BY code, valid_from`, code)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (RuleVersion, error) {
		var v RuleVersion
		var f time.Time
		var t *time.Time
		err := r.Scan(&v.ID, &v.Code, &f, &t, &v.Note, &v.Params, &v.CreatedAt)
		v.ValidFrom = f.Format("2006-01-02")
		if t != nil {
			s := t.Format("2006-01-02")
			v.ValidTo = &s
		}
		return v, err
	})
}

// AddRuleVersion yeni bir kural sürümünü validFrom gününden itibaren devreye alır. O günü kapsayan
// mevcut sürüm validFrom'da kapatılır, yeni sürüm onun eski bitişine kadar geçerli olur.
// Geçmiş tarihli sürüm eklenemez (geçmiş kararların dayanağı değişmesin diye).
func (a *App) AddRuleVersion(ctx context.Context, code, validFrom, note string, params json.RawMessage) (RuleVersion, error) {
	from, err := time.Parse("2006-01-02", validFrom)
	if err != nil {
		return RuleVersion{}, httpx.Validation("valid_from YYYY-AA-GG olmalı")
	}
	if !from.After(day(a.Now())) {
		return RuleVersion{}, httpx.Validation("yeni kural sürümü yalnızca ileri bir tarihten itibaren geçerli olabilir")
	}
	switch code {
	case "eligibility":
		var r eligibility.Rules
		dec := json.NewDecoder(strings.NewReader(string(params)))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&r); err != nil {
			return RuleVersion{}, httpx.Validation("kural parametreleri çözümlenemedi: " + err.Error())
		}
		if r.MinAge < 16 || r.MaxAge <= r.MinAge || r.MinWeightKg <= 0 || len(r.HbMin) == 0 || len(r.IntervalsDays) == 0 {
			return RuleVersion{}, httpx.Validation("kural parametreleri eksik ya da tutarsız")
		}
	case "shelf_life":
		var s ShelfParams
		if err := json.Unmarshal(params, &s); err != nil || len(s.Days) == 0 || s.ReservationTTLMinutes <= 0 {
			return RuleVersion{}, httpx.Validation("raf ömrü parametreleri eksik")
		}
	default:
		return RuleVersion{}, httpx.Validation("code eligibility ya da shelf_life olmalı")
	}
	var id int64
	err = a.InTx(ctx, "", func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `LOCK TABLE rule_versions IN SHARE ROW EXCLUSIVE MODE`); err != nil {
			return err
		}
		var curID int64
		var curFrom time.Time
		var curTo *time.Time
		err := tx.QueryRow(ctx, `SELECT id, valid_from, valid_to FROM rule_versions
			WHERE code=$1 AND valid_from <= $2 AND (valid_to IS NULL OR valid_to > $2)`, code, from).Scan(&curID, &curFrom, &curTo)
		if err != nil && err != pgx.ErrNoRows {
			return err
		}
		var newTo *time.Time
		if err == nil {
			if curFrom.Equal(from) {
				return httpx.Conflict("bu tarihte başlayan bir sürüm zaten var")
			}
			newTo = curTo
			if _, err := tx.Exec(ctx, `UPDATE rule_versions SET valid_to=$2 WHERE id=$1`, curID, from); err != nil {
				return err
			}
		}
		if err := tx.QueryRow(ctx, `INSERT INTO rule_versions (code, params, valid_from, valid_to, note, created_by)
			VALUES ($1,$2,$3,$4,nullif($5,''),$6) RETURNING id`, code, []byte(params), from, newTo, note, actor(ctx)).Scan(&id); err != nil {
			return err
		}
		return a.Audit(ctx, tx, "rule.version.create", "rule_version", id, map[string]any{"closed_version": curID}, map[string]any{"code": code, "valid_from": validFrom}, note)
	})
	if err != nil {
		return RuleVersion{}, err
	}
	vs, err := a.ListRules(ctx, code)
	if err != nil {
		return RuleVersion{}, err
	}
	for _, v := range vs {
		if v.ID == id {
			return v, nil
		}
	}
	return RuleVersion{}, fmt.Errorf("sürüm okunamadı")
}

// AuditEntry denetim kaydı.
type AuditEntry struct {
	ID        int64           `json:"id"`
	At        time.Time       `json:"at"`
	ActorID   *int64          `json:"actor_id"`
	ActorRole *string         `json:"actor_role"`
	Action    string          `json:"action"`
	Entity    string          `json:"entity"`
	EntityID  *string         `json:"entity_id"`
	Old       json.RawMessage `json:"old_value,omitempty"`
	New       json.RawMessage `json:"new_value,omitempty"`
	Reason    *string         `json:"reason,omitempty"`
	RequestID *string         `json:"request_id,omitempty"`
}

// ListAudit denetim kayıtları (keyset, action/entity filtreli).
func (a *App) ListAudit(ctx context.Context, action, entity, entityID string, p httpx.Page) (httpx.List[AuditEntry], error) {
	args := []any{p.Limit + 1}
	where := []string{"true"}
	if p.Cursor > 0 {
		args = append(args, p.Cursor)
		where = append(where, fmt.Sprintf("id < $%d", len(args)))
	}
	if action != "" {
		args = append(args, action)
		where = append(where, fmt.Sprintf("action=$%d", len(args)))
	}
	if entity != "" {
		args = append(args, entity)
		where = append(where, fmt.Sprintf("entity=$%d", len(args)))
	}
	if entityID != "" {
		args = append(args, entityID)
		where = append(where, fmt.Sprintf("entity_id=$%d", len(args)))
	}
	rows, err := a.DB.Query(ctx, `SELECT id, at, actor_id, actor_role, action, entity, entity_id, old_value, new_value, reason, request_id
		FROM audit_log WHERE `+strings.Join(where, " AND ")+` ORDER BY id DESC LIMIT $1`, args...)
	if err != nil {
		return httpx.List[AuditEntry]{}, err
	}
	items, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (AuditEntry, error) {
		var e AuditEntry
		err := r.Scan(&e.ID, &e.At, &e.ActorID, &e.ActorRole, &e.Action, &e.Entity, &e.EntityID, &e.Old, &e.New, &e.Reason, &e.RequestID)
		return e, err
	})
	if err != nil {
		return httpx.List[AuditEntry]{}, err
	}
	return pageOf(items, p.Limit, func(e AuditEntry) int64 { return e.ID }), nil
}

// Report dönemsel özet.
type Report struct {
	From             time.Time        `json:"from"`
	To               time.Time        `json:"to"`
	Donations        map[string]int   `json:"donations"`
	TestsPositive    int              `json:"tests_positive"`
	TestsTotal       int              `json:"tests_total"`
	Produced         map[string]int   `json:"produced"`
	Issued           map[string]int   `json:"issued"`
	Expired          map[string]int   `json:"expired"`
	Discarded        int              `json:"discarded"`
	Requests         map[string]int   `json:"requests_by_status"`
	EmergencyFillMin *float64         `json:"emergency_median_fill_minutes"`
	IneligibleTries  int              `json:"ineligible_attempts"`
	IssuedByHospital []map[string]any `json:"issued_by_hospital"`
}

// Summary [from, to) dönem raporu.
func (a *App) Summary(ctx context.Context, from, to time.Time) (Report, error) {
	r := Report{From: from, To: to, Donations: map[string]int{}, Produced: map[string]int{}, Issued: map[string]int{},
		Expired: map[string]int{}, Requests: map[string]int{}, IssuedByHospital: []map[string]any{}}
	count := func(sql string, dst map[string]int) error {
		rows, err := a.DB.Query(ctx, sql, from, to)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var k string
			var n int
			if err := rows.Scan(&k, &n); err != nil {
				return err
			}
			dst[k] = n
		}
		return rows.Err()
	}
	steps := []struct {
		sql string
		dst map[string]int
	}{
		{`SELECT donation_type::text, count(*) FROM donations WHERE collected_at >= $1 AND collected_at < $2 GROUP BY 1`, r.Donations},
		{`SELECT component::text, count(*) FROM products WHERE parent_id IS NULL AND produced_at >= $1 AND produced_at < $2 GROUP BY 1`, r.Produced},
		{`SELECT p.component::text, count(*) FROM distributions d JOIN products p ON p.id=d.product_id WHERE d.issued_at >= $1 AND d.issued_at < $2 GROUP BY 1`, r.Issued},
		{`SELECT p.component::text, count(*) FROM product_events e JOIN products p ON p.id=e.product_id WHERE e.to_status='expired' AND e.at >= $1 AND e.at < $2 GROUP BY 1`, r.Expired},
		{`SELECT status, count(*) FROM requests WHERE created_at >= $1 AND created_at < $2 GROUP BY 1`, r.Requests},
	}
	for _, s := range steps {
		if err := count(s.sql, s.dst); err != nil {
			return r, err
		}
	}
	err := a.DB.QueryRow(ctx, `SELECT count(*) FILTER (WHERE outcome='positive'), count(*) FROM test_results WHERE entered_at >= $1 AND entered_at < $2`, from, to).
		Scan(&r.TestsPositive, &r.TestsTotal)
	if err != nil {
		return r, err
	}
	if err := a.DB.QueryRow(ctx, `SELECT count(*) FROM product_events WHERE to_status='discarded' AND at >= $1 AND at < $2`, from, to).Scan(&r.Discarded); err != nil {
		return r, err
	}
	if err := a.DB.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE action='eligibility.rejected' AND at >= $1 AND at < $2`, from, to).Scan(&r.IneligibleTries); err != nil {
		return r, err
	}
	if err := a.DB.QueryRow(ctx, `SELECT percentile_cont(0.5) WITHIN GROUP (ORDER BY extract(epoch FROM (r.reserved_at - q.created_at))/60)
		FROM requests q JOIN reservations r ON r.request_id=q.id WHERE q.urgency='emergency' AND q.created_at >= $1 AND q.created_at < $2`, from, to).
		Scan(&r.EmergencyFillMin); err != nil {
		return r, err
	}
	rows, err := a.DB.Query(ctx, `SELECT h.name, count(*) FROM distributions d JOIN hospitals h ON h.id=d.hospital_id
		WHERE d.issued_at >= $1 AND d.issued_at < $2 GROUP BY 1 ORDER BY 2 DESC`, from, to)
	if err != nil {
		return r, err
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		var n int
		if err := rows.Scan(&name, &n); err != nil {
			return r, err
		}
		r.IssuedByHospital = append(r.IssuedByHospital, map[string]any{"hospital": name, "units": n})
	}
	return r, rows.Err()
}

// Live panodaki "sistem canlı" akışı: son ürün olayları, son kritik işlemler, son 5 dakikanın sayaçları.
func (a *App) Live(ctx context.Context) (map[string]any, error) {
	rows, err := a.DB.Query(ctx, `SELECT e.at, p.product_no, p.component, p.abo, p.rh_positive, e.from_status, e.to_status
		FROM product_events e JOIN products p ON p.id=e.product_id ORDER BY e.id DESC LIMIT 15`)
	if err != nil {
		return nil, err
	}
	events, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (map[string]any, error) {
		var at time.Time
		var no, comp, abo string
		var rh bool
		var from *string
		var to string
		err := r.Scan(&at, &no, &comp, &abo, &rh, &from, &to)
		g := abo + "-"
		if rh {
			g = abo + "+"
		}
		return map[string]any{"at": at, "product_no": no, "component": comp, "group": g, "from": from, "to": to}, err
	})
	if err != nil {
		return nil, err
	}
	rows, err = a.DB.Query(ctx, `SELECT at, action, entity, entity_id FROM audit_log
		WHERE action IN ('donation.create','eligibility.rejected','test_result.create','request.create','request.issue','reservation.preempted','stock.alert','product.transfused')
		ORDER BY id DESC LIMIT 15`)
	if err != nil {
		return nil, err
	}
	actions, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (map[string]any, error) {
		var at time.Time
		var action, entity string
		var eid *string
		err := r.Scan(&at, &action, &entity, &eid)
		return map[string]any{"at": at, "action": action, "entity": entity, "entity_id": eid}, err
	})
	if err != nil {
		return nil, err
	}
	var donations5, rejections5, requests5, issued5 int
	err = a.DB.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM donations WHERE created_at > now() - interval '5 minutes'),
		(SELECT count(*) FROM audit_log WHERE action='eligibility.rejected' AND at > now() - interval '5 minutes'),
		(SELECT count(*) FROM requests WHERE created_at > now() - interval '5 minutes'),
		(SELECT count(*) FROM distributions WHERE issued_at > now() - interval '5 minutes')`).
		Scan(&donations5, &rejections5, &requests5, &issued5)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"server_time":    a.Now(),
		"last_5_minutes": map[string]int{"donations": donations5, "ineligible_attempts": rejections5, "requests": requests5, "issued": issued5},
		"product_events": events,
		"actions":        actions,
	}, nil
}

// PruneIdempotency 24 saatten eski idempotans anahtarlarını siler.
func (a *App) PruneIdempotency(ctx context.Context) error {
	_, err := a.DB.Exec(ctx, `DELETE FROM idempotency_keys WHERE created_at < now() - interval '24 hours'`)
	return err
}
