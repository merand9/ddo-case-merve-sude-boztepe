package httpx

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/msboztepe/damarbagis/internal/auth"
)

func TestFromErrMapsDatabaseErrors(t *testing.T) {
	cases := map[string]int{"23505": 409, "23514": 422, "P0010": 409, "P0013": 403, "40001": 409, "23P01": 409, "23503": 422}
	for code, want := range cases {
		if got := FromErr(&pgconn.PgError{Code: code}); got.Status != want {
			t.Errorf("%s → %d, beklenen %d", code, got.Status, want)
		}
	}
	if FromErr(pgx.ErrNoRows).Status != 404 || FromErr(errors.New("x")).Status != 500 {
		t.Error("genel hata eşlemesi hatalı")
	}
	if FromErr(context.DeadlineExceeded).Status != 504 {
		t.Error("zaman aşımı 504 olmalı")
	}
	if FromErr(Validation("v")).Code != "validation_failed" {
		t.Error("API hatası korunmalı")
	}
}

func TestErrorFormat(t *testing.T) {
	rr := httptest.NewRecorder()
	H(func(w http.ResponseWriter, r *http.Request) error { return Conflict("çakışma") })(rr, httptest.NewRequest("GET", "/", nil))
	if rr.Code != 409 || !strings.Contains(rr.Body.String(), `"error":{"code":"conflict","message":"çakışma"}`) {
		t.Fatalf("hata biçimi hatalı: %d %s", rr.Code, rr.Body.String())
	}
}

func TestAuthenticateAndRequire(t *testing.T) {
	iss := auth.NewIssuer([]byte(strings.Repeat("s", 32)), time.Minute)
	ok := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })
	h := Authenticate(iss)(Require("bank", "admin")(ok))

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("GET", "/", nil))
	if rr.Code != 401 {
		t.Fatalf("belirteçsiz 401 olmalı: %d", rr.Code)
	}
	tok, _, _ := iss.Issue(auth.Claims{UserID: 1, Role: "hospital"})
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != 403 {
		t.Fatalf("yetkisiz rol 403 olmalı: %d", rr.Code)
	}
	tok, _, _ = iss.Issue(auth.Claims{UserID: 1, Role: "bank"})
	req.Header.Set("Authorization", "Bearer "+tok)
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != 204 {
		t.Fatalf("yetkili rol geçmeli: %d", rr.Code)
	}
	req.Header.Set("Authorization", "Bearer bozuk")
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != 401 {
		t.Fatalf("bozuk belirteç 401 olmalı: %d", rr.Code)
	}
}

func TestPageParams(t *testing.T) {
	p, err := PageParams(httptest.NewRequest("GET", "/?limit=10&cursor=55", nil), 50, 200)
	if err != nil || p.Limit != 10 || p.Cursor != 55 {
		t.Fatalf("sayfa parametreleri hatalı: %+v %v", p, err)
	}
	for _, q := range []string{"?limit=0", "?limit=999", "?cursor=-1", "?cursor=x"} {
		if _, err := PageParams(httptest.NewRequest("GET", "/"+q, nil), 50, 200); err == nil {
			t.Errorf("%s reddedilmeli", q)
		}
	}
}

func TestDecodeRejectsUnknownFields(t *testing.T) {
	var v struct{ A int }
	r := httptest.NewRequest("POST", "/", strings.NewReader(`{"A":1,"B":2}`))
	if err := Decode(r, &v); err == nil {
		t.Fatal("bilinmeyen alan reddedilmeli")
	}
}

func TestConnectErrorIs503(t *testing.T) {
	if FromErr(&pgconn.ConnectError{}).Status != 503 {
		t.Fatal("bağlantı hatası 503 olmalı")
	}
}
