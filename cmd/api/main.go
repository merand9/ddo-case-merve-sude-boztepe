// Komut api DamarBağış HTTP API sunucusudur: migrasyonları uygular, demo hesaplarını
// oluşturur, arka plan görevlerini başlatır ve düzgün kapanışı yönetir.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/msboztepe/damarbagis/internal/api"
	"github.com/msboztepe/damarbagis/internal/app"
	"github.com/msboztepe/damarbagis/internal/config"
	"github.com/msboztepe/damarbagis/internal/db"
	"github.com/msboztepe/damarbagis/internal/metrics"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "healthcheck" {
		os.Exit(healthcheck())
	}
	cfg, err := config.Load()
	log := newLogger(cfg.LogLevel)
	if err != nil {
		log.Error("yapılandırma hatası", "err", err.Error())
		os.Exit(1)
	}
	if err := run(cfg, log); err != nil {
		log.Error("sunucu durdu", "err", err.Error())
		os.Exit(1)
	}
}

func run(cfg config.Config, log *slog.Logger) error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	pool, err := db.Connect(ctx, cfg.DatabaseURL, cfg.DBMaxConns, 60*time.Second)
	if err != nil {
		return err
	}
	defer pool.Close()
	if err := db.Migrate(ctx, pool, log); err != nil {
		return err
	}
	a, err := app.New(pool, cfg, log)
	if err != nil {
		return err
	}
	if err := a.EnsureDemoData(ctx); err != nil {
		return err
	}

	srv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           (&api.Server{App: a, Metrics: metrics.New(), Log: log}).Router(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	jobsCtx, cancelJobs := context.WithCancel(context.Background())
	jobsDone := make(chan struct{})
	go func() { a.RunJobs(jobsCtx); close(jobsDone) }()

	errCh := make(chan error, 1)
	go func() {
		log.Info("API dinliyor", "addr", cfg.HTTPAddr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
		close(errCh)
	}()

	select {
	case <-ctx.Done():
		log.Info("kapanış sinyali alındı; açık istekler tamamlanıyor")
	case err := <-errCh:
		if err != nil {
			cancelJobs()
			return err
		}
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()
	err = srv.Shutdown(shutdownCtx)
	cancelJobs()
	<-jobsDone
	log.Info("düzgün kapanış tamamlandı")
	return err
}

func newLogger(level string) *slog.Logger {
	var l slog.Level
	_ = l.UnmarshalText([]byte(level))
	h := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: l})
	log := slog.New(h).With("service", "api")
	slog.SetDefault(log)
	return log
}

// healthcheck konteyner HEALTHCHECK'i için: imajda curl/wget olmadığından ikili kendi /health'ine istek atar.
func healthcheck() int {
	addr := os.Getenv("HTTP_ADDR")
	if addr == "" || addr[0] == ':' {
		addr = "127.0.0.1" + addr
		if addr == "127.0.0.1" {
			addr = "127.0.0.1:8080"
		}
	}
	c := http.Client{Timeout: 3 * time.Second}
	resp, err := c.Get("http://" + addr + "/health")
	if err != nil {
		return 1
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 1
	}
	return 0
}
