package app

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/msboztepe/damarbagis/internal/domain/compat"
	"github.com/msboztepe/damarbagis/internal/domain/eligibility"
	"github.com/msboztepe/damarbagis/internal/domain/forecast"
	"github.com/msboztepe/damarbagis/internal/httpx"
)

const historyDays = 84 // 12 hafta

// consumption bileşen için grup başına günlük dağıtım serisini döner (dün dahil, bugün hariç).
func (a *App) consumption(ctx context.Context, component string, days int) (map[compat.Group][]float64, time.Time, error) {
	today := day(a.Now())
	start := today.AddDate(0, 0, -days)
	lastDay := today.AddDate(0, 0, -1)
	rows, err := a.DB.Query(ctx, `
		SELECT (d.issued_at AT TIME ZONE 'UTC')::date, p.abo, p.rh_positive, count(*)
		FROM distributions d JOIN products p ON p.id = d.product_id
		WHERE p.component = $1 AND d.issued_at >= $2 AND d.issued_at < $3
		GROUP BY 1, 2, 3`, component, start, today)
	if err != nil {
		return nil, lastDay, err
	}
	defer rows.Close()
	out := map[compat.Group][]float64{}
	for _, g := range compat.AllGroups() {
		out[g] = make([]float64, days)
	}
	for rows.Next() {
		var dt time.Time
		var g compat.Group
		var n int
		if err := rows.Scan(&dt, &g.ABO, &g.RhPositive, &n); err != nil {
			return nil, lastDay, err
		}
		i := int(dt.Sub(start).Hours() / 24)
		if i >= 0 && i < days {
			out[g][i] = float64(n)
		}
	}
	return out, lastDay, rows.Err()
}

// GroupForecast bir kan grubunun tahmini.
type GroupForecast struct {
	Group          string    `json:"group"`
	ABO            string    `json:"abo"`
	RhPositive     bool      `json:"rh_positive"`
	Available      int       `json:"available"`
	AvgDaily28     float64   `json:"avg_daily_28d"`
	Forecast       []float64 `json:"forecast"`
	DaysOfSupply   float64   `json:"days_of_supply"`
	ExpectedExpiry int       `json:"expected_expiry_loss"`
	Level          string    `json:"level"`
}

// ForecastReport tahmin raporu.
type ForecastReport struct {
	Component   string          `json:"component"`
	GeneratedAt time.Time       `json:"generated_at"`
	HorizonDays int             `json:"horizon_days"`
	Method      string          `json:"method"`
	Groups      []GroupForecast `json:"groups"`
}

// Forecast her kan grubu için önümüzdeki `horizon` günün tüketim tahminini, stok yeterliliğini
// (gün) ve FEFO varsayımıyla SKT kaynaklı beklenen kaybı hesaplar.
func (a *App) Forecast(ctx context.Context, component string, horizon int) (ForecastReport, error) {
	if !compat.Component(component).Valid() {
		return ForecastReport{}, httpx.BadRequest("geçersiz bileşen")
	}
	if horizon < 1 || horizon > 28 {
		return ForecastReport{}, httpx.BadRequest("horizon 1-28 gün olmalı")
	}
	series, lastDay, err := a.consumption(ctx, component, historyDays)
	if err != nil {
		return ForecastReport{}, err
	}
	rows, err := a.DB.Query(ctx, `SELECT abo, rh_positive, expires_at FROM products
		WHERE component=$1 AND status='available' AND expires_at > now()`, component)
	if err != nil {
		return ForecastReport{}, err
	}
	stock := map[compat.Group][]time.Time{}
	for rows.Next() {
		var g compat.Group
		var e time.Time
		if err := rows.Scan(&g.ABO, &g.RhPositive, &e); err != nil {
			rows.Close()
			return ForecastReport{}, err
		}
		stock[g] = append(stock[g], e)
	}
	rows.Close()
	m := forecast.Default
	rep := ForecastReport{Component: component, GeneratedAt: a.Now(), HorizonDays: horizon,
		Method: fmt.Sprintf("%d günlük hareketli ortalama × %d haftalık haftanın günü çarpanı; FEFO kayıp simülasyonu", m.Window, m.Weeks)}
	for _, g := range compat.AllGroups() {
		h := series[g]
		pred := m.Predict(h, lastDay, horizon)
		for i := range pred {
			pred[i] = math.Round(pred[i]*100) / 100
		}
		exp := stock[g]
		loss, _ := forecast.ExpiryLoss(exp, day(a.Now()), pred)
		dos := forecast.DaysOfSupply(len(exp), pred)
		rep.Groups = append(rep.Groups, GroupForecast{
			Group: g.String(), ABO: g.ABO, RhPositive: g.RhPositive, Available: len(exp),
			AvgDaily28: math.Round(avg(h[len(h)-28:])*100) / 100, Forecast: pred,
			DaysOfSupply: math.Round(dos*100) / 100, ExpectedExpiry: loss,
			Level: forecast.Level(dos, a.Cfg.AlertCritical, a.Cfg.AlertLow),
		})
	}
	return rep, nil
}

// BacktestReport geriye dönük test raporu.
type BacktestReport struct {
	Component string                             `json:"component"`
	Days      int                                `json:"days"`
	Total     forecast.BacktestResult            `json:"total"`
	Groups    map[string]forecast.BacktestResult `json:"groups"`
}

// Backtest son `days` gün için rolling-origin bir günlük tahmin hatasını (MAPE/WAPE) raporlar.
// "total" tüm grupların toplamı üzerinde, "groups" grup bazındadır.
func (a *App) Backtest(ctx context.Context, component string, days int) (BacktestReport, error) {
	if !compat.Component(component).Valid() {
		return BacktestReport{}, httpx.BadRequest("geçersiz bileşen")
	}
	if days < 7 || days > 56 {
		return BacktestReport{}, httpx.BadRequest("days 7-56 olmalı")
	}
	series, lastDay, err := a.consumption(ctx, component, historyDays)
	if err != nil {
		return BacktestReport{}, err
	}
	m := forecast.Default
	rep := BacktestReport{Component: component, Days: days, Groups: map[string]forecast.BacktestResult{}}
	total := make([]float64, historyDays)
	for _, g := range compat.AllGroups() {
		h := series[g]
		for i, v := range h {
			total[i] += v
		}
		r := m.Backtest(h, lastDay, days)
		r.Points = nil
		rep.Groups[g.String()] = r
	}
	rep.Total = m.Backtest(total, lastDay, days)
	return rep, nil
}

// Alert stok alarmı.
type Alert struct {
	ID             int64      `json:"id"`
	Component      string     `json:"component"`
	Group          string     `json:"group"`
	ABO            string     `json:"abo"`
	RhPositive     bool       `json:"rh_positive"`
	Level          string     `json:"level"`
	AvailableUnits int        `json:"available_units"`
	DaysOfSupply   float64    `json:"days_of_supply"`
	CreatedAt      time.Time  `json:"created_at"`
	ResolvedAt     *time.Time `json:"resolved_at,omitempty"`
}

// EvaluateAlerts RBC ve PLT için tahmine göre alarmları açar/günceller/kapatır.
func (a *App) EvaluateAlerts(ctx context.Context) error {
	for _, comp := range []string{"RBC", "PLT"} {
		rep, err := a.Forecast(ctx, comp, 7)
		if err != nil {
			return err
		}
		err = a.InTx(ctx, "", func(tx pgx.Tx) error {
			for _, g := range rep.Groups {
				var openID *int64
				var openLevel *string
				err := tx.QueryRow(ctx, `SELECT id, level FROM stock_alerts WHERE component=$1 AND abo=$2 AND rh_positive=$3 AND resolved_at IS NULL FOR UPDATE`,
					comp, g.ABO, g.RhPositive).Scan(&openID, &openLevel)
				if err != nil && err != pgx.ErrNoRows {
					return err
				}
				switch {
				case g.Level == "" && openID != nil:
					if _, err := tx.Exec(ctx, `UPDATE stock_alerts SET resolved_at=now() WHERE id=$1`, *openID); err != nil {
						return err
					}
				case g.Level != "" && openID == nil:
					if _, err := tx.Exec(ctx, `INSERT INTO stock_alerts (component, abo, rh_positive, level, available_units, days_of_supply)
						VALUES ($1,$2,$3,$4,$5,$6)`, comp, g.ABO, g.RhPositive, g.Level, g.Available, math.Min(g.DaysOfSupply, 999999)); err != nil {
						return err
					}
					if err := a.Audit(ctx, tx, "stock.alert", "stock", comp+":"+g.Group, nil, g, ""); err != nil {
						return err
					}
				case g.Level != "" && openID != nil:
					if _, err := tx.Exec(ctx, `UPDATE stock_alerts SET level=$2, available_units=$3, days_of_supply=$4 WHERE id=$1`,
						*openID, g.Level, g.Available, math.Min(g.DaysOfSupply, 999999)); err != nil {
						return err
					}
				}
			}
			return nil
		})
		if err != nil {
			return err
		}
	}
	return nil
}

// ListAlerts açık (ve isteğe bağlı kapanmış) alarmlar.
func (a *App) ListAlerts(ctx context.Context, includeResolved bool) ([]Alert, error) {
	rows, err := a.DB.Query(ctx, `SELECT id, component, abo, rh_positive, level, available_units, days_of_supply::float8, created_at, resolved_at
		FROM stock_alerts WHERE $1 OR resolved_at IS NULL ORDER BY (level='critical') DESC, created_at DESC LIMIT 100`, includeResolved)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Alert, error) {
		var al Alert
		err := r.Scan(&al.ID, &al.Component, &al.ABO, &al.RhPositive, &al.Level, &al.AvailableUnits, &al.DaysOfSupply, &al.CreatedAt, &al.ResolvedAt)
		al.Group = compat.Group{ABO: al.ABO, RhPositive: al.RhPositive}.String()
		return al, err
	})
}

// CallCandidate çağrı listesindeki bağışçı.
type CallCandidate struct {
	DonorID      int64      `json:"donor_id"`
	DonorNo      string     `json:"donor_no"`
	Name         string     `json:"name"`
	Group        string     `json:"group"`
	Sex          string     `json:"sex"`
	Phone        *string    `json:"phone,omitempty"`
	LastDonation *time.Time `json:"last_donation,omitempty"`
}

// CallList kritik gruptaki, bugün bağış yapabilecek bağışçıları döner. Ön eleme SQL'de
// (yaş, aktif erteleme, bekleme süresi) dizinli yapılır; sonra her aday domain kural motoruyla
// yeniden doğrulanır, böylece liste ile bağış kaydı aynı kurala göre karar verir.
// group boşsa açık alarmı olan tüm gruplar için liste üretilir.
func (a *App) CallList(ctx context.Context, group string, limit int) (map[string][]CallCandidate, error) {
	if limit < 1 || limit > 500 {
		return nil, httpx.BadRequest("limit 1-500 olmalı")
	}
	var groups []compat.Group
	if group != "" {
		g, err := compat.ParseGroup(group)
		if err != nil {
			return nil, httpx.BadRequest(err.Error())
		}
		groups = append(groups, g)
	} else {
		alerts, err := a.ListAlerts(ctx, false)
		if err != nil {
			return nil, err
		}
		seen := map[compat.Group]bool{}
		for _, al := range alerts {
			g := compat.Group{ABO: al.ABO, RhPositive: al.RhPositive}
			if !seen[g] {
				seen[g] = true
				groups = append(groups, g)
			}
		}
	}
	book, err := a.RuleBook(ctx, nil)
	if err != nil {
		return nil, err
	}
	now := a.Now()
	v, ok := book.At(now)
	if !ok {
		return nil, fmt.Errorf("geçerli kural yok")
	}
	r := v.Rules
	iv := r.IntervalsDays
	out := map[string][]CallCandidate{}
	for _, g := range groups {
		rows, err := a.DB.Query(ctx, `
			SELECT d.id, d.donor_no, d.first_name || ' ' || d.last_name, d.sex, d.birth_date, d.phone, x.last_wb, x.last_plt
			FROM donors d
			LEFT JOIN LATERAL (
				SELECT max(collected_at) FILTER (WHERE donation_type='whole_blood') AS last_wb,
				       max(collected_at) FILTER (WHERE donation_type='platelet_apheresis') AS last_plt
				FROM donations WHERE donor_id = d.id
			) x ON true
			WHERE d.abo = $1 AND d.rh_positive = $2
			  AND d.birth_date <= $3::date - make_interval(years => $4)
			  AND d.birth_date >  $3::date - make_interval(years => $5 + 1)
			  AND NOT EXISTS (SELECT 1 FROM deferrals f WHERE f.donor_id = d.id
			                  AND f.starts_on <= $3 AND (f.ends_on IS NULL OR f.ends_on > $3))
			  AND (x.last_wb IS NULL OR x.last_wb < $3::date - CASE WHEN d.sex='M' THEN $6::int ELSE $7::int END + 1)
			  AND (x.last_plt IS NULL OR x.last_plt < $3::date - $8::int + 1)
			ORDER BY coalesce(x.last_wb, x.last_plt) ASC NULLS LAST, d.id
			LIMIT $9`, g.ABO, g.RhPositive, day(now), r.MinAge, r.MaxAge,
			iv[eligibility.WholeBlood][eligibility.WholeBlood][eligibility.Male],
			iv[eligibility.WholeBlood][eligibility.WholeBlood][eligibility.Female],
			iv[eligibility.Platelet][eligibility.WholeBlood][eligibility.Male], limit*2)
		if err != nil {
			return nil, err
		}
		type cand struct {
			c     CallCandidate
			birth time.Time
			wb    *time.Time
			plt   *time.Time
		}
		cands, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (cand, error) {
			var c cand
			err := row.Scan(&c.c.DonorID, &c.c.DonorNo, &c.c.Name, &c.c.Sex, &c.birth, &c.c.Phone, &c.wb, &c.plt)
			c.c.Group = g.String()
			c.c.LastDonation = latest(c.wb, c.plt)
			return c, err
		})
		if err != nil {
			return nil, err
		}
		list := []CallCandidate{}
		for _, c := range cands {
			if len(list) >= limit {
				break
			}
			var hist []eligibility.PriorDonation
			if c.wb != nil {
				hist = append(hist, eligibility.PriorDonation{Type: eligibility.WholeBlood, At: *c.wb})
			}
			if c.plt != nil {
				hist = append(hist, eligibility.PriorDonation{Type: eligibility.Platelet, At: *c.plt})
			}
			dec := eligibility.Evaluate(eligibility.Input{Sex: eligibility.Sex(c.c.Sex), BirthDate: c.birth, Type: eligibility.WholeBlood,
				At: now, History: hist}, book)
			if dec.Eligible {
				list = append(list, c.c)
			}
		}
		out[g.String()] = list
	}
	return out, nil
}

func latest(a, b *time.Time) *time.Time {
	switch {
	case a == nil:
		return b
	case b == nil:
		return a
	case a.After(*b):
		return a
	}
	return b
}

func avg(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	s := 0.0
	for _, x := range xs {
		s += x
	}
	return s / float64(len(xs))
}
