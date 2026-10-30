# TESTING — Hızlı Doğrulama Rehberi

## Tek komutla doğrulama

```bash
cp .env.example .env
make verify          # docker compose up --build --wait + docker compose --profile verify run --rm verify
```

`verify` servisi, ayağa kalkmış ve seed edilmiş sistem üzerinde aşağıdaki her kimliği API üzerinden uçtan uca dener. Bütünlük kontrolleri için veritabanına da bağlanır. Sonunda `PASS`/`FAIL` tablosu basar; bir kontrol bile başarısızsa çıkış kodu `1` olur. Yük testi bu komutun dışındadır (`make load`).

Doğrulamayı tekrarlamak için: `make reset-demo && make verify`. `reset-demo` veritabanı hacmini silip sistemi aynı tohumla (`SIM_SEED=42`) yeniden seed eder.

## (a) Demo hesapları

Tüm hesapların parolası: **`Demo1234!`** (`.env` → `DEMO_PASSWORD`)

| Rol | E-posta | Not |
|---|---|---|
| Yönetici | `admin@damarbagis.local` | Kullanıcılar, kural sürümleri, denetim kaydı |
| Hemşire | `hemsire@damarbagis.local` | Bağışçı kaydı, uygunluk, randevu, bağış |
| Laboratuvar uzmanı | `lab@damarbagis.local` | Test, üretim, değişiklik talebi (1. onay) |
| Laboratuvar uzmanı (2. onay) | `lab2@damarbagis.local` | Çift onay için ikinci kişi |
| Kan bankası sorumlusu | `bankasorumlusu@damarbagis.local` | Stok, tahsis, dağıtım, tahmin, izleme |
| Hastane | `hastane@damarbagis.local` | Bandırma Devlet Hastanesi (hospital_id=1) |
| Hastane (ikinci) | `hastane2@damarbagis.local` | Bursa Şehir Hastanesi (izolasyon testi) |
| Bağışçı | `bagisci@damarbagis.local` | Kendi profili, uygunluk, randevu |

## (b) Doğrulama kimlikleri

| Kimlik | Nasıl denenir | Beklenen sonuç | Kanıt komutu |
|---|---|---|---|
| `HEALTH` | `curl localhost:8080/health` | `200 {"status":"ok","checks":{"database":{"status":"ok"}}}`; DB kapalıyken `503` | `make verify`, `make chaos` |
| `AUTH-1` | Yanlış parolayla `POST /auth/login` | `401 invalid_credentials` | `make verify` |
| `AUTH-2` | Belirteçsiz `GET /api/v1/stock` | `401 unauthorized` | `make verify` |
| `AUTH-3` | Hastane → `/api/v1/donors`, bağışçı → `/api/v1/stock`, hemşire → `POST /requests` … | Hepsi `403 forbidden` | `make verify` |
| `AUTH-4` | Refresh token'ı iki kez kullan | 1. kullanım `200` (yeni çift), 2. kullanım `401`; tüm oturumlar kapatılır | `make verify` |
| `F-1` | Hemşire: `POST /donors`, `POST /donors/{id}/eligibility` | Kayıt `201`; uygunluk kararı gerekçe kodlarıyla (`HB_TOO_LOW`, `WEIGHT_TOO_LOW` …) | `make verify` |
| `F-2` | `POST /appointments` → `POST /donations` (aynı `Idempotency-Key` ile iki kez) → yeni bağış | Randevu `201`, ikinci açık randevu `409`; bağış tekrarı aynı kimlik + `Idempotent-Replayed: true`; ikinci bağış `422 donor_ineligible / INTERVAL_NOT_ELAPSED` | `make verify` |
| `F-3` | Lab: negatif sonuç, başka bağışa pozitif sonuç | Negatif → 3 ürün `available`; pozitif → 3 ürün `quarantined`, bağışçı `PERMANENTLY_DEFERRED`; ikinci sonuç girişi `409` | `make verify`, `go test ./internal/app -run Positive` |
| `F-4` | Lab: `POST /donations/{id}/components` | RBC (42 g), PLT (5 g), FFP (365 g); SKT toplama anından; tekrar üretim `409` | `make verify`, `go test ./internal/domain/product` |
| `F-5` | `GET /stock`, `GET /products?status=available&component=RBC&group=A%2B&limit=5` → `cursor` | 24 hücrelik matris; filtre doğru; keyset sayfa 2 daha küçük kimliklerle; `sort=expiry` SKT artan | `make verify` |
| `F-6` | Hastane: `POST /requests` (A+ RBC 2 ünite) | `201`, durum `reserved`, 2 rezervasyon; yanıtta `donor`/`donation_id` alanı yok; diğer hastane `404` | `make verify`, `go test ./internal/app -run HospitalIsolation` |
| `F-7` | Banka: `POST /requests/{id}/issue`; hastane: `POST /transfusions`; `GET /reports/summary` | Talep `issued`; transfüzyon `201`; başka hastanenin ürünü `404`; raporda dağıtım | `make verify` |
| `T-1` | Uygunluk kuralları (aşağıya bkz.) | 100. gün: kadın red / erkek uygun; aferez 10. gün red / 14. gün uygun; seyahat tarihli red; `iv_drug_use` kalıcı red (tarihsiz); ileri tarihli kural sürümü yalnızca o tarihten sonra uygulanır | `go test ./internal/domain/eligibility -v`, `make verify` |
| `T-2` | Bağış → üretim → negatif → RBC'yi böl → çift onaylı düzeltme ile pozitif; hasta → bağışçı → hasta izleme | 2 alt ürün (depth=1) recursive bulunup karantinaya alınır; look-back önceki bağışları da karantinaya alır; izleme iki yönde çalışır | `go test ./internal/app -run 'Positive|Trace' -v`, `make verify` |
| `T-3` | `GET /compat`, talep, 15 paralel talep, 2 sn TTL, çok acil talep | Uyum sırası `A+>A->O+>O-`; tam uyum + en erken SKT; hiçbir ürün iki aktif rezervasyonda değil; TTL dolunca `released`; çok acil talep rutin rezervasyonu devralır | `go test ./internal/app -run 'Concurrent|Emergency|Expiry|FEFO' -race -v`, `make verify` |
| `T-4` | `GET /forecast`, `GET /forecast/backtest`, `POST /alerts/evaluate`, `GET /call-list` | 8 grup × 7 gün tahmin, yeterlilik (gün), SKT kaybı; backtest MAPE ≈ %10; kritik grup alarmı; çağrı listesindeki bağışçılar uygunluk motorundan da geçer | `go test ./internal/domain/forecast -v`, `make verify` |
| `T-5` | Sağlık verisi (gerekçesiz/gerekçeli), denetim kaydı, çift onay, geçersiz geçiş, append-only | DB'de düz metin yok; gerekçesiz `400`; gerekçeli okuma denetimde; kendi talebini onaylama `403`; `transfused→available` `409`; `product_events`/`audit_log` UPDATE/DELETE reddedilir | `go test ./internal/app -run 'Health|Dual|AppendOnly' -v`, `make verify` |
| `IDEM` | Aynı anahtar + farklı gövde | `422 idempotency_key_reused` | `make verify` |
| `OBS` | `GET /metrics`, `GET /openapi.yaml` | Prometheus metin biçimi; geçerli OpenAPI 3 | `make verify` |
| `SIM` | Simülatör | ≥30.000 bağışçı, tek seed kaydı + parmak izi; son 2 dakikada canlı olay; reddedilen uygunsuz denemeler | `make verify`, `go test ./internal/sim -run SameSeed` |
| `WEB` | `http://localhost:3000` | React paketi sunulur, derin bağlantı (`/forecast`) çalışır, `/health` nginx üzerinden API'ye vekil edilir | `make verify`, `cd web && npm test` |

### Örnek `make verify` çıktısı (yerel çalıştırma)

```
| Kimlik | Sonuç | Süre | Özellik | Not |
|---|---|---|---|---|
| HEALTH | PASS | 0ms | GET /health (veritabanı dahil) | status=ok, db=ok, şema=0003_reference_data |
| AUTH-1 | PASS | 34ms | Yanlış parola → 401 | 401 invalid_credentials |
| AUTH-2 | PASS | 0ms | Belirteçsiz istek → 401 | 401 unauthorized |
| AUTH-3 | PASS | 2ms | Yetkisiz rol → 403 (sunucu tarafı RBAC) | 7 yetkisiz rol/uç nokta denemesi 403 döndü |
| AUTH-4 | PASS | 40ms | Refresh token rotasyonu ve tekrar kullanım tespiti | yeni belirteç alındı, eski belirteç reddedildi |
| F-1 | PASS | … | Bağışçı kaydı ve uygunluk kontrolü | uygun=evet; düşük Hb+kilo → 2 gerekçe |
| F-2 | PASS | … | Randevu ve bağış kaydı (+ idempotans) | tekrar istek aynı kaydı döndü; 2. bağış 422 INTERVAL_NOT_ELAPSED |
| F-3 | PASS | … | Test sonuçları ve karantina | negatif → 3 ürün stokta; pozitif → 3 ürün karantina, bağışçı kalıcı red |
| F-4 | PASS | … | Bileşen üretimi | RBC(42g) + PLT(5g) + FFP(365g), pending_test; tekrar üretim 409 |
| F-5 | PASS | … | Stok | 24 hücre; filtre, keyset ve SKT sıralaması doğru |
| F-6 | PASS | … | Hastane talepleri ve rezervasyon | 2/2 rezerve, bağışçı alanı yok, diğer hastane 404 |
| F-7 | PASS | … | Dağıtım, transfüzyon ve raporlar | dağıtıldı, transfüze edildi; raporda RBC dağıtımı |
| T-1 | PASS | … | Uygunluk kuralları | 100. gün: K red (120g) / E uygun (90g); aferez 10.gün red / 14.gün uygun; … |
| T-2 | PASS | … | Geri izlenebilirlik | bölünmüş 2 alt ürün (depth=1) karantinada; hasta → bağışçı → hasta |
| T-3 | PASS | … | Uyum ve tahsis | A+>A->O+>O-; FEFO; 15 eşzamanlı talep, çift ürün yok; 2 sn TTL serbest kaldı |
| T-4 | PASS | … | Tahmin ve çağrı listesi | backtest MAPE=10.07% WAPE=9.32% |
| T-5 | PASS | … | Mahremiyet ve bütünlük | DB'de şifreli; gerekçesiz 400; kendi talebini onaylama 403; transfused→available 409 |
| IDEM | PASS | … | Idempotency-Key | farklı gövde 422 idempotency_key_reused |
| OBS | PASS | … | /metrics ve OpenAPI | |
| SIM | PASS | … | Simülatör | 30000+ bağışçı, seed parmak izi 9545aedf157d5a50 |
| WEB | PASS | … | Ön yüz ve API vekili | |

Toplam: 21, başarılı: 21, başarısız: 0
```

## (c) Arayüzde elle denenecek kısa senaryolar

`http://localhost:3000` açın. Giriş ekranındaki demo hesaplarına tıklamak formu doldurur.

1. **Canlı pano** (`bankasorumlusu`): Sayaçlar ve ürün olay akışı 5 saniyede bir değişir; simülatör çalışıyor. Stok matrisinde kırmızı hücre kritik stoğu gösterir.
2. **Bağışçı kaydı ve uygunluk** (`hemsire`): *Bağışçılar → Yeni bağışçı kaydı* formunu doldurun. Açılan bağışçı ekranında Hb'yi `11.5` yapıp *Kontrol et*'e basın: gerekçeli red görünür. Hb'yi `13.5` yapıp *Bağışı kaydet*'e basın; ikinci kez basınca bekleme süresi reddi ve en erken uygun tarih görünür.
3. **Sağlık verisi** (`hemsire`): Bağışçı ekranında gerekçe yazmadan *Görüntüle* reddedilir. Gerekçe yazınca veri görünür. `admin` → *Yönetim → Denetim kaydı* ekranında `donor.health.read` filtresiyle bu erişim gerekçesiyle listelenir.
4. **Laboratuvar** (`lab`): *Bileşen üretimi bekleyenler* listesinden *Üret*. *Test bekleyenler* listesinden *Pozitif… → hbv* seçin; ürünlerin karantinaya alındığı bildirilir. Alttaki formdan bir düzeltme talebi açın. Aynı hesapla *Onayla* 403 verir; `lab2` ile girip onaylayın.
5. **Hastane talebi** (`hastane`): *Talepler → Yeni talep*: A+ eritrosit, 2 ünite. Rezerve edilen ürün numaraları ve "alternatif" rozetleri görünür; bağışçı bilgisi yoktur. *Çok acil* talep anında karşılanır.
6. **Dağıtım** (`bankasorumlusu`): *Talepler → Dağıt*. Ardından `hastane` → *Ürünlerimiz → Transfüzyon*.
7. **Tahmin** (`bankasorumlusu`): *Tahmin ve çağrı*: 7 günlük tahmin, yeterlilik, MAPE/WAPE, açık alarmlar. *Çağrı listesi → Listele* alarmdaki grubun uygun bağışçılarını gösterir.
8. **Geri izleme** (`bankasorumlusu`): 6. adımdaki hasta referansıyla *Hasta → bağışçı*; çıkan bağışçı id'si ile *Bağışçı → hasta*.
9. **Stok** (`bankasorumlusu`): Bir eritrositi *Böl*; alt ürünler "(alt)" olarak listelenir. Ürün satırındaki barkodu kopyalayıp *İmzalı barkod doğrulama*'ya yapıştırın; bir karakteri değiştirince imza geçersiz olur.
10. **Bağışçı** (`bagisci`): Uygunluk durumu, randevu alma ve randevu hatırlatma kartı.
11. **Mobil**: Tarayıcıyı 390 px genişliğe daraltın; menü yatay kayar, tablolar yatay kaydırılır.
12. **Derin bağlantı ve oturum**: `http://localhost:3000/forecast` adresini doğrudan açın. Oturum yoksa giriş ekranına, giriş sonrası tekrar tahmin sayfasına dönülür. Yetkisiz rolle açılırsa rolün ana ekranına yönlendirilir.

## (d) Zorlu görev kanıt komutları

| Görev | Komut | Ne kanıtlar |
|---|---|---|
| T-1 | `go test ./internal/domain/eligibility -v -cover` | 20 test: 90/120/14 gün, yaş/kilo/Hb sınırları, süreli/kalıcı erteleme, türe özgü erteleme, çoklu gerekçe, zaman geçerlilikli kural defteri; kapsam %95.9 |
| T-1 | `TEST_DATABASE_URL=… go test ./internal/app -run RuleVersion -v` | İleri tarihli kural sürümü DB üzerinden; geçmiş tarihli sürüm reddedilir |
| T-2 | `TEST_DATABASE_URL=… go test ./internal/app -run 'PositiveTest|Trace' -v` | Recursive karantina (depth=1 alt ürünler), look-back, hasta↔bağışçı izleme |
| T-3 | `TEST_DATABASE_URL=… go test ./internal/app -run 'Concurrent|Unique|Emergency|Expiry|FEFO' -race -v` | 20 eşzamanlı tahsis / 3 ürün → 3 rezervasyon; DB benzersiz dizini; acil öncelik; TTL; FEFO |
| T-3 | `go test ./internal/domain/compat -v` | 8×8 ABO/Rh matrisi, plazma ters uyum, aday sıralaması |
| T-4 | `go test ./internal/domain/forecast -v` ve `curl …/api/v1/forecast/backtest` | Haftanın günü etkisi, sızıntısız backtest, FEFO kayıp simülasyonu; seed verisinde MAPE ≈ %10 |
| T-5 | `TEST_DATABASE_URL=… go test ./internal/app -run 'Health|Dual|AppendOnly' -v` | Şifreleme + gerekçe + denetim; çift onay (tetikleyici dahil); append-only ve değişmez alanlar |
| Kaos | `make chaos` | DB durdurulunca `/health` 503, yazma 503 `service_unavailable`; DB dönünce otomatik toparlanma; bütünlük korunur |
| Yük | `make load` | k6: p50/p95/p99 ve hata oranı (README'deki tablo) |

Entegrasyon testleri için yerelde boş bir PostgreSQL yeterlidir (testler şemayı sıfırlar):

```bash
docker run -d --name pgtest -e POSTGRES_USER=damar -e POSTGRES_PASSWORD=damar -e POSTGRES_DB=damar_test -p 55432:5432 postgres:16-alpine
TEST_DATABASE_URL=postgres://damar:damar@localhost:55432/damar_test?sslmode=disable go test ./... -race -count=1
```
