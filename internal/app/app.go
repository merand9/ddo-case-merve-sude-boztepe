// Package app iş akışlarını (transaction, kilit, denetim kaydı) uygular. HTTP katmanından
// bağımsızdır; handler'lar yalnızca girdi çözer ve bu paketi çağırır.
package app

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/msboztepe/damarbagis/internal/auth"
	"github.com/msboztepe/damarbagis/internal/config"
	"github.com/msboztepe/damarbagis/internal/domain/eligibility"
	"github.com/msboztepe/damarbagis/internal/domain/product"
	"github.com/msboztepe/damarbagis/internal/httpx"
	"github.com/msboztepe/damarbagis/internal/secure"
)

// App servis katmanı.
type App struct {
	DB     *pgxpool.Pool
	Sealer *secure.Sealer
	Issuer *auth.Issuer
	Cfg    config.Config
	Log    *slog.Logger
	Now    func() time.Time // testlerde sabitlenebilir
}

// New servis katmanını kurar.
func New(db *pgxpool.Pool, cfg config.Config, log *slog.Logger) (*App, error) {
	s, err := secure.NewSealer(cfg.HealthKey)
	if err != nil {
		return nil, err
	}
	return &App{
		DB: db, Sealer: s, Cfg: cfg, Log: log,
		Issuer: auth.NewIssuer(cfg.JWTSecret, cfg.AccessTTL),
		Now:    func() time.Time { return time.Now().UTC() },
	}, nil
}

// InTx fn'i bir transaction içinde çalıştırır. Aktör ve gerekçe, veritabanı tetikleyicilerinin
// (ürün olayları) okuyabilmesi için transaction'a özel ayarlara yazılır.
func (a *App) InTx(ctx context.Context, reason string, fn func(tx pgx.Tx) error) error {
	return pgx.BeginFunc(ctx, a.DB, func(tx pgx.Tx) error {
		actor := ""
		if c := httpx.ClaimsFrom(ctx); c != nil {
			actor = strconv.FormatInt(c.UserID, 10)
		}
		if _, err := tx.Exec(ctx, `SELECT set_config('app.actor_id', $1, true), set_config('app.reason', $2, true)`,
			actor, reason); err != nil {
			return err
		}
		return fn(tx)
	})
}

// Audit denetim kaydı yazar. old/new JSON'a çevrilir; nil olabilir.
func (a *App) Audit(ctx context.Context, q pgx.Tx, action, entity string, entityID any, old, new any, reason string) error {
	var actorID *int64
	var role *string
	if c := httpx.ClaimsFrom(ctx); c != nil {
		actorID, role = &c.UserID, &c.Role
	}
	oj, err := jsonOrNil(old)
	if err != nil {
		return err
	}
	nj, err := jsonOrNil(new)
	if err != nil {
		return err
	}
	var eid *string
	if entityID != nil {
		s := fmt.Sprint(entityID)
		eid = &s
	}
	var rsn *string
	if reason != "" {
		rsn = &reason
	}
	_, err = q.Exec(ctx, `INSERT INTO audit_log (actor_id, actor_role, action, entity, entity_id, old_value, new_value, reason, request_id)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`, actorID, role, action, entity, eid, oj, nj, rsn, httpx.RequestID(ctx))
	return err
}

func jsonOrNil(v any) ([]byte, error) {
	if v == nil {
		return nil, nil
	}
	return json.Marshal(v)
}

// ---- kurallar ----

type ruleRow struct {
	ValidFrom time.Time
	ValidTo   *time.Time
	Params    []byte
}

func loadRules(ctx context.Context, q pgx.Tx, db *pgxpool.Pool, code string) ([]ruleRow, error) {
	const sql = `SELECT valid_from, valid_to, params FROM rule_versions WHERE code=$1 ORDER BY valid_from`
	var rows pgx.Rows
	var err error
	if q != nil {
		rows, err = q.Query(ctx, sql, code)
	} else {
		rows, err = db.Query(ctx, sql, code)
	}
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (ruleRow, error) {
		var rr ruleRow
		err := r.Scan(&rr.ValidFrom, &rr.ValidTo, &rr.Params)
		return rr, err
	})
}

// RuleBook uygunluk kural sürümlerini yükler.
func (a *App) RuleBook(ctx context.Context, tx pgx.Tx) (eligibility.RuleBook, error) {
	rows, err := loadRules(ctx, tx, a.DB, "eligibility")
	if err != nil {
		return nil, err
	}
	book := make(eligibility.RuleBook, 0, len(rows))
	for _, r := range rows {
		var rules eligibility.Rules
		if err := json.Unmarshal(r.Params, &rules); err != nil {
			return nil, fmt.Errorf("kural sürümü %s çözümlenemedi: %w", r.ValidFrom.Format("2006-01-02"), err)
		}
		book = append(book, eligibility.Version{ValidFrom: r.ValidFrom, ValidTo: r.ValidTo, Rules: rules})
	}
	return book, nil
}

// ShelfParams raf ömrü kural sürümü.
type ShelfParams struct {
	Days                  product.Shelf `json:"days"`
	LookbackDays          int           `json:"lookback_days"`
	ReservationTTLMinutes int           `json:"reservation_ttl_minutes"`
}

// ShelfAt verilen anda geçerli raf ömrü kurallarını döner.
func (a *App) ShelfAt(ctx context.Context, tx pgx.Tx, t time.Time) (ShelfParams, error) {
	rows, err := loadRules(ctx, tx, a.DB, "shelf_life")
	if err != nil {
		return ShelfParams{}, err
	}
	day := t.UTC().Truncate(24 * time.Hour)
	for _, r := range rows {
		if !day.Before(r.ValidFrom) && (r.ValidTo == nil || day.Before(*r.ValidTo)) {
			var p ShelfParams
			if err := json.Unmarshal(r.Params, &p); err != nil {
				return p, err
			}
			return p, nil
		}
	}
	return ShelfParams{}, fmt.Errorf("raf ömrü kuralı bulunamadı")
}

// actor bağlamdaki kullanıcı kimliğini döner (yoksa nil).
func actor(ctx context.Context) *int64 {
	if c := httpx.ClaimsFrom(ctx); c != nil {
		return &c.UserID
	}
	return nil
}

func day(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}
