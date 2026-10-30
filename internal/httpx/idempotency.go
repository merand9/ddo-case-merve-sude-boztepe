package httpx

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"net/http"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Idempotency durum değiştiren POST isteklerinde "Idempotency-Key" başlığını uygular.
//
//   - Aynı kullanıcı + anahtar + aynı gövde: ilk yanıt (durum kodu ve gövde) aynen tekrar döner.
//   - Aynı anahtar farklı gövdeyle: 422 idempotency_key_reused.
//   - İlk istek hâlâ işleniyorsa: 409 idempotency_in_progress.
//   - 5xx yanıtlar saklanmaz; istemci aynı anahtarla yeniden deneyebilir.
//
// Anahtar satırı INSERT ... ON CONFLICT DO NOTHING ile atomik olarak sahiplenilir; böylece
// eşzamanlı iki tekrar isteğinden yalnızca biri işlenir.
func Idempotency(pool *pgxpool.Pool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := r.Header.Get("Idempotency-Key")
			if r.Method != http.MethodPost || key == "" {
				next.ServeHTTP(w, r)
				return
			}
			c := ClaimsFrom(r.Context())
			if c == nil {
				next.ServeHTTP(w, r)
				return
			}
			if len(key) < 8 || len(key) > 128 {
				WriteError(w, r, BadRequest("Idempotency-Key 8-128 karakter olmalı"))
				return
			}
			body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
			if err != nil {
				WriteError(w, r, BadRequest("gövde okunamadı"))
				return
			}
			r.Body = io.NopCloser(bytes.NewReader(body))
			h := sha256.New()
			h.Write([]byte(r.Method + " " + r.URL.Path + "\n"))
			h.Write(body)
			hash := h.Sum(nil)

			ctx := r.Context()
			tag, err := pool.Exec(ctx, `INSERT INTO idempotency_keys (user_id, key, method, path, request_hash)
				VALUES ($1,$2,$3,$4,$5) ON CONFLICT DO NOTHING`, c.UserID, key, r.Method, r.URL.Path, hash)
			if err != nil {
				WriteError(w, r, err)
				return
			}
			if tag.RowsAffected() == 0 {
				replay(w, r, pool, c.UserID, key, hash)
				return
			}

			rec := &bufferedWriter{header: http.Header{}}
			next.ServeHTTP(rec, r)
			status := rec.status
			if status == 0 {
				status = http.StatusOK
			}
			bg := context.WithoutCancel(ctx)
			if status >= 500 {
				_, _ = pool.Exec(bg, `DELETE FROM idempotency_keys WHERE user_id=$1 AND key=$2`, c.UserID, key)
			} else {
				_, _ = pool.Exec(bg, `UPDATE idempotency_keys SET status_code=$3, response=$4 WHERE user_id=$1 AND key=$2`,
					c.UserID, key, status, rec.buf.Bytes())
			}
			for k, v := range rec.header {
				w.Header()[k] = v
			}
			w.WriteHeader(status)
			_, _ = w.Write(rec.buf.Bytes())
		})
	}
}

func replay(w http.ResponseWriter, r *http.Request, pool *pgxpool.Pool, uid int64, key string, hash []byte) {
	var storedHash, resp []byte
	var status *int
	err := pool.QueryRow(r.Context(), `SELECT request_hash, status_code, response FROM idempotency_keys
		WHERE user_id=$1 AND key=$2`, uid, key).Scan(&storedHash, &status, &resp)
	if errors.Is(err, pgx.ErrNoRows) {
		WriteError(w, r, E(http.StatusConflict, "idempotency_retry", "anahtar serbest bırakıldı, yeniden deneyin"))
		return
	}
	if err != nil {
		WriteError(w, r, err)
		return
	}
	if !bytes.Equal(storedHash, hash) {
		WriteError(w, r, E(http.StatusUnprocessableEntity, "idempotency_key_reused",
			"bu Idempotency-Key farklı bir istekle kullanılmış"))
		return
	}
	if status == nil {
		WriteError(w, r, E(http.StatusConflict, "idempotency_in_progress", "aynı anahtarlı istek hâlâ işleniyor"))
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Idempotent-Replayed", "true")
	w.WriteHeader(*status)
	_, _ = w.Write(resp)
}

type bufferedWriter struct {
	header http.Header
	status int
	buf    bytes.Buffer
}

func (b *bufferedWriter) Header() http.Header { return b.header }
func (b *bufferedWriter) WriteHeader(s int) {
	if b.status == 0 {
		b.status = s
	}
}
func (b *bufferedWriter) Write(p []byte) (int, error) {
	if b.status == 0 {
		b.status = http.StatusOK
	}
	return b.buf.Write(p)
}
