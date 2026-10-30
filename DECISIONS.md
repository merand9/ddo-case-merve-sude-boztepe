# Mimari Karar Kayıtları (ADR)

Her kayıt: bağlam → karar → sonuçlar (artı/eksi) → değerlendirilen alternatifler.

---

## ADR-001 — Tek Go modülü, katmanlı paketler; iş kuralları saf "domain" paketlerinde

**Bağlam.** Uygunluk, ABO/Rh uyumu, tahmin ve ürün yaşam döngüsü projenin değerlendirilen çekirdeği. Bu kuralların veritabanı ya da HTTP olmadan test edilebilmesi gerekiyor (kapsam hedefi ≥ %70).

**Karar.** `internal/domain/{eligibility,compat,forecast,product}` paketleri yan etkisiz, yalnızca standart kütüphaneye bağımlı fonksiyonlardan oluşur. `internal/app` transaction, kilit ve denetim kaydını yönetir ve domain fonksiyonlarını çağırır. `internal/api` yalnızca girdi çözer, rol denetler, yanıt yazar.

**Sonuçlar.** Domain kapsamı %95–100. Aynı uygunluk motoru bağış kaydında, randevuda, çağrı listesinde ve simülatörde ortak kullanılıyor, bu yüzden "liste uygun diyor ama kayıt reddediyor" tutarsızlığı oluşmuyor. Eksi tarafı: `app` paketi büyük, ama tek sorumluluğu iş akışı.

**Alternatifler.** Hexagonal yapı ve repository arayüzleri gibi tam soyutlamalar bu ölçekte gereksiz tören olurdu. Tüm mantığı SQL fonksiyonlarına taşımak da Go testlerini ve okunabilirliği zayıflatırdı.

---

## ADR-002 — Eşzamanlı tahsiste `FOR UPDATE SKIP LOCKED` ve kısmi benzersiz dizin

**Bağlam.** İki talep aynı ürünü eşzamanlı rezerve edememeli. Acil talepler beklememeli.

**Karar.** Tahsis transaction'ı önce talep satırını `FOR UPDATE` ile kilitler. Ardından aday ürünleri uyum sırası ve FEFO ile `SELECT … FOR UPDATE OF p SKIP LOCKED` kullanarak seçer. Başka bir transaction'ın kilitlediği ürün atlanır, kimse kimseyi beklemez. Son savunma hattı olarak `reservations_one_active` dizini `(product_id) WHERE status='active'` koşuluyla benzersizdir; uygulama hatası olsa bile çift rezervasyon veritabanında imkânsızdır. Aynı bağışçı için eşzamanlı bağış kaydı da bağışçı satırının `FOR UPDATE` ile kilitlenmesiyle sıraya girer.

**Kanıt.** `TestConcurrentAllocationNeverDoubleBooks` (3 ürün, 20 eşzamanlı tahsis → tam 3 rezervasyon), `TestUniqueActiveReservationIndex`, `TestConcurrentDonationsSameDonor` (5 eşzamanlı istek → 1 bağış, 4 red), `make verify` içindeki T-3 (15 paralel HTTP talebi).

**Alternatifler.** `SERIALIZABLE` ile yeniden deneme doğru sonuç verir ama çakışmada yeniden deneme maliyeti doğurur, yük altında gecikmeyi artırır. Optimistic `version` sütunu ise aynı ürünü isteyen iki talepten birini boşuna başarısız kılar. `SKIP LOCKED` bu iş yükü için hem doğru hem de en hızlı seçenek.

---

## ADR-003 — Ürün durum makinesi veritabanında; geçişler eklemeli-yalnız olay tablosunda

**Bağlam.** "Ürün durum geçişleri geri alınamaz kayıtla yapılmalı." Uygulama katmanındaki bir hata bile transfüze edilmiş bir ürünü stoğa döndürmemeli.

**Karar.** `product_transitions` tablosu izinli geçişleri tanımlar. `products_guard` tetikleyicisi her `UPDATE` işleminde geçişi denetler, geçersizse `P0010` hatasıyla reddeder. Geçerli her geçişi aktör ve gerekçeyle birlikte `product_events` tablosuna yazar. Aktör ve gerekçe, transaction'a özel `set_config('app.actor_id' …)` ayarından okunur. `product_events` ve `audit_log` tablolarında `UPDATE/DELETE/TRUNCATE` tetikleyiciyle yasaktır. Ürünün kimlik alanları (SKT, grup, bağış) değiştirilemez ve ürün silinemez. Aynı geçiş tablosu Go tarafında da (`domain/product`) tutulur, böylece API veritabanına gitmeden anlamlı hata dönebilir. Testler iki tablonun aynı olduğunu (17 geçiş) doğrular.

**Sonuçlar.** Kural tek yerde zorlanır, `psql` ile yapılan elle müdahale de bu kurala tabidir. Eksi tarafı: toplu geçmiş yüklemesinde ilk olay tetikleyicisi `app.bulk_load` ile atlanır. Bu açık, yalnızca simülatörün tek transaction'ında kullanılır.

**Alternatifler.** Olay kaynaklı (event-sourced) ürün tablosunda durum olaylardan türetilir. Daha saf bir çözüm olurdu ama sorgu maliyeti ve karmaşıklığı artardı. Yalnız uygulama içi kontrol ise elle yapılan SQL müdahalesine karşı korumasızdı.

---

## ADR-004 — Zaman geçerlilikli kurallar: JSON sürümler + `EXCLUDE` kısıtı

**Bağlam.** Bekleme süreleri, Hb sınırları ve erteleme süreleri zamanla değişir. Geçmiş kararlar o günün kuralıyla açıklanabilmeli, gelecekteki değişiklik önceden tanımlanabilmeli.

**Karar.** `rule_versions(code, params jsonb, valid_from, valid_to)` tablosu kullanılır. `EXCLUDE USING gist (code WITH =, daterange(valid_from, valid_to) WITH &&)` kısıtıyla aynı kod için aralıkların çakışması veritabanında imkânsızdır. Yeni sürüm yalnızca ileri bir tarihten eklenebilir; o günü kapsayan sürüm o tarihte kapatılır. Karar, karar anındaki sürümle verilir ve `rule_version` alanında döner. Kayıtlı bir ertelemenin bitiş günü, **erteleme başlangıcında** geçerli olan kurala göre hesaplanıp saklanır; kural sonradan değişse de mevcut erteleme etkilenmez.

**Kanıt.** `TestRuleBookTimeValidity`, `TestDeferralEndUsesRuleAtStart`, `TestRuleVersionTimeValidity` ve verify T-1 (ileri tarihli `max_age=20` sürümü yalnızca o tarihten sonra red üretir).

**Alternatifler.** Kural başına satır (ör. `min_age`, `hb_min_F` …) daha ilişkisel olurdu ama bir "kural seti"nin bütün olarak sürümlenmesini zorlaştırırdı. Kodu sabit (hard-coded) kurallarla bırakmak ise zaman geçerliliği gereksinimini karşılamazdı.

---

## ADR-005 — Sağlık verisi uygulama katmanında AES-256-GCM; AAD = bağışçı no

**Bağlam.** Sağlık beyanı şifreli saklanmalı, erişimde gerekçe zorunlu olmalı ve denetim kaydı tutulmalı.

**Karar.** `health_enc bytea = [sürüm][nonce][şifreli+etiket]`. AAD olarak bağışçı numarası kullanılır; şifreli değer başka bir satıra kopyalanırsa çözülemez. Anahtar ortam değişkeninden gelir ve veritabanında bulunmaz. Okuma uç noktası `X-Access-Reason` başlığını (en az 10 karakter) zorunlu tutar. Çözme işlemi ve `donor.health.read` denetim kaydı **aynı transaction'da** yapılır: denetim yazılamazsa veri de dönmez. Hastane rolü yalnızca `hospital_products` görünümünü kullanır. Bu görünümde bağış ve bağışçı sütunları yoktur.

**Alternatifler.** `pgcrypto` ile veritabanında şifreleme yapmak anahtarı SQL oturumuna taşır; anahtar sorgu loglarına sızabilir. Disk düzeyinde şifreleme ise veritabanı kullanıcısına karşı koruma sağlamaz.

**Bilinen sınırlama.** Anahtar rotasyonu için sürüm baytı var ama rotasyon aracı yazılmadı. Kimlik bilgileri (ad, telefon) operasyonel arama gerektiği için düz metin tutuluyor.

---

## ADR-006 — Tahmin modeli: 28 günlük hareketli ortalama × 8 haftalık gün çarpanı, FEFO kayıp simülasyonu

**Bağlam.** Grup bazında tüketim hızı, haftanın günü etkisi ve SKT kaynaklı kayıp tahmini isteniyor. Sonuç geriye dönük testle raporlanmalı.

**Karar.** `taban = son 28 günün ortalaması` (7'nin katı olduğu için haftalık etkiden arınmış) ve `çarpan(g) = son 8 haftada g gününün ortalaması / genel ortalama` (0.2–3 aralığına sınırlı). SKT kaybı, stoktaki ürünlerin SKT'leri üzerinde FEFO tüketim simülasyonuyla hesaplanır. Backtest rolling-origin yöntemiyle yapılır: her gün yalnızca o günden önceki veriyle tahmin edilir, böylece sızıntı olmaz (`TestBacktestNoLeakage`). MAPE'nin yanında WAPE de raporlanır, çünkü düşük hacimli gruplarda (AB−) MAPE yanıltıcı derecede yüksek çıkar.

**Sonuç.** Seed verisinde RBC toplam MAPE ≈ %10, WAPE ≈ %9. A+ ve O+ gruplarında yaklaşık %15; AB− ve B− gruplarında %45 üzeri (Poisson gürültüsü baskın).

**Alternatifler.** Holt-Winters / ETS daha iyi trend yakalar ama 12 haftalık veride parametre kestirimi kararsızdır. Harici bir ML kütüphanesi ise bağımlılık ve açıklanabilirlik maliyeti getirirdi.

---

## ADR-007 — Bağımlılık minimizasyonu: chi + pgx + jwt + x/crypto; migrasyon ve metrik için küçük kendi uygulamalarımız

**Bağlam.** Soğuk makinede 5 dakika içinde ayağa kalkma hedefi ve küçük imaj isteniyor.

**Karar.** Doğrudan bağımlılıklar yalnızca `chi`, `pgx/v5`, `golang-jwt/jwt/v5` ve `golang.org/x/crypto` (argon2id). Migrasyonlar gömülü SQL dosyalarıyla, `schema_migrations` tablosu ve advisory lock kullanan ~60 satırlık bir çalıştırıcıyla uygulanır. Prometheus metin biçimi ~150 satırlık bir paketle üretilir (`internal/metrics`, test edildi). İmajlar `distroless/static:nonroot` tabanlıdır, kabuk içermez; healthcheck ikilinin kendi `healthcheck` alt komutuyla yapılır.

**Sonuçlar.** `go mod download` saniyeler sürer, imaj yaklaşık 14 MB (12 MB ikili + 2 MB distroless taban). Eksi tarafı: `goose` / `client_golang`'ın down-migrasyon ve runtime metrikleri gibi gelişmiş özellikleri yok.

**Alternatifler.** `goose` çok sayıda veritabanı sürücüsünü dolaylı bağımlılık olarak getiriyor. `prometheus/client_golang` ise protobuf zincirini getiriyor.

---

## ADR-008 — Ön yüz: derleme adımsız, bağımlılıksız tek sayfa uygulama (vanilla JS + nginx)

**Bağlam.** Ön yüz serbest bırakılmış. Değerlendirmede sistemin hızlı ayağa kalkması, gerçek veriyle çalışması ve mobilde kullanılabilir olması bekleniyor.

**Karar.** Hash tabanlı yönlendirmeyle çalışan tek bir `app.js` (~700 satır) ve CSS değişkenleriyle yazılmış bir stil dosyası kullanılır. Her ekran ortak `section()` yardımcısıyla yükleniyor / boş / hata durumlarını gösterir. Durum değiştiren POST isteklerine otomatik `Idempotency-Key` eklenir ve gönderim düğmesi istek süresince kilitlenir. 401 alındığında refresh token ile sessizce yenileme yapılır. nginx `/api` ve `/auth` isteklerini API'ye vekil eder, böylece tarayıcı tek origin ile çalışır ve CORS gerekmez. CSP `script-src 'self'` olarak ayarlıdır (inline script yok; yalnızca stil öznitelikleri için `style-src 'unsafe-inline'`).

**Gerekçe.** React, Vue gibi bir framework `npm install` ile derleme adımı ekler ve soğuk önbellekte dakikalar alabilir. Bu ekranlar ağırlıklı olarak form ve tablodan oluştuğu için framework'ün sağladığı kazanç sınırlı. Bedeli: bileşen yeniden kullanımı ve tip güvenliği daha zayıf. Ekran sayısı artarsa Svelte ya da Preact'e geçiş mantıklı olur.

---

## ADR-009 — Simülatör: seed verisini saf fonksiyonla üretip COPY ile yükleme, canlı modda API kullanma

**Bağlam.** 30.000 bağışçı, aynı tohumla aynı veri, gerçekçi bir tüketim geçmişi ve arayüzde görünen canlı olaylar isteniyor.

**Karar.** `sim.Generate(Config)` saf bir fonksiyondur ve 120 günü gün gün simüle eder. Bu simülasyonda canlı sistemle aynı `compat.Candidates` + FEFO tahsis kuralı ve aynı bekleme süreleri kullanılır. Map iterasyon sırası sabitlenmiştir (determinizm). Çıktı tek transaction'da `COPY` ile yüklenir (≈4 sn). Kimlikler tablo dizilerinden blok halinde ayrılır, böylece API'nin oluşturduğu kayıtlarla çakışmaz. Sağlık verisi bağışçıya özgü deterministik nonce ile şifrelenir. Canlı mod tüm yazmaları **API üzerinden** demo hesaplarıyla yapar; RBAC, uygunluk, idempotans ve denetim kaydı gerçek akıştan geçer. Bilerek uygunsuz bağış denemeleri de üretilir ve 422 ile reddedilip denetime yazılır.

**Kanıt.** `TestSameSeedSameData` (aynı tohum aynı parmak izi, farklı tohum farklı) ve `TestGeneratedHistoryRespectsInvariants` (çapa sonrası olay yok, ürün iki kez dağıtılmıyor, bekleme süresi ihlali yok, uyumsuz dağıtım yok).
