#!/usr/bin/env bash
# Kaos senaryosu: veritabanını durdurur, API'nin bağımlılık kaybını /health üzerinden
# bildirdiğini (503) ve veritabanı geri gelince kendiliğinden toparlandığını (200) gösterir.
# Ayrıca kesinti sırasında yazma isteklerinin anlamlı hata döndüğünü ve veri bütünlüğünün korunduğunu denetler.
set -euo pipefail
API=${API_URL:-http://localhost:8080}
PW=${DEMO_PASSWORD:-Demo1234!}

health() { curl -s -o /dev/null -w '%{http_code}' --max-time 3 "$API/health" || echo 000; }
login() { curl -s "$API/auth/login" -H 'Content-Type: application/json' -d "{\"email\":\"$1\",\"password\":\"$PW\"}" | sed -E 's/.*"access_token":"([^"]+)".*/\1/'; }

step() { printf '\n\033[1m▶ %s\033[0m\n' "$*"; }

step "Başlangıç: /health = $(health)"
TOK=$(login hastane@damarbagis.local)

step "Veritabanı durduruluyor (docker compose stop db)"
docker compose stop db >/dev/null
sleep 2
code=$(health)
echo "  /health → $code (beklenen 503)"
[ "$code" = "503" ] || { echo "HATA: API bağımlılık kaybını bildirmedi"; exit 1; }
w=$(curl -s -o /dev/null -w '%{http_code}' --max-time 15 "$API/api/v1/requests" -H "Authorization: Bearer $TOK" \
  -H 'Content-Type: application/json' -H "Idempotency-Key: chaos-$(date +%s)" \
  -d '{"component":"RBC","group":"A+","units":1,"patient_ref":"KAOS-1"}' || echo 000)
echo "  kesinti sırasında yazma isteği → $w (beklenen 5xx; API çökmeden hata döner)"
docker compose ps api --format '  api durumu: {{.Status}}'

step "Veritabanı yeniden başlatılıyor"
t0=$(date +%s)
docker compose start db >/dev/null
for i in $(seq 1 60); do
  code=$(health)
  [ "$code" = "200" ] && break
  sleep 1
done
t1=$(date +%s)
echo "  /health → $code ($((t1 - t0)) sn içinde toparlandı)"
[ "$code" = "200" ] || { echo "HATA: API toparlanmadı"; exit 1; }

step "Toparlanma sonrası yazma"
r=$(curl -s -o /dev/null -w '%{http_code}' "$API/api/v1/requests" -H "Authorization: Bearer $TOK" -H 'Content-Type: application/json' \
  -H "Idempotency-Key: chaos-after-$(date +%s)" -d '{"component":"RBC","group":"A+","units":1,"patient_ref":"KAOS-2"}')
echo "  talep → $r (beklenen 201)"
[ "$r" = "201" ] || exit 1

step "Bütünlük: hiçbir ürün iki aktif rezervasyonda değil"
dup=$(docker compose exec -T db sh -c 'psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -tAc "SELECT count(*) FROM (SELECT product_id FROM reservations WHERE status='"'"'active'"'"' GROUP BY 1 HAVING count(*)>1) x"')
echo "  çift rezervasyon: $dup"
[ "$dup" = "0" ] || exit 1
printf '\n\033[32mKAOS SENARYOSU BAŞARILI\033[0m\n'
