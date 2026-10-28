#!/usr/bin/env bash
# Black-box authorisation probe (make security): tries to get in where it
# should not, with curl, against the running stack. Every line compares the
# HTTP status with the expected one; any deviation is a finding.
#
# Demo data assumed (make reset-demo): employees 1-6 are the demo accounts
# (1 calisan, 2 yonetici, 3 ik, 4 bordro, 5 muhasebe, 6 denetci).
set -u
API=${API:-http://localhost:8080}
B="$API/api/v1"
PW=${DEMO_PASSWORD:-Demo.2026!}
COMPOSE=${COMPOSE:-docker compose}
pass=0; fail=0
TMP=$(mktemp -d)

ok()   { pass=$((pass + 1)); printf '  ok    %-4s %s\n' "$1" "$2"; }
bad()  { fail=$((fail + 1)); printf '  FAIL  %-4s %s  (beklenen %s)\n' "$1" "$2" "$3"; }

# req METHOD PATH TOKEN [BODY] -> prints status; body in $TMP/body, headers in $TMP/hdr
req() {
  local args=(-s -o "$TMP/body" -D "$TMP/hdr" -w '%{http_code}' -X "$1" "$B$2")
  [[ -n "${3:-}" ]] && args+=(-H "Authorization: Bearer $3")
  [[ -n "${4:-}" ]] && args+=(-H 'Content-Type: application/json' -d "$4")
  curl "${args[@]}"
}
# expect WANT METHOD PATH TOKEN [BODY] [LABEL]
expect() {
  local got; got=$(req "$2" "$3" "$4" "${5:-}")
  local label="${6:-$2 $3}"
  [[ "$got" == "$1" ]] && ok "$got" "$label" || bad "$got" "$label" "$1"
}
login() {
  curl -s -c "$TMP/jar_$1" -H 'Content-Type: application/json' \
    -d "{\"email\":\"$1@bordroplus.test\",\"password\":\"$PW\"}" "$B/auth/login" |
    sed -n 's/.*"access_token":"\([^"]*\)".*/\1/p'
}
b64url() { base64 | tr -d '=\n' | tr '/+' '_-'; }

for u in calisan yonetici ik bordro muhasebe denetci; do declare "T_$u=$(login $u)"; done
OUTSIDE=$(curl -s -H "Authorization: Bearer $T_ik" "$B/employees?q=S00015" | sed -n 's/.*"id":\([0-9]*\).*/\1/p' | head -1)
INTEAM=$(curl -s -H "Authorization: Bearer $T_ik" "$B/employees?q=S00002" | sed -n 's/.*"id":\([0-9]*\).*/\1/p' | head -1)
echo "BordroPlus yetki denemesi — $API (ekip içi #$INTEAM, ekip dışı #$OUTSIDE)"

echo; echo "1) Token'sız erişim: her korumalı uç nokta 401"
while read -r m p; do expect 401 "$m" "$p" ""; done <<'EOF'
GET /auth/me
GET /audit-log
GET /system/status
GET /org/units
POST /org/units
PATCH /org/units/1
GET /employees
GET /employees/1
POST /employees
PATCH /employees/1
GET /employees/1/salaries
POST /employees/1/salaries
GET /delegations
POST /delegations
POST /delegations/1/revoke
GET /leave/holidays
POST /leave/holidays
GET /leave/employees/1/balance
GET /leave/employees/1/ledger
GET /leave/requests
POST /leave/requests
GET /leave/requests/1
POST /leave/requests/1/approve
POST /leave/requests/1/reject
POST /leave/requests/1/cancel
POST /timesheet/punches
POST /timesheet/clock
GET /timesheet/employees/1/days?from=2026-01-01&to=2026-01-31
GET /timesheet/employees/1/months/2026-01
POST /timesheet/employees/1/months/2026-01/approve
GET /timesheet/corrections
POST /timesheet/corrections/1/approve
POST /timesheet/corrections/1/reject
GET /payroll/rule-sets
GET /payroll/periods
GET /payroll/periods/2025-01
POST /payroll/periods/2025-01/run
POST /payroll/periods/2025-01/submit
POST /payroll/periods/2025-01/approve
POST /payroll/periods/2025-01/reject
POST /payroll/periods/2025-01/close
POST /payroll/employees/1/calculate
POST /payroll/employees/1/recalculate
GET /payroll/employees/1/results
GET /payroll/employees/1/results/2025-01
GET /payroll/employees/1/payslips/2025-01
GET /payroll/items
POST /payroll/items
POST /payroll/items/1/cancel
GET /reports/payroll-summary?period=2025-01
GET /reports/employer-cost
GET /reports/leave-usage
GET /reports/overtime?month=2025-01
EOF

echo; echo "2) Token manipülasyonu"
IFS=. read -r H P S <<<"$T_calisan"
NONE="$(printf '{"alg":"none","typ":"JWT"}' | b64url).$P."
expect 401 GET /auth/me "$NONE" "" "alg=none token"
FORGED_P=$(printf '%s' "$P" | tr '_-' '/+' | base64 -d 2>/dev/null | sed 's/"role":"employee"/"role":"hr"/;s/"sub":"1"/"sub":"3"/' | b64url)
expect 401 GET /auth/me "$H.$FORGED_P.$S" "" "rolü hr / sub=3 yapılmış, imza eski"
expect 401 GET /auth/me "${T_calisan%?}A" "" "imzanın son karakteri değiştirilmiş"
OTHERKEY="$H.$P.$(printf 'x%.0s' {1..43})"
expect 401 GET /auth/me "$OTHERKEY" "" "başka anahtarla imza"
expect 401 GET /auth/me "Bearer $T_calisan" "" "çift Bearer önekli"
code=$(curl -s -o /dev/null -w '%{http_code}' -H "Authorization: $T_calisan" "$B/auth/me")
[[ "$code" == 401 ]] && ok 401 "Bearer öneki olmadan token" || bad "$code" "Bearer öneki olmadan token" 401

echo; echo "3) Rol dışı işlemler: 403"
expect 403 GET /audit-log "$T_calisan" "" "çalışan → denetim kaydı"
expect 403 GET /audit-log "$T_ik" "" "İK → denetim kaydı"
expect 403 POST /employees "$T_calisan" '{"employee_no":"X-001","first_name":"a","last_name":"b","org_unit_id":3,"hire_date":"2026-01-01"}' "çalışan → personel oluşturma"
expect 403 POST /employees "$T_yonetici" '{"employee_no":"X-001","first_name":"a","last_name":"b","org_unit_id":3,"hire_date":"2026-01-01"}' "yönetici → personel oluşturma"
expect 403 POST /employees/1/salaries "$T_bordro" '{"amount":"999999.00","valid_from":"2026-12-01"}' "bordro → maaş yazma"
expect 403 POST /employees/1/salaries "$T_calisan" '{"amount":"999999.00","valid_from":"2026-12-01"}' "çalışan → kendi maaşını yükseltme"
expect 403 PATCH /org/units/1 "$T_yonetici" '{"name":"x"}' "yönetici → organizasyon değiştirme"
expect 403 POST /payroll/periods/2026-12/run "$T_ik" "" "İK → bordro hesaplama"
expect 403 POST /payroll/periods/2026-12/approve "$T_bordro" "" "bordro → kendi hesabını onaylama"
expect 403 POST /payroll/periods/2026-12/close "$T_bordro" "" "bordro → dönem kapatma"
expect 403 POST /payroll/employees/1/calculate "$T_muhasebe" '{"period":"2026-01"}' "muhasebe → bordro hesaplama"
expect 403 POST /payroll/items "$T_calisan" '{"employee_id":1,"kind":"bonus","label":"x","amount":"1000000.00","start_period":"2026-12"}' "çalışan → kendine prim"
expect 403 GET /payroll/periods "$T_yonetici" "" "yönetici → bordro dönemleri"
expect 403 GET /reports/payroll-summary?period=2025-01 "$T_yonetici" "" "yönetici → bordro raporu"
expect 403 POST /timesheet/punches "$T_yonetici" '{"punches":[{"employee_id":1,"punched_at":"2026-01-05T08:00:00+03:00","direction":"in"}]}' "yönetici → basım aktarma"
expect 403 POST /leave/holidays "$T_calisan" '{"date":"2027-12-31","name":"x"}' "çalışan → tatil ekleme"
expect 403 POST /delegations "$T_calisan" '{"delegate_employee_id":3,"start_date":"2027-01-01","end_date":"2027-01-02"}' "çalışan → vekâlet verme"
expect 403 POST /delegations "$T_denetci" '{"manager_employee_id":2,"delegate_employee_id":6,"start_date":"2027-01-01","end_date":"2027-01-02"}' "denetçi → vekâlet verme"

echo; echo "4) IDOR: başkasının kaydı"
for p in "/employees/3" "/employees/3/salaries" "/leave/employees/3/balance" "/leave/employees/3/ledger" \
         "/timesheet/employees/3/months/2025-11" "/timesheet/employees/3/days?from=2025-11-01&to=2025-11-30" \
         "/payroll/employees/3/results?year=2025" "/payroll/employees/3/results/2025-01" "/payroll/employees/3/payslips/2025-01"; do
  expect 403 GET "$p" "$T_calisan" "" "çalışan → GET $p"
done
expect 403 GET "/employees/$OUTSIDE" "$T_yonetici" "" "yönetici → alt ağaç dışı kart"
expect 403 GET "/leave/employees/$OUTSIDE/balance" "$T_yonetici" "" "yönetici → alt ağaç dışı izin bakiyesi"
expect 403 GET "/timesheet/employees/$OUTSIDE/months/2025-11" "$T_yonetici" "" "yönetici → alt ağaç dışı puantaj"
expect 403 GET "/payroll/employees/$INTEAM/results?year=2025" "$T_yonetici" "" "yönetici → ekibinin bordrosu"
expect 403 POST "/employees/$OUTSIDE/salaries" "$T_yonetici" '{"amount":"1.00","valid_from":"2026-12-01"}' "yönetici → ekip dışı maaş yazma"
expect 403 POST /leave/requests "$T_calisan" '{"employee_id":3,"leave_type":"annual","start_date":"2027-02-01","end_date":"2027-02-01"}' "çalışan → başkası adına izin talebi"
masked=$(curl -s -H "Authorization: Bearer $T_yonetici" "$B/employees/$INTEAM")
[[ "$masked" == *'"salary_masked":true'* && "$masked" != *'"amount"'* ]] && ok 200 "yönetici → ekip üyesi kartında maaş maskeli" || bad "?" "yönetici ekip üyesinin maaşını görüyor" "maskeli"
masked=$(curl -s -H "Authorization: Bearer $T_denetci" "$B/employees/1")
[[ "$masked" == *'"salary_masked":true'* ]] && ok 200 "denetçi → kartta maaş maskeli" || bad "?" "denetçi maaşı görüyor" "maskeli"
list=$(curl -s -H "Authorization: Bearer $T_calisan" "$B/employees?limit=200")
n=$(grep -o '"employee_no"' <<<"$list" | wc -l)
(( n == 1 )) && ok 200 "çalışan listesinde yalnız kendisi ($n kayıt)" || bad 200 "çalışan listesinde $n kayıt" "1"

echo; echo "5) İş kuralı atlatma"
# A weekday about two months ahead (a weekend day would be refused as "no working days").
off=60; while :; do day=$(date -d "+$off days" +%F); (( $(date -d "+$off days" +%u) <= 5 )) && break; off=$((off + 1)); done
code=$(req POST /leave/requests "$T_yonetici" "{\"leave_type\":\"annual\",\"start_date\":\"$day\",\"end_date\":\"$day\"}")
RID=$(sed -n 's/.*"id":\([0-9]*\).*/\1/p' "$TMP/body" | head -1)
expect 403 POST "/leave/requests/$RID/approve" "$T_yonetici" "" "yönetici → kendi iznini onaylama"
curl -s -o /dev/null -X POST -H "Authorization: Bearer $T_ik" "$B/leave/requests/$RID/cancel"
code=$(req POST /leave/requests "$T_calisan" "{\"leave_type\":\"annual\",\"start_date\":\"$day\",\"end_date\":\"$day\"}")
RID=$(sed -n 's/.*"id":\([0-9]*\).*/\1/p' "$TMP/body" | head -1)
expect 403 POST "/leave/requests/$RID/approve" "$T_calisan" "" "çalışan → kendi iznini onaylama"
expect 403 POST "/leave/requests/$RID/approve" "$T_denetci" "" "denetçi → izin onaylama"
curl -s -o /dev/null -X POST -H "Authorization: Bearer $T_calisan" "$B/leave/requests/$RID/cancel"
# Idempotency keys are per user: someone else's key must not replay their response.
KEY="probe-$(date +%s%N)"
curl -s -o /dev/null -X POST -H "Authorization: Bearer $T_ik" -H "Idempotency-Key: $KEY" -H 'Content-Type: application/json' \
  -d "{\"employee_no\":\"P$(date +%s | tail -c 9)\",\"first_name\":\"a\",\"last_name\":\"b\",\"org_unit_id\":3,\"hire_date\":\"2026-01-01\"}" "$B/employees"
code=$(curl -s -o "$TMP/body" -D "$TMP/hdr" -w '%{http_code}' -X POST -H "Authorization: Bearer $T_calisan" -H "Idempotency-Key: $KEY" \
  -H 'Content-Type: application/json' -d '{"employee_no":"X-1","first_name":"a","last_name":"b","org_unit_id":3,"hire_date":"2026-01-01"}' "$B/employees")
[[ "$code" == 403 ]] && ! grep -qi 'Idempotent-Replayed' "$TMP/hdr" && ok 403 "başka kullanıcının Idempotency-Key'i ile yanıt çalma" || bad "$code" "başka kullanıcının Idempotency-Key'i yanıtı döndürdü" 403

echo; echo "6) Girdi: enjeksiyon, toplu atama, sınırlar"
expect 200 GET "/employees?q=%27%20OR%201%3D1%20--" "$T_calisan" "" "SQL enjeksiyonu (q) çalışanı kapsam dışına çıkaramaz"
n=$(grep -o '"employee_no"' "$TMP/body" | wc -l); (( n == 0 )) && ok "-" "  → enjeksiyon sonucu boş" || bad "$n" "  → enjeksiyonla $n kayıt döndü" 0
expect 200 GET "/audit-log?action=x%27%3B%20DROP%20TABLE%20users%3B--" "$T_denetci" "" "SQL enjeksiyonu (action)"
expect 400 POST /employees "$T_ik" '{"employee_no":"X-9","first_name":"a","last_name":"b","org_unit_id":3,"hire_date":"2026-01-01","id":1,"salary_masked":false}' "toplu atama: bilinmeyen alan reddedilir"
expect 400 POST /leave/requests "$T_calisan" '{"leave_type":"annual","start_date":"2027-01-04","end_date":"2027-01-04","status":"approved"}' "toplu atama: talebi 'approved' gönderme"
expect 400 POST /auth/login "" '{"email":"calisan@bordroplus.test","password":"x","role":"hr"}' "toplu atama: girişte rol"
{ printf '{"employee_no":"'; head -c 1100000 /dev/zero | tr '\0' 'a'; printf '"}'; } > "$TMP/big.json"
code=$(curl -s -o /dev/null -w '%{http_code}' -H "Authorization: Bearer $T_ik" -H 'Content-Type: application/json' \
  --data-binary "@$TMP/big.json" "$B/employees")
[[ "$code" == 413 ]] && ok 413 "1 MB üstü gövde" || bad "$code" "1 MB üstü gövde" 413
expect 422 POST /payroll/items "$T_bordro" '{"employee_id":1,"kind":"advance","label":"x","amount":"-5000.00","start_period":"2027-01"}' "negatif tutarla avans"
expect 404 GET "/employees/../../etc/passwd" "$T_ik" "" "yol geçişi"

echo; echo "7) Oturum: refresh tekrar kullanımı, pasif hesap"
curl -s -o /dev/null -c "$TMP/j1" -H 'Content-Type: application/json' -d "{\"email\":\"denetci@bordroplus.test\",\"password\":\"$PW\"}" "$B/auth/login"
cp "$TMP/j1" "$TMP/stolen"
c1=$(curl -s -o /dev/null -w '%{http_code}' -b "$TMP/j1" -c "$TMP/j1" -X POST "$B/auth/refresh")
c2=$(curl -s -o /dev/null -w '%{http_code}' -b "$TMP/stolen" -X POST "$B/auth/refresh")
c3=$(curl -s -o /dev/null -w '%{http_code}' -b "$TMP/j1" -X POST "$B/auth/refresh")
[[ "$c1/$c2/$c3" == "200/401/401" ]] && ok "-" "çalınmış refresh token: saldırgan 401, aile iptal (kurban da 401)" || bad "$c1/$c2/$c3" "refresh tekrar kullanımı" "200/401/401"
U=$(curl -s -H "Authorization: Bearer $T_muhasebe" "$B/auth/me" | sed -n 's/.*"id":\([0-9]*\).*/\1/p' | head -1)
$COMPOSE exec -T db sh -c "psql -U \"\$POSTGRES_USER\" -d \"\$POSTGRES_DB\" -qc 'UPDATE users SET is_active = false WHERE id = $U'" >/dev/null
expect 401 GET /auth/me "$T_muhasebe" "" "pasifleştirilmiş hesabın henüz geçerli token'ı"
$COMPOSE exec -T db sh -c "psql -U \"\$POSTGRES_USER\" -d \"\$POSTGRES_DB\" -qc 'UPDATE users SET is_active = true WHERE id = $U'" >/dev/null

echo; echo "8) Yapılandırma"
cookie=$(curl -s -D - -o /dev/null -H 'Content-Type: application/json' -d "{\"email\":\"ik@bordroplus.test\",\"password\":\"$PW\"}" "$B/auth/login" | grep -i '^set-cookie')
[[ "$cookie" == *HttpOnly* && "$cookie" == *SameSite=Strict* && "$cookie" == *"Path=/api/v1/auth"* ]] && ok "-" "refresh cookie: HttpOnly, SameSite=Strict, Path=/api/v1/auth" || bad "-" "cookie bayrakları: $cookie" "HttpOnly+Strict"
acao=$(curl -s -D - -o /dev/null -H "Origin: https://evil.example" -H "Authorization: Bearer $T_ik" "$B/employees" | grep -i '^access-control-allow-origin')
[[ -z "$acao" ]] && ok "-" "CORS: yabancı origin'e izin yok" || bad "-" "CORS yabancı origin'e açık: $acao" "yok"
hdr=$(curl -s -D - -o /dev/null "$API/health")
for h in X-Content-Type-Options X-Frame-Options Referrer-Policy Cache-Control; do
  grep -qi "^$h:" <<<"$hdr" && ok "-" "başlık $h" || bad "-" "başlık $h eksik" "var"
done
grep -qi '^server:.*[0-9]' <<<"$hdr" && bad "-" "Server başlığı sürüm sızdırıyor" "yok" || ok "-" "Server başlığında sürüm yok"
err=$(curl -s -X POST -H "Authorization: Bearer $T_ik" -H 'Content-Type: application/json' -d '{"employee_no":"D0001","first_name":"a","last_name":"b","org_unit_id":999999,"hire_date":"2026-01-01"}' "$B/employees")
grep -qiE 'pgx|SQLSTATE|postgres|panic|goroutine' <<<"$err" && bad "-" "hata yanıtı iç ayrıntı sızdırıyor: $err" "temiz" || ok "-" "hata yanıtlarında iç ayrıntı yok"
(exec 3<>/dev/tcp/127.0.0.1/5432) 2>/dev/null && bad "-" "PostgreSQL 5432 hosta açık" "kapalı" || ok "-" "PostgreSQL hosttan erişilemez"
m=$(curl -s -o /dev/null -w '%{http_code}' "$API/metrics")
[[ "$m" == 200 ]] && printf '  NOT   %-4s %s\n' "$m" "/metrics kimliksiz erişilebilir (bkz. bulgular)"

echo; echo "9) Kaba kuvvet ve kullanıcı sayımı"
t_known=0; t_unknown=0
for i in 1 2 3; do
  t_known=$(awk "BEGIN{print $t_known + $(curl -s -o /dev/null -w '%{time_total}' -H 'Content-Type: application/json' -d '{"email":"bordro@bordroplus.test","password":"yanlis"}' "$B/auth/login")}")
  t_unknown=$(awk "BEGIN{print $t_unknown + $(curl -s -o /dev/null -w '%{time_total}' -H 'Content-Type: application/json' -d '{"email":"yok'"$i"'@bordroplus.test","password":"yanlis"}' "$B/auth/login")}")
done
awk "BEGIN{d=($t_known-$t_unknown)/3; if (d<0) d=-d; exit !(d < 0.05)}" && ok "-" "var olan / olmayan e-posta yanıt süresi farkı < 50 ms" || bad "-" "yanıt süresi kullanıcı varlığını sızdırıyor ($t_known / $t_unknown)" "<50 ms"
for i in 1 2 3; do curl -s -o /dev/null -H 'Content-Type: application/json' -d '{"email":"bordro@bordroplus.test","password":"yanlis"}' "$B/auth/login"; done
expect 401 POST /auth/login "" "{\"email\":\"bordro@bordroplus.test\",\"password\":\"$PW\"}" "5 hatalı denemeden sonra doğru parola da reddedilir (kilit)"
$COMPOSE exec -T db sh -c "psql -U \"\$POSTGRES_USER\" -d \"\$POSTGRES_DB\" -qc \"UPDATE users SET failed_logins = 0, locked_until = NULL WHERE email = 'bordro@bordroplus.test'\"" >/dev/null

rm -rf "$TMP"
echo; echo "Sonuç: $pass başarılı, $fail bulgu"
(( fail == 0 ))
