# BordroPlus — Test ve Doğrulama Rehberi

## Hızlı başlangıç

```bash
cp .env.example .env
docker compose up --build        # ilk açılışta simülatör ~35 sn'de 2 yıllık veriyi yükler
make verify                      # her özellik ve görev için PASS/FAIL tablosu (~3 sn)
make reset-demo                  # tüm veriyi silip ilk seed haline döner (aynı tohum = aynı veri)
```

`make verify`, ayaktaki sistemde `docker compose --profile verify run --rm verify` çalıştırır. Örnek çıktı:

```
KİMLİK  KONTROL                                                  SONUÇ     SÜRE
────────────────────────────────────────────────────────────────────────────────
HEALTH  GET /health ve veritabanı bağlantısı                     PASS        0ms
AUTH    Giriş, yanlış parola, refresh rotation, çıkış            PASS       77ms
AUTHZ   Token yok 401, yetkisiz rol 403, sahte token             PASS        2ms
F-1     Personel kartı ve organizasyon ağacı                     PASS       14ms
IDEMP   Idempotency-Key ile tekrar edilen POST                   PASS        7ms
T-5     Alt ağaç yetkisi, vekâlet, maaş maskeleme                PASS       19ms
T-1     Hakediş, bakiye, eşzamanlı onay, rapor iadesi            PASS       67ms
F-2     İzin talebi, onay kuyruğu ve bakiye                      PASS       12ms
T-2     Eksik/çift basım, gece vardiyası, öneri onayı            PASS       27ms
F-3     Puantaj ay onayı ve kilit                                PASS        5ms
T-3     Brütten nete, kümülatif matrah, geçmiş ay düzeltmesi     PASS       39ms
T-4     Paralel hesap <10 sn, determinizm, iş akışı              PASS      955ms
F-4     Bordro hesaplama ve onay akışı (rol ayrımı)              PASS        6ms
F-6     Dönem kapanışı ve kapalı dönem koruması                  PASS        4ms
F-5     Avans/borç/ek ödeme ve geçmiş dönem düzeltmesi           PASS      518ms
F-7     Raporlar, CSV ve bordro çıktısı                          PASS       52ms
AUDIT   Denetim kaydı (yalnız denetçi)                           PASS        2ms
────────────────────────────────────────────────────────────────────────────────
17/17 PASS
```

**Tekrarlanabilirlik:**
- Her çalıştırma kendine ait çalışanlar oluşturur: `V<zaman>` (izin, puantaj ve bordro senaryoları için) ve `W<zaman>` (asgari ücretli).
- Bordro iş akışı için 2025–2026 içinde henüz kapanmamış ilk ay kullanılır. Bu yüzden `make verify` art arda çalıştırılabilir; her çalıştırma sıradaki ayı kapatır.
- 24 çalıştırmadan sonra (ya da istendiğinde) `make reset-demo` her şeyi baştan kurar.

## Demo hesapları

Parola: `.env` içindeki `DEMO_PASSWORD` (varsayılan `Demo.2026!`).

| Rol | E-posta | Görebildikleri / yapabildikleri |
|---|---|---|
| Çalışan | `calisan@bordroplus.test` | Kendi kartı ve maaşı, izin talebi, puantajı, onaylı bordrosu |
| Yönetici | `yonetici@bordroplus.test` | Bilgi Teknolojileri alt ağacı (≈235 kişi): izin ve puantaj görme/onaylama, vekâlet |
| İK uzmanı | `ik@bordroplus.test` | Tüm çalışanlar, maaş, organizasyon, izin/puantaj yönetimi, basım aktarımı |
| Bordro uzmanı | `bordro@bordroplus.test` | Bordro hesaplama, kalemler (avans/borç/prim), kontrole gönderme |
| Muhasebe | `muhasebe@bordroplus.test` | Bordro onayı, reddi ve dönem kapanışı, raporlar |
| Denetçi | `denetci@bordroplus.test` | Her şeyi okur (maaşlar maskeli), denetim kaydı, raporlar |

Simülatör ayrıca `S00001`–`S01500` sicil numaralı 1.500 çalışan üretir. Bunların giriş hesabı yoktur; yönetici ve İK hesaplarından görülürler.

## Doğrulama kimlikleri: nasıl denenir, beklenen sonuç

İstek örneklerinin hepsi [`requests.http`](requests.http) dosyasındadır (VS Code REST Client). `make verify` aşağıdaki senaryoların tamamını otomatik çalıştırır.

| Kimlik | Nasıl denenir | Beklenen sonuç |
|---|---|---|
| HEALTH | `GET /health` | `200`, `{"status":"ok","checks":{"database":"ok"}}`; veritabanı durursa `503` |
| AUTH | Yanlış parolayla `POST /api/v1/auth/login` | `401 invalid_credentials`; bilinmeyen e-postada da aynı yanıt |
| AUTH | Giriş → `POST /auth/refresh` (cookie) → `POST /auth/logout` → tekrar refresh | Yeni access token; çıkıştan sonra `401 invalid_refresh_token` |
| AUTHZ | Token'sız `GET /api/v1/auth/me`; çalışan ile `GET /audit-log`; bordro uzmanı ile `POST /employees` | `401 unauthorized`; `403 forbidden`; `403` |
| F-1 | İK: `POST /employees` (maaşla), `GET /employees/{id}`, `GET /org/units`, `GET /employees?limit=50&cursor=` | Kart oluşur; maaş İK'ya açık; ağaç 20+ birim; keyset sayfalama ilerler |
| IDEMP | Aynı `Idempotency-Key` ile `POST /employees` iki kez, sonra farklı gövdeyle | Aynı kayıt ve `Idempotent-Replayed: true`; farklı gövdede `422 idempotency_key_reused` |
| T-5 | Yönetici: `GET /employees?limit=200`; İK çalışanının kartı; denetçi ile bir kart | Yalnız `genel_mudurluk.bilgi_teknolojileri.*`; `403`; `salary_masked: true` |
| T-5 | Yönetici bugünü kapsayan `POST /delegations` (vekil: calisan) → calisan `GET /auth/me`, `GET /employees` → `POST /delegations/{id}/revoke` | Vekil `leave.approve.team` izni alır ve ekibi görür; iptalden sonra yalnız kendini görür |
| T-1 | İK: yeni çalışanın `GET /leave/employees/{id}/balance` | İşe girişi 2024-01-02 olan çalışana 2 yıldönümü × 14 gün = 28 gün |
| T-1 | 5'er günlük iki talep, ardından bakiyeyi aşan üçüncü talep | Üçüncü talep `409 insufficient_balance` (bekleyen talepler düşülür) |
| T-1 | İki talebin yönetici ve İK tarafından **aynı anda** onaylanması | İkisi de onaylanır; bakiye tam 10 gün azalır; asla negatife düşmez |
| T-1 | Onaylı iznin Çarşamba–Perşembe günlerine rapor (`sick`) girip onaylamak | 2 gün bakiyeye geri döner (`total_refunded: 2`) |
| T-1 | Tek gün `half_day: "morning"` | `days: 0.5` |
| F-2 | Çalışan kendi talebini oluşturur → yönetici `?for_approval=true` → çalışan onaylamaya çalışır → yönetici reddeder | Talep kuyrukta görünür; kendi onayı `403 self_approval`; durum `rejected` |
| T-2 | İK, 3–9 Kasım 2025 haftası için `POST /timesheet/punches`: çift giriş, eksik çıkış, Perşembe gece vardiyası, Cumartesi ve Pazar | `GET .../days` 2 anomali ve 2 düzeltme önerisi; gece vardiyası Perşembe ve Cuma'ya bölünür |
| T-2 | Öneriler onaylanmadan ay onayı | `409 unresolved_anomalies`; ham basımlar değişmez |
| F-3 | Önerileri onayla (eksik çıkış için `{"at":"…17:00:00+03:00"}`), sonra `POST .../months/2025-11/approve` | normal 2.160 dk, Pazar 225 dk, fazla mesai 0, 7 gün; kilitli aya basım `409 period_locked` |
| T-3 | Bordro uzmanı, dönemin ilk günü işe başlamış asgari ücretli için `POST /payroll/employees/{id}/calculate` | Net 2025'te `22104.67`, 2026'da `28075.50`; gelir ve damga vergisi 0 |
| T-3 | Aynı ay ve sonraki ay hesaplanır, ardından ay ortasından geçerli zam girilir ve `recalculate {"from":…}` | Satırlar tutarlı (brüt − kesintiler = net); her iki ayın kümülatif matrahı güncellenir |
| T-4 | `POST /payroll/periods/{p}/run` iki kez | 1.300+ çalışan < 10 sn (≈0,5–1 sn); ikinci çalıştırmada `changed: 0` ve aynı `digest` |
| F-4 | Bordro uzmanı `submit`, sonra kendisi `approve`; muhasebe `approve`; ardından tek çalışan `calculate` | `403`; `200 approved`; `409 period_frozen` |
| F-6 | Muhasebe `close`, tekrar `close`; bordro uzmanı kapalı dönemi `run` | `200 closed`; `409 invalid_transition`; `409 period_frozen` |
| F-5 | Kapanıştan sonra kapalı ayın ortasından geçerli zam; sonraki aya 3 taksitli borç; sonraki ayı `run` | Sonraki ay bordrosunda `Geçmiş dönem düzeltmesi (YYYY-MM)` ve `… (1/3)` satırları |
| F-7 | Denetçi: `GET /reports/payroll-summary?period=…` ve `&format=csv`; bordro uzmanı: `GET /payroll/employees/{id}/payslips/{p}?format=html` | Toplam kişi = dönemdeki kişi; CSV başlığı `Birim;Yol;Kişi;Brüt…`; yazdırılabilir bordro |
| AUDIT | Denetçi: `GET /audit-log?action=payroll.close`; çalışan: `GET /audit-log` | Kapanış kaydı listelenir; çalışana `403` |

## Kanıt komutları (zorlu görevler)

Birim ve entegrasyon testleri gerçek PostgreSQL üzerinde çalışır:

```bash
make testdb-up                   # 127.0.0.1:55432'de geçici Postgres
cd backend
```

| Görev | Komut | Ne kanıtlar |
|---|---|---|
| T-1 | `go test -v ./internal/leave/` | Kıdem bantları, yaş kuralı, artık yıl, takvim ve yarım gün; demo bakiyeleri elle hesapla aynı; rapor iadesi her iki sırada |
| T-1 (yarış) | `go test -v -run TestConcurrentApprovalsNeverOverdraw -count=10 ./internal/leave/` | 34 gün bakiyeye 10'ar günlük 5 eşzamanlı onay → her seferinde tam 3 onay, bakiye 4 |
| T-2 | `go test -v ./internal/timesheet/` | Eşleştirme, çift/eksik basım, öneri saati, mola, gece vardiyası, 45 saat, tatil, ay ataması, onay ve dondurma |
| T-2 (yarış) | `go test -v -run TestConcurrentCorrectionApproval -count=10 ./internal/timesheet/` | Aynı öneriye 5 eşzamanlı onay → 1 başarı, tek düzeltme basımı |
| T-3 | `go test -v ./internal/payroll/ -run 'MinimumWage\|Cumulative\|Ordinary\|Retroactive\|MidMonth'` | Yayımlanan resmî rakamlar (28.075,50; 40.214,03; 4.211,33; 4.537,75; 57.881,23), dilim geçişi, geçmiş ay düzeltmesi |
| T-4 | `go test -v ./internal/payroll/ -run 'Period\|Rerun\|PriorPeriod\|Backdated\|1500'` | Durum makinesi, veritabanı seviyesinde dondurma, determinizm, düzeltme satırı, 1.506 çalışan ≈1 sn |
| T-5 | `go test -v ./internal/server/ -run 'EmployeeListScopes\|EmployeeCardAccess\|Delegation\|ConcurrentOverlapping\|UnitMove\|SalaryEncrypted'` | Rol bazında kapsam, maaş maskeleme, vekâlet, eşzamanlı çakışan vekâlet, şifreli maaş |
| Tümü | `make test` | 120+ test |

## Arayüzde elle denenecek senaryolar

Arayüz: http://localhost:3000. Giriş ekranındaki "Demo hesapları" bölümü e-postayı doldurur.

| # | Hesap | Adımlar | Beklenen |
|---|---|---|---|
| 1 | herhangi biri | Panel'i açık bırakın | "Sistem durumu" 10 sn'de bir yenilenir; "Bugünkü basım" ve simülatörün son çalışma saati ilerler (canlı) |
| 2 | calisan | Menü | Yalnızca Panel, Organizasyon, İzinler, Puantaj, Bordrolarım görünür; adres çubuğuna `/payroll` yazınca "yetkiniz yok" |
| 3 | calisan | İzinler → Yeni talep: gelecek bir hafta, sonra yarım gün (sabah) | İş günü sayısı gösterilir (hafta sonu/tatil düşülmez; yarım gün 0,5); "Kullanılabilir" bekleyen kadar azalır |
| 4 | yonetici | İzinler → Onay bekleyenler → Onayla / Reddet (gerekçe zorunlu) | Talep kuyruktan düşer; çalışanın Hareketler (defter) tablosunda kullanım satırı |
| 5 | yonetici | Kendi izin talebini oluşturup onaylamayı deneyin | Talep yöneticinin kuyruğunda görünmez; İK'nın kuyruğunda görünür |
| 6 | yonetici | İzinler → Vekâlet: bugün için calisan'ı seçip vekâlet verin; calisan ile tekrar girin | calisan'ın üst çubuğunda "Vekil" etiketi, Personel menüsü ve ekip kuyruğu görünür; vekâlet iptal edilince kaybolur |
| 7 | yonetici | Personel | Yalnızca Bilgi Teknolojileri alt ağacı (≈235 kişi); kartlarda maaş "maskeli"; Organizasyon'da kendi ağacı "Ekibim" ile işaretli |
| 8 | ik | Personel → Yeni çalışan (maaşlı) → kartta "Maaş geçmişi"ne geçmiş tarihli yeni maaş | Kart açılır, maaş görünür; geçmiş tarihli maaş için bilgi notu |
| 9 | yonetici | Puantaj → Ekip puantajı → bir simülatör çalışanı (ör. Gece Vardiyası) → önceki ay | Haftalık döküm, gece vardiyalarının iki güne bölünmüş dağılımı; tutarsız basımlar ve öneri saatleri |
| 10 | yonetici | Puantaj → Düzeltme önerileri → Onayla / Reddet | Öneri kuyruktan düşer; ilgili ay tekrar açıldığında anomali sayısı azalır; anomali kalmayınca "Ayı onayla" etkinleşir |
| 11 | calisan | Puantaj → Giriş bas / Çıkış bas | Sunucu saatiyle kayıt; Panel'deki "Bugünkü basım" artar |
| 12 | bordro | Bordro → Dönem hesapla (ilk açık ay) | ≈1.300 çalışan, süre (ms), paralel işçi sayısı, dönem özeti; ikinci kez "0 değişti" ve aynı özet |
| 13 | bordro → muhasebe | Kontrole gönder (bordro) → muhasebe ile Onayla / Reddet → Dönemi kapat | Butonlar yalnızca yetkili role görünür; kapalı dönemde hesap düğmeleri kaybolur |
| 14 | bordro | Bordro → Çalışan bordrosu → çalışan seç → ay satırına tıkla → "Yazdır / PDF" | Satır satır formüllü bordro (SGK matrahı, kümülatif matrah, asgari ücret istisnası); yazdırılabilir sayfa |
| 15 | bordro | Avans / borç / prim → 3 taksitli borç; aylık maaşın dörtte birini aşan avans | Taksit takvimi; aşan avansta hata mesajı |
| 16 | calisan | Bordrolarım | Yalnızca onaylı/kapalı dönemler; taslak dönemler listelenmez |
| 17 | muhasebe / denetci | Raporlar → Bordro özeti → CSV indir; İşveren maliyeti; İzin kullanımı; Fazla mesai | Birim toplamları (bireysel maaş yok); CSV Türkçe karakterlerle açılır |
| 18 | denetci | Denetim kaydı → işlem: `payroll.close` | Kim, ne zaman, eski/yeni değer |
| 19 | herhangi biri | Tarayıcıyı ~390 px genişliğe daraltın | Menü ☰ ile açılır; tablolar etiketli kartlara dönüşür; yatay kaydırma yok |
| 20 | herhangi biri | İki sekmede aynı hesapla 15 dakikadan uzun kalın, sonra iki sekmede de işlem yapın | Oturum düşmez: refresh tek uçuşta yapılır ve sekmeler arasında paylaşılır |

Bu senaryoların bir kısmı tarayıcıda otomatik de çalışır (Node gerekir):

```bash
make e2e   # her rol × her sayfa × masaüstü/telefon (154 görünüm) + izin akışı (talep → onay → İK iptali)
```
