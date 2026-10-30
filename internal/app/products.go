package app

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/msboztepe/damarbagis/internal/domain/compat"
	"github.com/msboztepe/damarbagis/internal/domain/product"
	"github.com/msboztepe/damarbagis/internal/httpx"
)

// ProductFilter ürün listesi filtresi.
type ProductFilter struct {
	Status    string
	Component string
	Group     string
	Sort      string // id (varsayılan, keyset) | expiry
}

// ListProducts personel için ürün listesi. Varsayılan sıralama id azalan (keyset);
// sort=expiry FEFO görünümü verir (ilk sayfa).
func (a *App) ListProducts(ctx context.Context, f ProductFilter, p httpx.Page) (httpx.List[Product], error) {
	args := []any{p.Limit + 1}
	where := []string{"true"}
	if f.Status != "" {
		args = append(args, strings.Split(f.Status, ","))
		where = append(where, fmt.Sprintf("status::text = ANY($%d)", len(args)))
	}
	if f.Component != "" {
		if !compat.Component(f.Component).Valid() {
			return httpx.List[Product]{}, httpx.BadRequest("geçersiz bileşen")
		}
		args = append(args, f.Component)
		where = append(where, fmt.Sprintf("component=$%d", len(args)))
	}
	if f.Group != "" {
		g, err := compat.ParseGroup(f.Group)
		if err != nil {
			return httpx.List[Product]{}, httpx.BadRequest(err.Error())
		}
		args = append(args, g.ABO, g.RhPositive)
		where = append(where, fmt.Sprintf("abo=$%d AND rh_positive=$%d", len(args)-1, len(args)))
	}
	order := "id DESC"
	if f.Sort == "expiry" {
		order = "expires_at, id"
	} else if p.Cursor > 0 {
		args = append(args, p.Cursor)
		where = append(where, fmt.Sprintf("id < $%d", len(args)))
	}
	rows, err := a.DB.Query(ctx, `SELECT `+productCols+` FROM products WHERE `+strings.Join(where, " AND ")+` ORDER BY `+order+` LIMIT $1`, args...)
	if err != nil {
		return httpx.List[Product]{}, err
	}
	items, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Product, error) { return a.scanProduct(r) })
	if err != nil {
		return httpx.List[Product]{}, err
	}
	page := pageOf(items, p.Limit, func(p Product) int64 { return p.ID })
	if f.Sort == "expiry" {
		page.NextCursor = nil
	}
	return page, nil
}

// GetProduct ürün ve geçiş geçmişi.
func (a *App) GetProduct(ctx context.Context, id int64) (map[string]any, error) {
	p, err := a.scanProduct(a.DB.QueryRow(ctx, `SELECT `+productCols+` FROM products WHERE id=$1`, id))
	if err == pgx.ErrNoRows {
		return nil, httpx.NotFound("ürün")
	}
	if err != nil {
		return nil, err
	}
	rows, err := a.DB.Query(ctx, `SELECT id, from_status, to_status, actor_id, reason, at FROM product_events WHERE product_id=$1 ORDER BY id`, id)
	if err != nil {
		return nil, err
	}
	events, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (map[string]any, error) {
		var eid int64
		var from *string
		var to string
		var act *int64
		var reason *string
		var at time.Time
		err := r.Scan(&eid, &from, &to, &act, &reason, &at)
		return map[string]any{"id": eid, "from": from, "to": to, "actor_id": act, "reason": reason, "at": at}, err
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"product": p, "events": events}, nil
}

// StockRow stok özeti satırı.
type StockRow struct {
	Component   string     `json:"component"`
	ABO         string     `json:"abo"`
	RhPositive  bool       `json:"rh_positive"`
	Group       string     `json:"group"`
	Available   int        `json:"available"`
	Reserved    int        `json:"reserved"`
	PendingTest int        `json:"pending_test"`
	Quarantined int        `json:"quarantined"`
	Expiring48h int        `json:"expiring_48h"`
	NextExpiry  *time.Time `json:"next_expiry"`
	OpenAlert   *string    `json:"alert,omitempty"`
}

// StockSummary bileşen × kan grubu matrisi (8 grup × 3 bileşen, boş hücreler dahil).
func (a *App) StockSummary(ctx context.Context) ([]StockRow, error) {
	rows, err := a.DB.Query(ctx, `
		WITH g AS (
			SELECT c::component_t AS component, ab::abo_group AS abo, rh
			FROM unnest(ARRAY['RBC','PLT','FFP']) c, unnest(ARRAY['O','A','B','AB']) ab, unnest(ARRAY[false,true]) rh
		), s AS (
			SELECT component, abo, rh_positive,
				count(*) FILTER (WHERE status='available') AS available,
				count(*) FILTER (WHERE status='reserved') AS reserved,
				count(*) FILTER (WHERE status='pending_test') AS pending,
				count(*) FILTER (WHERE status='quarantined') AS quarantined,
				count(*) FILTER (WHERE status IN ('available','reserved') AND expires_at < now() + interval '48 hours') AS expiring,
				min(expires_at) FILTER (WHERE status='available') AS next_expiry
			FROM products WHERE status IN ('available','reserved','pending_test','quarantined')
			GROUP BY 1,2,3
		)
		SELECT g.component, g.abo, g.rh, coalesce(s.available,0), coalesce(s.reserved,0), coalesce(s.pending,0),
			coalesce(s.quarantined,0), coalesce(s.expiring,0), s.next_expiry, al.level
		FROM g LEFT JOIN s ON s.component=g.component AND s.abo=g.abo AND s.rh_positive=g.rh
		LEFT JOIN stock_alerts al ON al.component=g.component AND al.abo=g.abo AND al.rh_positive=g.rh AND al.resolved_at IS NULL
		ORDER BY g.component, g.abo, g.rh`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (StockRow, error) {
		var s StockRow
		err := r.Scan(&s.Component, &s.ABO, &s.RhPositive, &s.Available, &s.Reserved, &s.PendingTest, &s.Quarantined, &s.Expiring48h, &s.NextExpiry, &s.OpenAlert)
		s.Group = compat.Group{ABO: s.ABO, RhPositive: s.RhPositive}.String()
		return s, err
	})
}

// SplitProduct ürünü (pediatrik kullanım için) eşit alt ürünlere böler. Ana ürün "split"
// olur; alt ürünler parent_id ile bağlanır (geri izleme recursive sorgularla yapılır).
func (a *App) SplitProduct(ctx context.Context, id int64, parts int) ([]Product, error) {
	if parts < 2 || parts > 4 {
		return nil, httpx.Validation("parts 2-4 olmalı")
	}
	var out []Product
	err := a.InTx(ctx, "bölme", func(tx pgx.Tx) error {
		p, err := a.scanProduct(tx.QueryRow(ctx, `SELECT `+productCols+` FROM products WHERE id=$1 FOR UPDATE`, id))
		if err == pgx.ErrNoRows {
			return httpx.NotFound("ürün")
		}
		if err != nil {
			return err
		}
		if p.Status != string(product.Available) {
			return httpx.E(409, "invalid_transition", "yalnızca stoktaki (available) ürün bölünebilir")
		}
		if p.Component == "FFP" {
			return httpx.Validation("plazma bu sistemde bölünmez")
		}
		if _, err := tx.Exec(ctx, `UPDATE products SET status='split' WHERE id=$1`, id); err != nil {
			return err
		}
		for i := 1; i <= parts; i++ {
			c, err := a.scanProduct(tx.QueryRow(ctx, `INSERT INTO products (product_no, donation_id, parent_id, component, abo, rh_positive, volume_ml, produced_at, expires_at, status)
				VALUES ($1,$2,$3,$4,$5,$6,$7,now(),$8,'available') RETURNING `+productCols,
				fmt.Sprintf("%s-%d", p.ProductNo, i), p.DonationID, p.ID, p.Component, p.ABO, p.RhPositive, p.VolumeML/parts, p.ExpiresAt))
			if err != nil {
				return err
			}
			out = append(out, c)
		}
		return a.Audit(ctx, tx, "product.split", "product", id, p, out, "")
	})
	return out, err
}

// DiscardProduct ürünü imha eder (gerekçe zorunlu). Geçiş kuralı tetikleyicide de denetlenir.
func (a *App) DiscardProduct(ctx context.Context, id int64, reason string) error {
	if len(strings.TrimSpace(reason)) < 3 {
		return httpx.Validation("imha gerekçesi gerekli")
	}
	return a.InTx(ctx, "imha: "+reason, func(tx pgx.Tx) error {
		var status string
		if err := tx.QueryRow(ctx, `SELECT status FROM products WHERE id=$1 FOR UPDATE`, id).Scan(&status); err != nil {
			if err == pgx.ErrNoRows {
				return httpx.NotFound("ürün")
			}
			return err
		}
		if !product.CanTransition(product.Status(status), product.Discarded) {
			return httpx.E(409, "invalid_transition", fmt.Sprintf("%s durumundaki ürün imha edilemez", status))
		}
		if _, err := tx.Exec(ctx, `UPDATE products SET status='discarded' WHERE id=$1`, id); err != nil {
			return err
		}
		return a.Audit(ctx, tx, "product.discard", "product", id, map[string]string{"status": status}, map[string]string{"status": "discarded"}, reason)
	})
}

// ForceTransition yalnızca yönetici/sorumlu için doğrudan geçiş (geçersiz geçişler DB'de reddedilir).
func (a *App) ForceTransition(ctx context.Context, id int64, to, reason string) error {
	return a.InTx(ctx, reason, func(tx pgx.Tx) error {
		var from string
		if err := tx.QueryRow(ctx, `SELECT status FROM products WHERE id=$1 FOR UPDATE`, id).Scan(&from); err != nil {
			if err == pgx.ErrNoRows {
				return httpx.NotFound("ürün")
			}
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE products SET status=$2::product_status WHERE id=$1`, id, to); err != nil {
			return err
		}
		return a.Audit(ctx, tx, "product.transition", "product", id, map[string]string{"status": from}, map[string]string{"status": to}, reason)
	})
}

// VerifyBarcode imzalı barkodu doğrular ve ürün bilgisini (bağışçısız) döner.
func (a *App) VerifyBarcode(ctx context.Context, code string) (map[string]any, error) {
	no, ok := product.VerifyBarcode(a.Cfg.BarcodeSecret, code)
	if !ok {
		return map[string]any{"valid": false}, nil
	}
	var id int64
	var comp, abo, status string
	var rh bool
	var exp time.Time
	err := a.DB.QueryRow(ctx, `SELECT id, component, abo, rh_positive, status, expires_at FROM hospital_products WHERE product_no=$1`, no).
		Scan(&id, &comp, &abo, &rh, &status, &exp)
	if err == pgx.ErrNoRows {
		return map[string]any{"valid": false, "reason": "imza geçerli ama ürün bulunamadı"}, nil
	}
	if err != nil {
		return nil, err
	}
	return map[string]any{"valid": true, "product_no": no, "component": comp,
		"group": compat.Group{ABO: abo, RhPositive: rh}.String(), "status": status, "expires_at": exp}, nil
}

// HospitalProduct hastaneye dağıtılmış ürün (bağışçı/bağış bilgisi içermez).
type HospitalProduct struct {
	ID         int64      `json:"id"`
	ProductNo  string     `json:"product_no"`
	Barcode    string     `json:"barcode"`
	Component  string     `json:"component"`
	Group      string     `json:"group"`
	VolumeML   int        `json:"volume_ml"`
	ExpiresAt  time.Time  `json:"expires_at"`
	Status     string     `json:"status"`
	RequestID  int64      `json:"request_id"`
	IssuedAt   time.Time  `json:"issued_at"`
	PatientRef *string    `json:"patient_ref,omitempty"`
	Transfused *time.Time `json:"transfused_at,omitempty"`
}

// HospitalProducts hastanenin kendisine dağıtılan ürünler.
func (a *App) HospitalProducts(ctx context.Context, hospitalID int64) ([]HospitalProduct, error) {
	rows, err := a.DB.Query(ctx, `SELECT hp.id, hp.product_no, hp.component, hp.abo, hp.rh_positive, hp.volume_ml, hp.expires_at, hp.status,
			d.request_id, d.issued_at, t.patient_ref, t.transfused_at
		FROM distributions d JOIN hospital_products hp ON hp.id=d.product_id
		LEFT JOIN transfusions t ON t.product_id=d.product_id
		WHERE d.hospital_id=$1 ORDER BY d.id DESC LIMIT 200`, hospitalID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (HospitalProduct, error) {
		var h HospitalProduct
		var abo string
		var rh bool
		err := r.Scan(&h.ID, &h.ProductNo, &h.Component, &abo, &rh, &h.VolumeML, &h.ExpiresAt, &h.Status, &h.RequestID, &h.IssuedAt, &h.PatientRef, &h.Transfused)
		h.Group = compat.Group{ABO: abo, RhPositive: rh}.String()
		h.Barcode = product.SignBarcode(a.Cfg.BarcodeSecret, h.ProductNo)
		return h, err
	})
}

// ---- geri izlenebilirlik ----

// TraceNode izleme ağacındaki bir ürün.
type TraceNode struct {
	ProductID   int64      `json:"product_id"`
	ProductNo   string     `json:"product_no"`
	ParentID    *int64     `json:"parent_id,omitempty"`
	Depth       int        `json:"depth"`
	Component   string     `json:"component"`
	Status      string     `json:"status"`
	DonationID  int64      `json:"donation_id"`
	CollectedAt time.Time  `json:"collected_at"`
	Hospital    *string    `json:"hospital,omitempty"`
	PatientRef  *string    `json:"patient_ref,omitempty"`
	Transfused  *time.Time `json:"transfused_at,omitempty"`
}

// TraceForward bağışçıdan hastaya: bağışçının tüm bağışlarının kök ürünlerinden başlayarak
// parent_id üzerinden alt ürünleri recursive gezer; dağıtım ve transfüzyonları ekler.
func (a *App) TraceForward(ctx context.Context, donorID int64) (map[string]any, error) {
	d, err := a.GetDonor(ctx, donorID)
	if err != nil {
		return nil, err
	}
	rows, err := a.DB.Query(ctx, `
		WITH RECURSIVE tree AS (
			SELECT p.id, p.parent_id, 0 AS depth
			FROM products p JOIN donations dn ON dn.id = p.donation_id
			WHERE dn.donor_id = $1 AND p.parent_id IS NULL
			UNION ALL
			SELECT c.id, c.parent_id, t.depth + 1
			FROM products c JOIN tree t ON c.parent_id = t.id
		)
		SELECT p.id, p.product_no, t.parent_id, t.depth, p.component, p.status, p.donation_id, dn.collected_at,
			h.name, tr.patient_ref, tr.transfused_at
		FROM tree t JOIN products p ON p.id = t.id
		JOIN donations dn ON dn.id = p.donation_id
		LEFT JOIN distributions ds ON ds.product_id = p.id
		LEFT JOIN hospitals h ON h.id = ds.hospital_id
		LEFT JOIN transfusions tr ON tr.product_id = p.id
		ORDER BY dn.collected_at DESC, p.id`, donorID)
	if err != nil {
		return nil, err
	}
	nodes, err := pgx.CollectRows(rows, scanTrace)
	if err != nil {
		return nil, err
	}
	patients := map[string]bool{}
	for _, n := range nodes {
		if n.PatientRef != nil {
			patients[*n.PatientRef] = true
		}
	}
	return map[string]any{"donor": d, "products": nodes, "patients": keys(patients)}, nil
}

// TraceBack hastadan bağışçıya (look-back): hastaya transfüze edilen ürünlerden parent_id
// zinciri boyunca köke çıkar (recursive), bağışa ve bağışçıya ulaşır. Ardından aynı
// bağışçıların diğer ürünlerini (TraceForward ile) listeler; böylece risk altındaki diğer
// hastalar da görülebilir.
func (a *App) TraceBack(ctx context.Context, patientRef string) (map[string]any, error) {
	rows, err := a.DB.Query(ctx, `
		WITH RECURSIVE up AS (
			SELECT p.id, p.parent_id, p.id AS leaf, 0 AS hops
			FROM transfusions t JOIN products p ON p.id = t.product_id
			WHERE t.patient_ref = $1
			UNION ALL
			SELECT par.id, par.parent_id, up.leaf, up.hops + 1
			FROM products par JOIN up ON up.parent_id = par.id
		)
		SELECT DISTINCT leaf.product_no, root.product_no, up.hops, dn.id, dn.collected_at, d.id, d.donor_no
		FROM up
		JOIN products root ON root.id = up.id AND up.parent_id IS NULL
		JOIN products leaf ON leaf.id = up.leaf
		JOIN donations dn ON dn.id = root.donation_id
		JOIN donors d ON d.id = dn.donor_id
		ORDER BY d.id`, patientRef)
	if err != nil {
		return nil, err
	}
	type link struct {
		Transfused  string    `json:"transfused_product_no"`
		Root        string    `json:"root_product_no"`
		Hops        int       `json:"hops"`
		DonationID  int64     `json:"donation_id"`
		CollectedAt time.Time `json:"collected_at"`
		DonorID     int64     `json:"donor_id"`
		DonorNo     string    `json:"donor_no"`
	}
	links, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (link, error) {
		var l link
		err := r.Scan(&l.Transfused, &l.Root, &l.Hops, &l.DonationID, &l.CollectedAt, &l.DonorID, &l.DonorNo)
		return l, err
	})
	if err != nil {
		return nil, err
	}
	if len(links) == 0 {
		return nil, httpx.NotFound("bu hasta için transfüzyon kaydı")
	}
	related := map[string]bool{}
	donors := map[int64]bool{}
	for _, l := range links {
		if donors[l.DonorID] {
			continue
		}
		donors[l.DonorID] = true
		fw, err := a.TraceForward(ctx, l.DonorID)
		if err != nil {
			return nil, err
		}
		for _, p := range fw["patients"].([]string) {
			if p != patientRef {
				related[p] = true
			}
		}
	}
	return map[string]any{"patient_ref": patientRef, "links": links, "other_recipients_of_same_donors": keys(related)}, nil
}

func scanTrace(r pgx.CollectableRow) (TraceNode, error) {
	var n TraceNode
	err := r.Scan(&n.ProductID, &n.ProductNo, &n.ParentID, &n.Depth, &n.Component, &n.Status, &n.DonationID, &n.CollectedAt,
		&n.Hospital, &n.PatientRef, &n.Transfused)
	return n, err
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
