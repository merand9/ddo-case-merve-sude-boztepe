// Komut loadgen k6 betiğiyle (loadtest/load.js) aynı karışımı üreten basit bir yük aracıdır.
// k6'nın kurulu olmadığı ortamlarda ölçüm almak için: go run ./cmd/loadgen -duration 60s
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"math/rand/v2"
	"net/http"
	"os"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

func main() {
	base := flag.String("base", "http://localhost:8080", "API adresi")
	dur := flag.Duration("duration", 60*time.Second, "süre")
	vus := flag.Int("vus", 20, "okuma yapan eşzamanlı kullanıcı")
	rate := flag.Int("rate", 10, "saniyede hastane talebi")
	flag.Parse()
	c := &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{MaxIdleConnsPerHost: 200}}
	login := func(email string) string {
		b, _ := json.Marshal(map[string]string{"email": email, "password": "Demo1234!"})
		r, err := c.Post(*base+"/auth/login", "application/json", bytes.NewReader(b))
		if err != nil {
			fmt.Println(err)
			os.Exit(1)
		}
		defer r.Body.Close()
		var t struct {
			AccessToken string `json:"access_token"`
		}
		_ = json.NewDecoder(r.Body).Decode(&t)
		return t.AccessToken
	}
	bank, hosp, nurse := login("bankasorumlusu@damarbagis.local"), login("hastane@damarbagis.local"), login("hemsire@damarbagis.local")

	type sample struct {
		d   time.Duration
		ok  bool
		key string
	}
	var mu sync.Mutex
	var samples []sample
	var total atomic.Int64
	do := func(key, method, url, tok string, body []byte, want int) {
		req, _ := http.NewRequest(method, url, bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+tok)
		req.Header.Set("Content-Type", "application/json")
		if method == "POST" {
			req.Header.Set("Idempotency-Key", fmt.Sprintf("lg-%d-%d", time.Now().UnixNano(), rand.IntN(1e9)))
		}
		t0 := time.Now()
		resp, err := c.Do(req)
		ok := err == nil && resp.StatusCode == want
		if resp != nil {
			resp.Body.Close()
		}
		mu.Lock()
		samples = append(samples, sample{time.Since(t0), ok, key})
		mu.Unlock()
		total.Add(1)
	}
	end := time.Now().Add(*dur)
	var wg sync.WaitGroup
	for i := 0; i < *vus; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for time.Now().Before(end) {
				do("read", "GET", *base+"/api/v1/stock", bank, nil, 200)
				do("read", "GET", *base+"/api/v1/products?status=available&component=RBC&sort=expiry&limit=50", bank, nil, 200)
				do("read", "GET", *base+"/api/v1/donors?q=yil&limit=25", nurse, nil, 200)
				do("read", "GET", *base+"/api/v1/requests?status=open,partial,reserved&limit=50", bank, nil, 200)
				time.Sleep(200 * time.Millisecond)
			}
		}()
	}
	groups := []string{"A+", "A+", "O+", "B+", "AB+", "A-", "O-"}
	tick := time.NewTicker(time.Second / time.Duration(*rate))
	n := 0
	for time.Now().Before(end) {
		<-tick.C
		n++
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			b, _ := json.Marshal(map[string]any{"component": "RBC", "group": groups[rand.IntN(len(groups))], "units": 1, "urgency": "routine", "patient_ref": fmt.Sprintf("LG-%d", n)})
			do("write", "POST", *base+"/api/v1/requests", hosp, b, 201)
		}(n)
	}
	wg.Wait()
	fmt.Printf("| Senaryo | İstek | p50 | p95 | p99 | Hata oranı | RPS |\n|---|---|---|---|---|---|---|\n")
	for _, k := range []string{"read", "write", "all"} {
		var ds []time.Duration
		fail := 0
		for _, s := range samples {
			if k != "all" && s.key != k {
				continue
			}
			ds = append(ds, s.d)
			if !s.ok {
				fail++
			}
		}
		sort.Slice(ds, func(i, j int) bool { return ds[i] < ds[j] })
		p := func(q float64) string {
			if len(ds) == 0 {
				return "-"
			}
			return fmt.Sprintf("%.1f ms", float64(ds[int(q*float64(len(ds)-1))].Microseconds())/1000)
		}
		name := map[string]string{"read": "Personel okuma (stok, ürün, bağışçı arama, talepler)", "write": "Hastane talebi + anında tahsis (yazma)", "all": "Toplam"}[k]
		fmt.Printf("| %s | %d | %s | %s | %s | %.2f%% | %.1f |\n", name, len(ds), p(.5), p(.95), p(.99), 100*float64(fail)/float64(max(len(ds), 1)), float64(len(ds))/dur.Seconds())
	}
}
