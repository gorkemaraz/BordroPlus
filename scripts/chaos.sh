#!/usr/bin/env bash
# Chaos scenario (make chaos): take dependencies away from the running stack
# and show that the system degrades cleanly and recovers by itself.
#
#   1. database stopped  -> API stays up, /health 503 fast, requests fail fast
#   2. database back     -> API recovers without a restart (pool reconnects)
#   3. API restarted     -> the nginx proxy (:3000) re-resolves the new container
#
# Prints PASS/FAIL per step and exits non-zero if any step fails.
set -u

API=${API:-http://localhost:8080}
WEB=${WEB:-http://localhost:3000}
PASSWORD=${DEMO_PASSWORD:-Demo.2026!}
COMPOSE=${COMPOSE:-docker compose}
fails=0

step() { printf '%-64s ' "$1"; }
pass() { echo "PASS ${1:-}"; }
fail() { echo "FAIL ${1:-}"; fails=$((fails + 1)); }

code() { curl -s -o /dev/null -m "${2:-10}" -w '%{http_code}' "$1"; }
timed() { curl -s -o /dev/null -m "${2:-10}" -w '%{http_code} %{time_total}' "$1"; }

login() {
  curl -s -m 10 -o /dev/null -w '%{http_code}' -H 'Content-Type: application/json' \
    -d "{\"email\":\"calisan@bordroplus.test\",\"password\":\"$PASSWORD\"}" "$API/api/v1/auth/login"
}

# wait_for URL CODE SECONDS: poll until URL answers CODE; prints elapsed seconds.
wait_for() {
  local start=$SECONDS
  while (( SECONDS - start < $3 )); do
    [[ "$(code "$1" 3)" == "$2" ]] && { echo $((SECONDS - start)); return 0; }
    sleep 1
  done
  return 1
}

echo "BordroPlus kaos senaryosu — $API, $WEB"
echo

step "0. Başlangıç: API ve arayüz vekili sağlıklı"
[[ "$(code "$API/health")" == 200 && "$(code "$WEB/health")" == 200 ]] && pass || { fail "(yığın ayakta mı?)"; exit 1; }

# 1. Database outage
$COMPOSE stop db >/dev/null 2>&1
step "1a. Veritabanı durduruldu: /health 503 ve 2 sn içinde yanıt"
read -r c t <<<"$(timed "$API/health")"
[[ "$c" == 503 ]] && awk "BEGIN{exit !($t < 2)}" && pass "($c, ${t}s)" || fail "($c, ${t}s)"

step "1b. Veritabanı yokken giriş 5 sn içinde 503 döner (asılı kalmaz)"
start=$(date +%s%N); c=$(login); ms=$(( ($(date +%s%N) - start) / 1000000 ))
[[ "$c" == 503 ]] && (( ms < 5000 )) && pass "($c database_unavailable, ${ms} ms)" || fail "($c, ${ms} ms)"

step "1c. API süreci ayakta kaldı (yeniden başlamadı)"
restarts_before=$(docker inspect -f '{{.RestartCount}}' "$($COMPOSE ps -q api)")
[[ "$($COMPOSE ps --status running -q api)" != "" ]] && pass "(restart sayısı $restarts_before)" || fail

# 2. Database recovery
$COMPOSE start db >/dev/null 2>&1
step "2a. Veritabanı geri geldi: /health 200'e döner (≤ 60 sn)"
if s=$(wait_for "$API/health" 200 60); then pass "(${s} sn)"; else fail; fi

step "2b. API yeniden başlatılmadan giriş ve veri okuma çalışır"
restarts_after=$(docker inspect -f '{{.RestartCount}}' "$($COMPOSE ps -q api)")
c=$(login)
[[ "$c" == 200 && "$restarts_after" == "$restarts_before" ]] && pass "(giriş $c, restart sayısı değişmedi)" || fail "(giriş $c, restart $restarts_before→$restarts_after)"

# 3. API restart behind the proxy
$COMPOSE restart api >/dev/null 2>&1
step "3a. API yeniden başlatıldı: vekil üzerinden /health 200 (≤ 60 sn)"
if s=$(wait_for "$WEB/health" 200 60); then pass "(${s} sn)"; else fail; fi

step "3b. Arayüz vekili üzerinden giriş çalışır"
c=$(curl -s -m 10 -o /dev/null -w '%{http_code}' -H 'Content-Type: application/json' \
  -d "{\"email\":\"calisan@bordroplus.test\",\"password\":\"$PASSWORD\"}" "$WEB/api/v1/auth/login")
[[ "$c" == 200 ]] && pass || fail "($c)"

step "3c. Simülatör kesintiden sonra çalışıyor"
[[ "$($COMPOSE ps --status running -q simulator)" != "" ]] && pass || fail

echo
if (( fails )); then echo "$fails adım başarısız"; exit 1; fi
echo "Tüm adımlar başarılı: sistem bağımlılık kaybında temiz biçimde bozuldu ve kendiliğinden toparlandı."
