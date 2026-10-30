package app

import (
	"context"
	"time"
)

const jobsLockID = 727274002

// RunJobs arka plan görevlerini periyodik çalıştırır: süresi dolan rezervasyonları bırakma,
// SKT'si geçen ürünleri expired yapma, açık talepleri aciliyet sırasıyla tahsis etme ve
// (dakikada bir) stok alarmlarını değerlendirme. Birden çok API örneği çalışsa da aynı anda
// yalnızca biri çalıştırsın diye PostgreSQL advisory lock kullanılır.
func (a *App) RunJobs(ctx context.Context) {
	t := time.NewTicker(a.Cfg.JobInterval)
	defer t.Stop()
	lastAlerts := time.Time{}
	lastPrune := time.Time{}
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		a.tick(ctx, &lastAlerts, &lastPrune)
	}
}

func (a *App) tick(ctx context.Context, lastAlerts, lastPrune *time.Time) {
	tctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	conn, err := a.DB.Acquire(tctx)
	if err != nil {
		a.Log.Warn("görev: bağlantı alınamadı", "err", err.Error())
		return
	}
	defer conn.Release()
	var got bool
	if err := conn.QueryRow(tctx, `SELECT pg_try_advisory_lock($1)`, jobsLockID).Scan(&got); err != nil || !got {
		return
	}
	defer conn.Exec(context.Background(), `SELECT pg_advisory_unlock($1)`, jobsLockID) //nolint:errcheck

	if n, err := a.ReleaseExpired(tctx); err != nil {
		a.Log.Warn("görev: rezervasyon bırakma", "err", err.Error())
	} else if n > 0 {
		a.Log.Info("süresi dolan rezervasyonlar bırakıldı", "count", n)
	}
	if n, err := a.ExpireProducts(tctx); err != nil {
		a.Log.Warn("görev: SKT", "err", err.Error())
	} else if n > 0 {
		a.Log.Info("SKT geçen ürünler expired yapıldı", "count", n)
	}
	if _, err := a.ProcessQueue(tctx); err != nil {
		a.Log.Warn("görev: talep kuyruğu", "err", err.Error())
	}
	if time.Since(*lastAlerts) > time.Minute {
		*lastAlerts = time.Now()
		if err := a.EvaluateAlerts(tctx); err != nil {
			a.Log.Warn("görev: alarm", "err", err.Error())
		}
	}
	if time.Since(*lastPrune) > time.Hour {
		*lastPrune = time.Now()
		_ = a.PruneIdempotency(tctx)
	}
}
