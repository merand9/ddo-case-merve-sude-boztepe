// Package httpx HTTP katmanı için ortak yardımcılar: hata biçimi, JSON, ara katmanlar,
// kimlik doğrulama ve idempotans.
package httpx

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/msboztepe/damarbagis/internal/auth"
	"github.com/msboztepe/damarbagis/internal/metrics"
)

// Error API hata tipi. Tüm hatalar {"error":{"code","message","details"}} biçiminde döner.
type Error struct {
	Status  int    `json:"-"`
	Code    string `json:"code"`
	Message string `json:"message"`
	Details any    `json:"details,omitempty"`
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }

// E kısa yoldan hata üretir.
func E(status int, code, msg string) *Error { return &Error{Status: status, Code: code, Message: msg} }

// Sık kullanılan hatalar.
func BadRequest(msg string) *Error { return E(http.StatusBadRequest, "bad_request", msg) }
func Validation(msg string) *Error {
	return E(http.StatusUnprocessableEntity, "validation_failed", msg)
}
func NotFound(what string) *Error    { return E(http.StatusNotFound, "not_found", what+" bulunamadı") }
func Conflict(msg string) *Error     { return E(http.StatusConflict, "conflict", msg) }
func Forbidden(msg string) *Error    { return E(http.StatusForbidden, "forbidden", msg) }
func Unauthorized(msg string) *Error { return E(http.StatusUnauthorized, "unauthorized", msg) }

// WithDetails ayrıntı ekler.
func (e *Error) WithDetails(d any) *Error { e.Details = d; return e }

// FromErr bir Go hatasını API hatasına çevirir (veritabanı kısıt hataları dahil).
func FromErr(err error) *Error {
	var ae *Error
	if errors.As(err, &ae) {
		return ae
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return NotFound("kayıt")
	}
	var ce *pgconn.ConnectError
	if errors.As(err, &ce) {
		return E(http.StatusServiceUnavailable, "service_unavailable", "veritabanına şu an ulaşılamıyor, kısa süre sonra tekrar deneyin")
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return E(http.StatusGatewayTimeout, "timeout", "işlem zaman aşımına uğradı")
	}
	var pg *pgconn.PgError
	if errors.As(err, &pg) {
		switch pg.Code {
		case "23505":
			return Conflict("kayıt zaten mevcut (" + pg.ConstraintName + ")")
		case "23503":
			return Validation("ilişkili kayıt bulunamadı (" + pg.ConstraintName + ")")
		case "23514", "23502", "22P02", "22007", "22008":
			return Validation("veri kısıtı ihlali: " + pg.Message)
		case "23P01":
			return Conflict("geçerlilik aralığı çakışıyor (" + pg.ConstraintName + ")")
		case "P0010":
			return E(http.StatusConflict, "invalid_transition", pg.Message)
		case "P0011", "P0012":
			return E(http.StatusConflict, "immutable", pg.Message)
		case "P0013":
			return E(http.StatusForbidden, "dual_approval_required", pg.Message)
		case "40001", "40P01":
			return E(http.StatusConflict, "retry", "eşzamanlı işlem çakışması, tekrar deneyin")
		}
	}
	return E(http.StatusInternalServerError, "internal", "beklenmeyen bir hata oluştu")
}

// WriteJSON yanıt yazar.
func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if v != nil {
		_ = json.NewEncoder(w).Encode(v)
	}
}

// WriteError hatayı standart biçimde yazar; 5xx hataları loglanır.
func WriteError(w http.ResponseWriter, r *http.Request, err error) {
	ae := FromErr(err)
	if ae.Status >= 500 {
		Logger(r.Context()).Error("istek hatası", "err", err.Error(), "path", r.URL.Path)
	}
	WriteJSON(w, ae.Status, map[string]any{"error": ae})
}

// Handler hata dönen handler tipi.
type Handler func(w http.ResponseWriter, r *http.Request) error

// H Handler'ı http.HandlerFunc'a çevirir.
func H(h Handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := h(w, r); err != nil {
			WriteError(w, r, err)
		}
	}
}

// Decode gövdeyi katı biçimde çözer (bilinmeyen alan reddedilir, 1 MB sınır).
func Decode(r *http.Request, v any) error {
	dec := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return BadRequest("geçersiz JSON gövdesi: " + err.Error())
	}
	return nil
}

// IDParam yol parametresindeki pozitif tamsayı kimliği okur.
func IDParam(r *http.Request, name string) (int64, error) {
	id, err := strconv.ParseInt(chi.URLParam(r, name), 10, 64)
	if err != nil || id <= 0 {
		return 0, BadRequest("geçersiz kimlik: " + name)
	}
	return id, nil
}

// Page keyset sayfalama parametreleri: ?limit=50&cursor=<son id>.
type Page struct {
	Limit  int
	Cursor int64
}

// PageParams sorgu parametrelerinden sayfa bilgisini okur.
func PageParams(r *http.Request, def, max int) (Page, error) {
	p := Page{Limit: def}
	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > max {
			return p, BadRequest(fmt.Sprintf("limit 1..%d olmalı", max))
		}
		p.Limit = n
	}
	if v := r.URL.Query().Get("cursor"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 0 {
			return p, BadRequest("geçersiz cursor")
		}
		p.Cursor = n
	}
	return p, nil
}

// List keyset sayfalı liste yanıtı.
type List[T any] struct {
	Items      []T    `json:"items"`
	NextCursor *int64 `json:"next_cursor"`
}

// ---- bağlam anahtarları ----

type ctxKey int

const (
	keyLogger ctxKey = iota
	keyClaims
	keyRequestID
	keyUserSlot
)

// Logger bağlamdaki istek loglayıcısını döner.
func Logger(ctx context.Context) *slog.Logger {
	if l, ok := ctx.Value(keyLogger).(*slog.Logger); ok {
		return l
	}
	return slog.Default()
}

// ClaimsFrom kimliği doğrulanmış kullanıcıyı döner.
func ClaimsFrom(ctx context.Context) *auth.Claims {
	c, _ := ctx.Value(keyClaims).(*auth.Claims)
	return c
}

// WithClaims test ve iç kullanım için kimlik ekler.
func WithClaims(ctx context.Context, c *auth.Claims) context.Context {
	return context.WithValue(ctx, keyClaims, c)
}

// RequestID bağlamdaki istek kimliği.
func RequestID(ctx context.Context) string {
	s, _ := ctx.Value(keyRequestID).(string)
	return s
}

// ---- ara katmanlar ----

type statusRecorder struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (s *statusRecorder) WriteHeader(code int) { s.status = code; s.ResponseWriter.WriteHeader(code) }
func (s *statusRecorder) Write(b []byte) (int, error) {
	if s.status == 0 {
		s.status = http.StatusOK
	}
	n, err := s.ResponseWriter.Write(b)
	s.bytes += n
	return n, err
}

// Observe istek kimliği, yapılandırılmış log, metrik ve panik kurtarmayı birleştirir.
// Parola ve belirteç içeren başlıklar/gövdeler loglanmaz; yalnızca yöntem, rota, durum, süre.
func Observe(base *slog.Logger, reg *metrics.Registry) func(http.Handler) http.Handler {
	reg.Counter("http_requests_total", "Toplam HTTP isteği", "method", "route", "code")
	reg.Histogram("http_request_duration_seconds", "HTTP istek süresi",
		[]float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5}, "method", "route")
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rid := r.Header.Get("X-Request-ID")
			if rid == "" || len(rid) > 64 {
				b := make([]byte, 8)
				_, _ = rand.Read(b)
				rid = hex.EncodeToString(b)
			}
			w.Header().Set("X-Request-ID", rid)
			log := base.With("request_id", rid)
			ctx := context.WithValue(r.Context(), keyLogger, log)
			ctx = context.WithValue(ctx, keyRequestID, rid)
			uidSlot := new(int64) // Authenticate doldurur; log satırında kullanıcı görünsün
			ctx = context.WithValue(ctx, keyUserSlot, uidSlot)
			rec := &statusRecorder{ResponseWriter: w}
			defer func() {
				if p := recover(); p != nil {
					log.Error("panik", "panic", fmt.Sprint(p), "stack", string(debug.Stack()))
					if rec.status == 0 {
						WriteJSON(rec, 500, map[string]any{"error": E(500, "internal", "beklenmeyen bir hata oluştu")})
					}
				}
				route := chi.RouteContext(ctx).RoutePattern()
				if route == "" {
					route = "unmatched"
				}
				dur := time.Since(start)
				reg.Inc("http_requests_total", r.Method, route, strconv.Itoa(rec.status))
				reg.Observe("http_request_duration_seconds", dur.Seconds(), r.Method, route)
				lvl := slog.LevelInfo
				if rec.status >= 500 {
					lvl = slog.LevelError
				}
				uid := *uidSlot
				if route != "/health" && route != "/metrics" {
					log.Log(ctx, lvl, "http", "method", r.Method, "route", route, "status", rec.status,
						"duration_ms", dur.Milliseconds(), "bytes", rec.bytes, "user_id", uid)
				}
			}()
			next.ServeHTTP(rec, r.WithContext(ctx))
		})
	}
}

// Timeout her isteğe bağlam zaman aşımı ekler.
func Timeout(d time.Duration) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx, cancel := context.WithTimeout(r.Context(), d)
			defer cancel()
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// CORS geliştirme sırasında ön yüzün doğrudan API'ye erişebilmesi için.
func CORS(origin string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if o := r.Header.Get("Origin"); o != "" && o == origin {
				w.Header().Set("Access-Control-Allow-Origin", o)
				w.Header().Set("Vary", "Origin")
				w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, Idempotency-Key, X-Access-Reason")
				w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
			}
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// Authenticate Bearer belirtecini doğrular. Belirteç yoksa ya da geçersizse 401.
func Authenticate(iss *auth.Issuer) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := r.Header.Get("Authorization")
			tok, ok := strings.CutPrefix(h, "Bearer ")
			if !ok || tok == "" {
				WriteError(w, r, Unauthorized("kimlik doğrulama gerekli"))
				return
			}
			c, err := iss.Parse(tok)
			if err != nil {
				WriteError(w, r, Unauthorized(err.Error()))
				return
			}
			if slot, ok := r.Context().Value(keyUserSlot).(*int64); ok {
				*slot = c.UserID
			}
			next.ServeHTTP(w, r.WithContext(WithClaims(r.Context(), c)))
		})
	}
}

// Require rol tabanlı yetkilendirme: kullanıcının rolü listede değilse 403.
func Require(roles ...string) func(http.Handler) http.Handler {
	allowed := map[string]bool{}
	for _, r := range roles {
		allowed[r] = true
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			c := ClaimsFrom(r.Context())
			if c == nil {
				WriteError(w, r, Unauthorized("kimlik doğrulama gerekli"))
				return
			}
			if !allowed[c.Role] {
				WriteError(w, r, Forbidden("bu işlem için yetkiniz yok"))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
