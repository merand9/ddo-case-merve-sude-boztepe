// Package api HTTP uç noktalarını ve rol tabanlı erişim kurallarını tanımlar.
package api

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/msboztepe/damarbagis/internal/app"
	"github.com/msboztepe/damarbagis/internal/domain/compat"
	"github.com/msboztepe/damarbagis/internal/httpx"
	"github.com/msboztepe/damarbagis/internal/metrics"
)

//go:embed openapi.yaml
var openapiSpec []byte

// Roller
const (
	rDonor    = "donor"
	rNurse    = "nurse"
	rLab      = "lab"
	rBank     = "bank"
	rHospital = "hospital"
	rAdmin    = "admin"
)

// Server HTTP sunucusu bileşenleri.
type Server struct {
	App     *app.App
	Metrics *metrics.Registry
	Log     *slog.Logger
}

// Router tüm rotaları kurar. Her uç nokta açıkça bir rol listesiyle korunur; listede olmayan
// rol 403 alır. Yalnızca /health, /metrics, /openapi.yaml ve /auth/* kimlik doğrulamasızdır.
func (s *Server) Router() http.Handler {
	a := s.App
	r := chi.NewRouter()
	r.Use(httpx.Observe(s.Log, s.Metrics))
	r.Use(httpx.CORS(a.Cfg.CORSOrigin))
	r.Use(httpx.Timeout(a.Cfg.RequestTimeout))

	s.registerGauges()

	r.Get("/health", s.health)
	r.Get("/metrics", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		s.Metrics.Write(w)
	})
	r.Get("/openapi.yaml", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/yaml")
		_, _ = w.Write(openapiSpec)
	})

	r.Route("/auth", func(r chi.Router) {
		r.Post("/login", httpx.H(s.login))
		r.Post("/refresh", httpx.H(s.refresh))
		r.Post("/logout", httpx.H(s.logout))
	})

	staff := []string{rNurse, rLab, rBank, rAdmin}
	r.Route("/api/v1", func(r chi.Router) {
		r.Use(httpx.Authenticate(a.Issuer))
		r.Use(httpx.Idempotency(a.DB))
		with := func(roles ...string) chi.Router { return r.With(httpx.Require(roles...)) }

		with(rDonor, rNurse, rLab, rBank, rHospital, rAdmin).Get("/me", httpx.H(s.me))
		with(rDonor, rNurse, rLab, rBank, rHospital, rAdmin).Get("/hospitals", httpx.H(s.hospitals))
		with(append(staff, rHospital)...).Get("/compat", httpx.H(s.compat))
		with(append(staff, rHospital)...).Get("/products/verify", httpx.H(s.verifyBarcode))
		with(staff...).Get("/live", httpx.H(s.live))
		with(staff...).Get("/stock", httpx.H(s.stock))

		// Bağışçının kendi ekranları
		with(rDonor).Get("/me/donor", httpx.H(s.myDonor))
		with(rDonor).Post("/me/appointments", httpx.H(s.myBook))
		with(rDonor).Post("/me/appointments/{id}/cancel", httpx.H(s.myCancel))

		// Bağışçı kaydı ve uygunluk
		with(rNurse, rBank, rAdmin).Get("/donors", httpx.H(s.listDonors))
		with(rNurse, rAdmin).Post("/donors", httpx.H(s.createDonor))
		with(rNurse, rLab, rBank, rAdmin).Get("/donors/{id}", httpx.H(s.getDonor))
		with(rNurse, rAdmin).Post("/donors/{id}/eligibility", httpx.H(s.eligibility))
		with(rNurse, rBank, rAdmin).Get("/donors/{id}/deferrals", httpx.H(s.listDeferrals))
		with(rNurse, rAdmin).Post("/donors/{id}/deferrals", httpx.H(s.addDeferral))
		with(rNurse, rAdmin).Get("/donors/{id}/health", httpx.H(s.readHealth))

		// Randevu ve bağış
		with(rNurse, rAdmin).Get("/appointments", httpx.H(s.listAppointments))
		with(rNurse, rAdmin).Post("/appointments", httpx.H(s.bookAppointment))
		with(rNurse, rAdmin).Post("/appointments/{id}/cancel", httpx.H(s.cancelAppointment))
		with(rNurse, rAdmin).Post("/donations", httpx.H(s.createDonation))
		with(rNurse, rLab, rBank, rAdmin).Get("/donations", httpx.H(s.listDonations))
		with(rNurse, rLab, rBank, rAdmin).Get("/donations/{id}", httpx.H(s.getDonation))

		// Laboratuvar: test, karantina, üretim, çift onay
		with(rLab, rAdmin).Post("/donations/{id}/components", httpx.H(s.produce))
		with(rLab).Post("/donations/{id}/test-results", httpx.H(s.testResult))
		with(rLab).Post("/donations/{id}/test-changes", httpx.H(s.requestChange))
		with(rLab, rAdmin).Get("/test-changes", httpx.H(s.listChanges))
		with(rLab).Post("/test-changes/{id}/approve", httpx.H(s.decideChange(true)))
		with(rLab).Post("/test-changes/{id}/reject", httpx.H(s.decideChange(false)))

		// Stok ve ürünler
		with(rLab, rBank, rAdmin).Get("/products", httpx.H(s.listProducts))
		with(rLab, rBank, rAdmin).Get("/products/{id}", httpx.H(s.getProduct))
		with(rBank).Post("/products/{id}/split", httpx.H(s.split))
		with(rLab, rBank).Post("/products/{id}/discard", httpx.H(s.discard))
		with(rBank, rAdmin).Post("/products/{id}/transition", httpx.H(s.transition))

		// Talepler, tahsis, dağıtım
		with(rHospital).Post("/requests", httpx.H(s.createRequest))
		with(rHospital, rBank, rAdmin).Get("/requests", httpx.H(s.listRequests))
		with(rHospital, rBank, rAdmin).Get("/requests/{id}", httpx.H(s.getRequest))
		with(rHospital, rBank).Post("/requests/{id}/cancel", httpx.H(s.cancelRequest))
		with(rBank).Post("/requests/{id}/allocate", httpx.H(s.allocate))
		with(rBank).Post("/requests/{id}/issue", httpx.H(s.issue))
		with(rBank, rAdmin).Get("/reservations", httpx.H(s.reservations))
		with(rBank, rAdmin).Get("/reservations/{id}", httpx.H(s.getReservation))
		with(rBank).Post("/reservations/{id}/release", httpx.H(s.releaseReservation))
		with(rHospital).Get("/hospital/products", httpx.H(s.hospitalProducts))
		with(rHospital).Post("/transfusions", httpx.H(s.transfusion))

		// Geri izleme, tahmin, raporlar
		with(rBank, rAdmin).Get("/trace/donors/{id}", httpx.H(s.traceDonor))
		with(rBank, rAdmin).Get("/trace/patients/{ref}", httpx.H(s.tracePatient))
		with(rBank, rAdmin).Get("/forecast", httpx.H(s.forecast))
		with(rBank, rAdmin).Get("/forecast/backtest", httpx.H(s.backtest))
		with(rBank, rAdmin).Get("/alerts", httpx.H(s.alerts))
		with(rBank, rAdmin).Post("/alerts/evaluate", httpx.H(s.evaluateAlerts))
		with(rBank, rAdmin).Get("/call-list", httpx.H(s.callList))
		with(rBank, rAdmin).Get("/reports/summary", httpx.H(s.summary))

		// Yönetim
		with(rAdmin).Get("/users", httpx.H(s.listUsers))
		with(rAdmin).Post("/users", httpx.H(s.createUser))
		with(rAdmin, rNurse, rBank).Get("/rules", httpx.H(s.listRules))
		with(rAdmin).Post("/rules", httpx.H(s.addRule))
		with(rAdmin).Get("/audit", httpx.H(s.audit))
	})
	r.NotFound(func(w http.ResponseWriter, r *http.Request) {
		httpx.WriteError(w, r, httpx.NotFound("uç nokta"))
	})
	r.MethodNotAllowed(func(w http.ResponseWriter, r *http.Request) {
		httpx.WriteError(w, r, httpx.E(405, "method_not_allowed", "yöntem desteklenmiyor"))
	})
	return r
}

func (s *Server) registerGauges() {
	s.Metrics.Gauge(func(w io.Writer) {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		rows, err := s.App.DB.Query(ctx, `SELECT component, abo, rh_positive, count(*) FROM products
			WHERE status='available' GROUP BY 1,2,3`)
		if err != nil {
			return
		}
		defer rows.Close()
		fmt.Fprintln(w, "# HELP damar_stock_available_units Stoktaki kullanılabilir ürün sayısı")
		fmt.Fprintln(w, "# TYPE damar_stock_available_units gauge")
		for rows.Next() {
			var c, abo string
			var rh bool
			var n int
			if rows.Scan(&c, &abo, &rh, &n) == nil {
				fmt.Fprintf(w, "damar_stock_available_units{component=%q,group=%q} %d\n", c, compat.Group{ABO: abo, RhPositive: rh}.String(), n)
			}
		}
		st := s.App.DB.Stat()
		fmt.Fprintln(w, "# TYPE damar_db_pool_acquired_conns gauge")
		fmt.Fprintf(w, "damar_db_pool_acquired_conns %d\n", st.AcquiredConns())
		fmt.Fprintln(w, "# TYPE damar_db_pool_total_conns gauge")
		fmt.Fprintf(w, "damar_db_pool_total_conns %d\n", st.TotalConns())
	})
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	start := time.Now()
	var version string
	err := s.App.DB.QueryRow(ctx, `SELECT coalesce(max(version),'') FROM schema_migrations`).Scan(&version)
	body := map[string]any{"status": "ok", "time": time.Now().UTC(),
		"checks": map[string]any{"database": map[string]any{"status": "ok", "latency_ms": time.Since(start).Milliseconds(), "schema": version}}}
	if err != nil {
		body["status"] = "degraded"
		body["checks"] = map[string]any{"database": map[string]any{"status": "down", "error": "veritabanına ulaşılamıyor"}}
		httpx.WriteJSON(w, http.StatusServiceUnavailable, body)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, body)
}

// ---- yardımcılar ----

func ok(w http.ResponseWriter, v any) error { httpx.WriteJSON(w, http.StatusOK, v); return nil }
func created(w http.ResponseWriter, v any) error {
	httpx.WriteJSON(w, http.StatusCreated, v)
	return nil
}

func claims(r *http.Request) (role string, hospitalID int64) {
	c := httpx.ClaimsFrom(r.Context())
	return c.Role, c.HospitalID
}

func qInt(r *http.Request, name string, def int) (int, error) {
	v := r.URL.Query().Get(name)
	if v == "" {
		return def, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, httpx.BadRequest(name + " sayı olmalı")
	}
	return n, nil
}

// ---- auth ----

func (s *Server) login(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	if in.Email == "" || in.Password == "" {
		return httpx.Validation("email ve password gerekli")
	}
	t, err := s.App.Login(r.Context(), in.Email, in.Password)
	if err != nil {
		return err
	}
	return ok(w, t)
}

func (s *Server) refresh(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		RefreshToken string `json:"refresh_token"`
	}
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	t, err := s.App.Refresh(r.Context(), in.RefreshToken)
	if err != nil {
		return err
	}
	return ok(w, t)
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		RefreshToken string `json:"refresh_token"`
	}
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	if err := s.App.Logout(r.Context(), in.RefreshToken); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *Server) me(w http.ResponseWriter, r *http.Request) error {
	u, err := s.App.Me(r.Context())
	if err != nil {
		return err
	}
	return ok(w, u)
}

func (s *Server) hospitals(w http.ResponseWriter, r *http.Request) error {
	rows, err := s.App.DB.Query(r.Context(), `SELECT id, name, city FROM hospitals ORDER BY id`)
	if err != nil {
		return err
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id int64
		var name, city string
		if err := rows.Scan(&id, &name, &city); err != nil {
			return err
		}
		out = append(out, map[string]any{"id": id, "name": name, "city": city})
	}
	return ok(w, out)
}

func (s *Server) compat(w http.ResponseWriter, r *http.Request) error {
	c := compat.Component(r.URL.Query().Get("component"))
	if c == "" {
		c = compat.RBC
	}
	if !c.Valid() {
		return httpx.BadRequest("geçersiz bileşen")
	}
	g, err := compat.ParseGroup(r.URL.Query().Get("group"))
	if err != nil {
		return httpx.BadRequest(err.Error())
	}
	cs := compat.Candidates(c, g)
	out := make([]map[string]any, len(cs))
	for i, x := range cs {
		out[i] = map[string]any{"group": x.Group.String(), "rank": x.Rank, "exact": x.Exact}
	}
	return ok(w, map[string]any{"component": c, "recipient": g.String(), "candidates": out})
}

func (s *Server) live(w http.ResponseWriter, r *http.Request) error {
	v, err := s.App.Live(r.Context())
	if err != nil {
		return err
	}
	return ok(w, v)
}

func (s *Server) stock(w http.ResponseWriter, r *http.Request) error {
	v, err := s.App.StockSummary(r.Context())
	if err != nil {
		return err
	}
	return ok(w, v)
}

// ---- bağışçı ----

func (s *Server) myDonor(w http.ResponseWriter, r *http.Request) error {
	v, err := s.App.MyDonor(r.Context())
	if err != nil {
		return err
	}
	return ok(w, v)
}

type bookIn struct {
	DonorID      int64     `json:"donor_id"`
	ScheduledAt  time.Time `json:"scheduled_at"`
	DonationType string    `json:"donation_type"`
}

func (s *Server) myBook(w http.ResponseWriter, r *http.Request) error {
	var in bookIn
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	ap, err := s.App.BookAppointment(r.Context(), httpx.ClaimsFrom(r.Context()).DonorID, in.ScheduledAt, in.DonationType)
	if err != nil {
		return err
	}
	return created(w, ap)
}

func (s *Server) myCancel(w http.ResponseWriter, r *http.Request) error {
	id, err := httpx.IDParam(r, "id")
	if err != nil {
		return err
	}
	if err := s.App.CancelAppointment(r.Context(), id, httpx.ClaimsFrom(r.Context()).DonorID); err != nil {
		return err
	}
	return ok(w, map[string]string{"status": "cancelled"})
}

func (s *Server) listDonors(w http.ResponseWriter, r *http.Request) error {
	p, err := httpx.PageParams(r, 50, 200)
	if err != nil {
		return err
	}
	v, err := s.App.ListDonors(r.Context(), r.URL.Query().Get("q"), r.URL.Query().Get("group"), p)
	if err != nil {
		return err
	}
	return ok(w, v)
}

func (s *Server) createDonor(w http.ResponseWriter, r *http.Request) error {
	var in app.NewDonor
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	d, err := s.App.RegisterDonor(r.Context(), in)
	if err != nil {
		return err
	}
	return created(w, d)
}

func (s *Server) getDonor(w http.ResponseWriter, r *http.Request) error {
	id, err := httpx.IDParam(r, "id")
	if err != nil {
		return err
	}
	d, err := s.App.GetDonor(r.Context(), id)
	if err != nil {
		return err
	}
	return ok(w, d)
}

func (s *Server) eligibility(w http.ResponseWriter, r *http.Request) error {
	id, err := httpx.IDParam(r, "id")
	if err != nil {
		return err
	}
	var in app.EligibilityRequest
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	d, err := s.App.CheckEligibility(r.Context(), id, in)
	if err != nil {
		return err
	}
	return ok(w, d)
}

func (s *Server) listDeferrals(w http.ResponseWriter, r *http.Request) error {
	id, err := httpx.IDParam(r, "id")
	if err != nil {
		return err
	}
	v, err := s.App.ListDeferrals(r.Context(), id)
	if err != nil {
		return err
	}
	return ok(w, v)
}

func (s *Server) addDeferral(w http.ResponseWriter, r *http.Request) error {
	id, err := httpx.IDParam(r, "id")
	if err != nil {
		return err
	}
	var in struct {
		ReasonCode string `json:"reason_code"`
		StartsOn   string `json:"starts_on"`
		Note       string `json:"note"`
	}
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	d, err := s.App.AddDeferral(r.Context(), id, in.ReasonCode, in.StartsOn, in.Note)
	if err != nil {
		return err
	}
	return created(w, d)
}

func (s *Server) readHealth(w http.ResponseWriter, r *http.Request) error {
	id, err := httpx.IDParam(r, "id")
	if err != nil {
		return err
	}
	// Tarayıcılar başlıkta ASCII dışı karakter gönderemez; ön yüz gerekçeyi yüzde-kodlar.
	reason := r.Header.Get("X-Access-Reason")
	if dec, err := url.PathUnescape(reason); err == nil {
		reason = dec
	}
	h, err := s.App.ReadHealth(r.Context(), id, reason)
	if err != nil {
		return err
	}
	return ok(w, h)
}

func (s *Server) listAppointments(w http.ResponseWriter, r *http.Request) error {
	var donor int64
	if v := r.URL.Query().Get("donor_id"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return httpx.BadRequest("donor_id sayı olmalı")
		}
		donor = n
	}
	v, err := s.App.ListAppointments(r.Context(), donor, r.URL.Query().Get("date"))
	if err != nil {
		return err
	}
	return ok(w, v)
}

func (s *Server) bookAppointment(w http.ResponseWriter, r *http.Request) error {
	var in bookIn
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	ap, err := s.App.BookAppointment(r.Context(), in.DonorID, in.ScheduledAt, in.DonationType)
	if err != nil {
		return err
	}
	return created(w, ap)
}

func (s *Server) cancelAppointment(w http.ResponseWriter, r *http.Request) error {
	id, err := httpx.IDParam(r, "id")
	if err != nil {
		return err
	}
	if err := s.App.CancelAppointment(r.Context(), id, 0); err != nil {
		return err
	}
	return ok(w, map[string]string{"status": "cancelled"})
}

func (s *Server) createDonation(w http.ResponseWriter, r *http.Request) error {
	var in app.NewDonation
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	d, err := s.App.RecordDonation(r.Context(), in)
	if err != nil {
		return err
	}
	return created(w, d)
}

func (s *Server) listDonations(w http.ResponseWriter, r *http.Request) error {
	p, err := httpx.PageParams(r, 50, 200)
	if err != nil {
		return err
	}
	var donor int64
	if v := r.URL.Query().Get("donor_id"); v != "" {
		donor, _ = strconv.ParseInt(v, 10, 64)
	}
	v, err := s.App.ListDonations(r.Context(), r.URL.Query().Get("pending"), donor, p)
	if err != nil {
		return err
	}
	return ok(w, v)
}

func (s *Server) getDonation(w http.ResponseWriter, r *http.Request) error {
	id, err := httpx.IDParam(r, "id")
	if err != nil {
		return err
	}
	d, err := s.App.GetDonation(r.Context(), id)
	if err != nil {
		return err
	}
	return ok(w, d)
}

func (s *Server) produce(w http.ResponseWriter, r *http.Request) error {
	id, err := httpx.IDParam(r, "id")
	if err != nil {
		return err
	}
	var in struct {
		Components []string `json:"components"`
	}
	if r.ContentLength != 0 {
		if err := httpx.Decode(r, &in); err != nil {
			return err
		}
	}
	ps, err := s.App.ProduceComponents(r.Context(), id, in.Components)
	if err != nil {
		return err
	}
	return created(w, ps)
}

func (s *Server) testResult(w http.ResponseWriter, r *http.Request) error {
	id, err := httpx.IDParam(r, "id")
	if err != nil {
		return err
	}
	var m app.Markers
	if err := httpx.Decode(r, &m); err != nil {
		return err
	}
	res, err := s.App.EnterTestResult(r.Context(), id, m)
	if err != nil {
		return err
	}
	return created(w, res)
}

func (s *Server) requestChange(w http.ResponseWriter, r *http.Request) error {
	id, err := httpx.IDParam(r, "id")
	if err != nil {
		return err
	}
	var in struct {
		Proposed app.Markers `json:"proposed"`
		Reason   string      `json:"reason"`
	}
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	tc, err := s.App.RequestTestChange(r.Context(), id, in.Proposed, in.Reason)
	if err != nil {
		return err
	}
	return created(w, tc)
}

func (s *Server) listChanges(w http.ResponseWriter, r *http.Request) error {
	v, err := s.App.ListTestChanges(r.Context(), r.URL.Query().Get("status"))
	if err != nil {
		return err
	}
	return ok(w, v)
}

func (s *Server) decideChange(approve bool) httpx.Handler {
	return func(w http.ResponseWriter, r *http.Request) error {
		id, err := httpx.IDParam(r, "id")
		if err != nil {
			return err
		}
		tc, res, err := s.App.DecideTestChange(r.Context(), id, approve)
		if err != nil {
			return err
		}
		return ok(w, map[string]any{"change": tc, "effect": res})
	}
}

// ---- ürünler ----

func (s *Server) listProducts(w http.ResponseWriter, r *http.Request) error {
	p, err := httpx.PageParams(r, 50, 500)
	if err != nil {
		return err
	}
	q := r.URL.Query()
	v, err := s.App.ListProducts(r.Context(), app.ProductFilter{Status: q.Get("status"), Component: q.Get("component"), Group: q.Get("group"), Sort: q.Get("sort")}, p)
	if err != nil {
		return err
	}
	return ok(w, v)
}

func (s *Server) getProduct(w http.ResponseWriter, r *http.Request) error {
	id, err := httpx.IDParam(r, "id")
	if err != nil {
		return err
	}
	v, err := s.App.GetProduct(r.Context(), id)
	if err != nil {
		return err
	}
	return ok(w, v)
}

func (s *Server) split(w http.ResponseWriter, r *http.Request) error {
	id, err := httpx.IDParam(r, "id")
	if err != nil {
		return err
	}
	var in struct {
		Parts int `json:"parts"`
	}
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	v, err := s.App.SplitProduct(r.Context(), id, in.Parts)
	if err != nil {
		return err
	}
	return created(w, v)
}

func (s *Server) discard(w http.ResponseWriter, r *http.Request) error {
	id, err := httpx.IDParam(r, "id")
	if err != nil {
		return err
	}
	var in struct {
		Reason string `json:"reason"`
	}
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	if err := s.App.DiscardProduct(r.Context(), id, in.Reason); err != nil {
		return err
	}
	return ok(w, map[string]string{"status": "discarded"})
}

func (s *Server) transition(w http.ResponseWriter, r *http.Request) error {
	id, err := httpx.IDParam(r, "id")
	if err != nil {
		return err
	}
	var in struct {
		To     string `json:"to"`
		Reason string `json:"reason"`
	}
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	if len(strings.TrimSpace(in.Reason)) < 3 {
		return httpx.Validation("gerekçe gerekli")
	}
	if err := s.App.ForceTransition(r.Context(), id, in.To, in.Reason); err != nil {
		return err
	}
	return ok(w, map[string]string{"status": in.To})
}

func (s *Server) verifyBarcode(w http.ResponseWriter, r *http.Request) error {
	v, err := s.App.VerifyBarcode(r.Context(), r.URL.Query().Get("code"))
	if err != nil {
		return err
	}
	return ok(w, v)
}

// ---- talepler ----

func (s *Server) createRequest(w http.ResponseWriter, r *http.Request) error {
	var in app.NewRequest
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	_, hid := claims(r)
	v, err := s.App.CreateRequest(r.Context(), hid, in)
	if err != nil {
		return err
	}
	return created(w, v)
}

func hospitalScope(r *http.Request) int64 {
	role, hid := claims(r)
	if role == rHospital {
		return hid
	}
	return 0
}

func (s *Server) listRequests(w http.ResponseWriter, r *http.Request) error {
	p, err := httpx.PageParams(r, 50, 200)
	if err != nil {
		return err
	}
	v, err := s.App.ListRequests(r.Context(), hospitalScope(r), r.URL.Query().Get("status"), p)
	if err != nil {
		return err
	}
	return ok(w, v)
}

func (s *Server) getRequest(w http.ResponseWriter, r *http.Request) error {
	id, err := httpx.IDParam(r, "id")
	if err != nil {
		return err
	}
	v, err := s.App.GetRequest(r.Context(), id, hospitalScope(r))
	if err != nil {
		return err
	}
	return ok(w, v)
}

func (s *Server) cancelRequest(w http.ResponseWriter, r *http.Request) error {
	id, err := httpx.IDParam(r, "id")
	if err != nil {
		return err
	}
	if err := s.App.CancelRequest(r.Context(), id, hospitalScope(r)); err != nil {
		return err
	}
	return ok(w, map[string]string{"status": "cancelled"})
}

func (s *Server) allocate(w http.ResponseWriter, r *http.Request) error {
	id, err := httpx.IDParam(r, "id")
	if err != nil {
		return err
	}
	var in struct {
		TTLSeconds int `json:"ttl_seconds"`
	}
	if r.ContentLength != 0 {
		if err := httpx.Decode(r, &in); err != nil {
			return err
		}
	}
	ttl := time.Duration(in.TTLSeconds) * time.Second
	if in.TTLSeconds == 0 {
		var err error
		if ttl, err = s.defaultTTL(r); err != nil {
			return err
		}
	}
	res, err := s.App.Allocate(r.Context(), id, ttl)
	if err != nil {
		return err
	}
	return ok(w, res)
}

func (s *Server) defaultTTL(r *http.Request) (time.Duration, error) {
	sp, err := s.App.ShelfAt(r.Context(), nil, time.Now())
	if err != nil {
		return 0, err
	}
	return time.Duration(sp.ReservationTTLMinutes) * time.Minute, nil
}

func (s *Server) issue(w http.ResponseWriter, r *http.Request) error {
	id, err := httpx.IDParam(r, "id")
	if err != nil {
		return err
	}
	v, err := s.App.IssueRequest(r.Context(), id)
	if err != nil {
		return err
	}
	return ok(w, v)
}

func (s *Server) reservations(w http.ResponseWriter, r *http.Request) error {
	v, err := s.App.ActiveReservations(r.Context())
	if err != nil {
		return err
	}
	return ok(w, v)
}

func (s *Server) getReservation(w http.ResponseWriter, r *http.Request) error {
	id, err := httpx.IDParam(r, "id")
	if err != nil {
		return err
	}
	v, err := s.App.GetReservation(r.Context(), id)
	if err != nil {
		return err
	}
	return ok(w, v)
}

func (s *Server) releaseReservation(w http.ResponseWriter, r *http.Request) error {
	id, err := httpx.IDParam(r, "id")
	if err != nil {
		return err
	}
	if err := s.App.ReleaseReservation(r.Context(), id); err != nil {
		return err
	}
	return ok(w, map[string]string{"status": "released"})
}

func (s *Server) hospitalProducts(w http.ResponseWriter, r *http.Request) error {
	_, hid := claims(r)
	v, err := s.App.HospitalProducts(r.Context(), hid)
	if err != nil {
		return err
	}
	return ok(w, v)
}

func (s *Server) transfusion(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		ProductNo  string `json:"product_no"`
		PatientRef string `json:"patient_ref"`
	}
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	_, hid := claims(r)
	if err := s.App.RecordTransfusion(r.Context(), hid, in.ProductNo, in.PatientRef); err != nil {
		return err
	}
	return created(w, map[string]string{"status": "transfused", "product_no": in.ProductNo})
}

// ---- izleme, tahmin, rapor ----

func (s *Server) traceDonor(w http.ResponseWriter, r *http.Request) error {
	id, err := httpx.IDParam(r, "id")
	if err != nil {
		return err
	}
	v, err := s.App.TraceForward(r.Context(), id)
	if err != nil {
		return err
	}
	return ok(w, v)
}

func (s *Server) tracePatient(w http.ResponseWriter, r *http.Request) error {
	v, err := s.App.TraceBack(r.Context(), chi.URLParam(r, "ref"))
	if err != nil {
		return err
	}
	return ok(w, v)
}

func (s *Server) forecast(w http.ResponseWriter, r *http.Request) error {
	h, err := qInt(r, "horizon", 7)
	if err != nil {
		return err
	}
	c := r.URL.Query().Get("component")
	if c == "" {
		c = "RBC"
	}
	v, err := s.App.Forecast(r.Context(), c, h)
	if err != nil {
		return err
	}
	return ok(w, v)
}

func (s *Server) backtest(w http.ResponseWriter, r *http.Request) error {
	d, err := qInt(r, "days", 28)
	if err != nil {
		return err
	}
	c := r.URL.Query().Get("component")
	if c == "" {
		c = "RBC"
	}
	v, err := s.App.Backtest(r.Context(), c, d)
	if err != nil {
		return err
	}
	return ok(w, v)
}

func (s *Server) alerts(w http.ResponseWriter, r *http.Request) error {
	v, err := s.App.ListAlerts(r.Context(), r.URL.Query().Get("all") == "1")
	if err != nil {
		return err
	}
	return ok(w, v)
}

func (s *Server) evaluateAlerts(w http.ResponseWriter, r *http.Request) error {
	if err := s.App.EvaluateAlerts(r.Context()); err != nil {
		return err
	}
	return s.alerts(w, r)
}

func (s *Server) callList(w http.ResponseWriter, r *http.Request) error {
	limit, err := qInt(r, "limit", 50)
	if err != nil {
		return err
	}
	v, err := s.App.CallList(r.Context(), r.URL.Query().Get("group"), limit)
	if err != nil {
		return err
	}
	return ok(w, v)
}

func (s *Server) summary(w http.ResponseWriter, r *http.Request) error {
	to := time.Now().UTC()
	from := to.AddDate(0, 0, -30)
	var err error
	if v := r.URL.Query().Get("from"); v != "" {
		if from, err = time.Parse("2006-01-02", v); err != nil {
			return httpx.BadRequest("from YYYY-AA-GG olmalı")
		}
	}
	if v := r.URL.Query().Get("to"); v != "" {
		if to, err = time.Parse("2006-01-02", v); err != nil {
			return httpx.BadRequest("to YYYY-AA-GG olmalı")
		}
	}
	if !to.After(from) {
		return httpx.BadRequest("to, from'dan sonra olmalı")
	}
	v, err := s.App.Summary(r.Context(), from, to)
	if err != nil {
		return err
	}
	return ok(w, v)
}

// ---- yönetim ----

func (s *Server) listUsers(w http.ResponseWriter, r *http.Request) error {
	v, err := s.App.ListUsers(r.Context())
	if err != nil {
		return err
	}
	return ok(w, v)
}

func (s *Server) createUser(w http.ResponseWriter, r *http.Request) error {
	var in app.NewUser
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	u, err := s.App.CreateUser(r.Context(), in)
	if err != nil {
		return err
	}
	return created(w, u)
}

func (s *Server) listRules(w http.ResponseWriter, r *http.Request) error {
	v, err := s.App.ListRules(r.Context(), r.URL.Query().Get("code"))
	if err != nil {
		return err
	}
	return ok(w, v)
}

func (s *Server) addRule(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Code      string          `json:"code"`
		ValidFrom string          `json:"valid_from"`
		Note      string          `json:"note"`
		Params    json.RawMessage `json:"params"`
	}
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	v, err := s.App.AddRuleVersion(r.Context(), in.Code, in.ValidFrom, in.Note, in.Params)
	if err != nil {
		return err
	}
	return created(w, v)
}

func (s *Server) audit(w http.ResponseWriter, r *http.Request) error {
	p, err := httpx.PageParams(r, 50, 500)
	if err != nil {
		return err
	}
	q := r.URL.Query()
	v, err := s.App.ListAudit(r.Context(), q.Get("action"), q.Get("entity"), q.Get("entity_id"), p)
	if err != nil {
		return err
	}
	return ok(w, v)
}
