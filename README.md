# BordroPlus — İnsan Kaynakları, İzin ve Bordro Sistemi

**Geliştiren:** Görkem Araz

1.500 çalışanlı bir şirket için personel kartı, organizasyon ağacı, yıllık izin, puantaj/fazla mesai ve aylık bordroyu yöneten sistem.
Go API + PostgreSQL 16 + React arayüzü + veri üretici (simülatör), hepsi Docker Compose ile tek komutla çalışır.

> Bu belge geliştirme ilerledikçe güncellenmektedir.

## Kurulum

```bash
git clone <repo-adresi> && cd <repo>
cp .env.example .env
docker compose up --build
```

| Servis | Adres |
|---|---|
| Arayüz (React, nginx) | http://localhost:3000 |
| API (Go) | http://localhost:8080 |
| Sağlık | http://localhost:8080/health |
| Prometheus metrikleri | http://localhost:8080/metrics |
| PostgreSQL | yalnızca compose ağı içinde (`db:5432`), host'a açılmaz |

Faydalı komutlar (`make help`):

| Komut | Açıklama |
|---|---|
| `make up` | Yığını derler, başlatır ve tüm servisler `healthy` olana kadar bekler |
| `make test` | Go testleri (`-race`) ve frontend tip kontrolü |
| `make cover` | Go kapsam raporu |
| `make clean` | Yığını durdurur ve veritabanı hacmini siler |

### Demo hesapları

Migrate servisi ilk açılışta her rol için bir hesap oluşturur. Parola `.env` içindeki `DEMO_PASSWORD` değeridir (varsayılan: `Demo.2026!`).

| Rol | E-posta | Kişi (kurgusal) | Birim |
|---|---|---|---|
| Çalışan | `calisan@bordroplus.test` | Can Demir | Yazılım Geliştirme |
| Yönetici | `yonetici@bordroplus.test` | Elif Kaya | Bilgi Teknolojileri (birim yöneticisi) |
| İK uzmanı | `ik@bordroplus.test` | Selin Arslan | İnsan Kaynakları |
| Bordro uzmanı | `bordro@bordroplus.test` | Burak Şahin | Bordro |
| Muhasebe | `muhasebe@bordroplus.test` | Deniz Aydın | Muhasebe |
| Denetçi | `denetci@bordroplus.test` | Ece Koç | İç Denetim |

Seed idempotenttir: her açılışta yalnızca eksik kayıtları ekler, değiştirilmiş parolalara dokunmaz.

## Mimari Özeti

```
 tarayıcı ──► frontend (nginx :3000) ──/api, /health──► api (Go :8080) ──► db (PostgreSQL 16)
                                                          ▲                         ▲
                                       migrate (tek seferlik: goose + demo seed)    │
                                       simulator (2 yıllık geçmiş + canlı basımlar) ┘
                                       verify (profil: make verify → PASS/FAIL)
```

- **db**: `postgres:16-alpine`, yalnızca iç ağda. `pg_isready` sağlık kontrolü.
- **migrate**: gömülü (embed) goose migrasyonlarını uygular ve tamamlanınca çıkar. API, `service_completed_successfully` koşuluyla bunu bekler.
- **api**: Go + chi + pgx. `/health` (DB ping dahil), `/metrics` (Prometheus), JSON log, istek bazlı zaman aşımı, düzgün kapanış.
- **simulator**: 1.500 çalışanlık şirketi ve 2 yıllık geçmişi yükler, sonra bugünün basımlarını canlı üretir (bkz. Veri Üretici).
- **verify** (yalnız `--profile verify`): uçtan uca doğrulama.
- **frontend**: React + Vite + TypeScript, `nginx-unprivileged` ile servis edilir. API'ye aynı origin üzerinden ters vekil (reverse proxy) ile gider.

Tüm imajlar çok aşamalı derlenir ve root olmayan kullanıcıyla çalışır (Go: `distroless/static:nonroot`, frontend: uid 101).

### Teknoloji seçimleri

| Katman | Seçim | Gerekçe |
|---|---|---|
| HTTP | `chi` | `net/http` uyumlu, middleware zinciri sade, rota desenleri metrik etiketi olarak kullanılabiliyor |
| DB sürücüsü | `pgx/v5` + `pgxpool` | PostgreSQL'e özgü tipler (ltree, numeric, range) ve performans |
| Migrasyon | `goose` (embed) | SQL dosyaları ikili dosyaya gömülür; ayrı araç kurmaya gerek kalmaz |
| Log | `log/slog` (JSON) | Standart kütüphane; hassas alanlar (parola, token) otomatik maskelenir |
| Frontend | React + Vite + TS + TanStack Query | Rol bazlı çok ekranlı panel için olgun ekosistem. Yükleniyor/boş/hata durumlarını TanStack Query standart biçimde verir. Vite hızlı derler ve çıktı nginx ile statik servis edilir |

Ayrıntılı karar kayıtları: [DECISIONS.md](DECISIONS.md)

## Arayüz

React + Vite + TypeScript + TanStack Query, `nginx-unprivileged` ile servis edilir; API'ye aynı origin üzerinden ters vekil ile gider (ADR-003).

- **Rol bazlı menü ve ekranlar:** menü ve düğmeler kullanıcının izin listesinden (`/auth/me`) türetilir. Yine de asıl yetki kontrolü sunucudadır; arayüz yalnızca gösterilmeyecek şeyi gizler.
- **Ekranlar:** panel (canlı sistem durumu), personel listesi ve kartı, organizasyon ağacı, izinler (bakiye, defter, talep, onay kuyruğu, vekâlet), puantaj (web saati, aylık ve haftalık döküm, gece vardiyası dağılımı, anomaliler, düzeltme kuyruğu, ay onayı), bordro (dönem iş akışı, çalışan bordrosu, kalemler), bordrolarım, raporlar ve denetim kaydı.
- **Durumlar:** her veri görünümü ortak `Async` bileşeninden geçer; yükleniyor, boş ve hata durumları her ekranda aynıdır. Yetki hatası "yetkiniz yok" olarak, doğrulama hataları alan bazında gösterilir.
- **Mobil:** 760 px altında menü açılır menüye, tablolar etiketli kartlara dönüşür; yatay kaydırma olmaz (`make e2e` ile 390 px'te denetlenir).
- **Oturum:** access token yalnızca bellekte tutulur, refresh token `HttpOnly` cookie'dedir. 401 alan tüm istekler tek bir refresh'i bekler (single-flight); sekmeler arasında refresh bir Web Lock ile sıraya girer ve yeni token `BroadcastChannel` ile paylaşılır. Bu sayede iki sekme aynı refresh token'ı sunup birbirinin oturumunu düşürmez (ADR-004, `e2e/tabs.mjs`).
- **Para gösterimi:** tutarlar API'den ondalık metin olarak gelir ve metinden biçimlenir (`45.000,50 ₺`); arayüzde para üzerinde aritmetik yapılmaz.
- **Durum yönetimi:** idempotent POST'lar (çalışan, izin talebi, kalem) kullanıcı eylemi başına bir `Idempotency-Key` gönderir; çift tıklama çift kayıt üretmez.

## Kimlik Doğrulama ve Yetki

| Uç nokta | Açıklama |
|---|---|
| `POST /api/v1/auth/login` | `{"email","password"}` → access token (gövdede) + refresh token (cookie) |
| `POST /api/v1/auth/refresh` | Refresh cookie'sini döndürür (rotation), yeni access token verir |
| `POST /api/v1/auth/logout` | Refresh token ailesini iptal eder, cookie'yi siler |
| `GET /api/v1/auth/me` | Oturumdaki kullanıcı, rolü ve izin listesi |
| `GET /api/v1/audit-log` | Denetim kaydı, yalnızca denetçi; `?limit=&cursor=&entity_type=&entity_id=&actor_user_id=&action=` |

- **Parola:** argon2id (19 MiB, 2 tur, 1 iş parçacığı; OWASP profili), PHC biçiminde saklanır. Veritabanı `CHECK` kısıtı argon2id dışındaki özetleri reddeder.
- **Access token:** 15 dakikalık HS256 JWT. Algoritma, issuer ve son kullanma tarihi zorunlu tutulur; `alg=none` reddedilir.
- **Refresh token:** rastgele 256 bit, veritabanında yalnızca SHA-256 özeti saklanır. Her kullanımda yenisiyle değiştirilir. Kullanılmış bir token tekrar gelirse çalınmış sayılır ve tüm token ailesi iptal edilir. Token, `/api/v1/auth` yoluyla sınırlı `HttpOnly` + `SameSite=Strict` bir cookie'de taşınır.
- **Kaba kuvvet koruması:** art arda 5 hatalı denemeden sonra hesap 15 dakika kilitlenir. Yanlış parola, bilinmeyen e-posta, kilitli ve pasif hesap aynı 401 yanıtını alır; bilinmeyen e-postada da sahte bir özet doğrulanır, böylece yanıt süresi hesabın var olup olmadığını ele vermez.
- **Yetki:** her istekte hesap veritabanından yeniden okunur; pasifleştirme ve rol değişikliği token süresi dolmadan hemen etkili olur. Uç noktalar rol yerine izin (`audit.read`, `payroll.approve` …) ister. Kimlik yoksa 401, izin yoksa 403 döner.
- **Denetim kaydı:** giriş, hatalı giriş, çıkış, refresh token yeniden kullanımı ve seed işlemleri `audit_log` tablosuna yazılır. Tablo eklemeli-yalnızdır: `UPDATE`, `DELETE` ve `TRUNCATE` trigger tarafından reddedilir.

### Yetki matrisi

`.self` kendi kaydı, `.team` yöneticinin organizasyon alt ağacı, `.all` tüm şirket demektir. Matrisin tek kaynağı `backend/internal/auth/rbac.go` dosyasıdır ve birim testleriyle korunur.

| İzin | Çalışan | Yönetici | İK | Bordro | Muhasebe | Denetçi |
|---|:-:|:-:|:-:|:-:|:-:|:-:|
| Kendi kartı, izin talebi, puantajı, bordrosu | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ |
| Ekip izin/puantaj görme ve onaylama | | ✓ | | | | |
| Tüm çalışanları görme | | | ✓ | ✓ | ✓ | ✓ |
| Çalışan ve organizasyon düzenleme | | | ✓ | | | |
| Vekâlet verme (kendi kapsamı / herkes için) | | kendi | herkes | | | |
| İzin/puantaj yönetimi (tüm şirket) | | | ✓ | | | |
| Maaşı açık görme | | | ✓ | ✓ | | |
| Bordro hesaplama, ek kalemler | | | | ✓ | | |
| Bordro onayı ve dönem kapanışı | | | | | ✓ | |
| Raporlar | | | ✓ | ✓ | ✓ | ✓ |
| Denetim kaydı | | | | | | ✓ |

Görevler ayrılığı: bordroyu hesaplayan rol onaylayamaz ve dönemi kapatamaz. Bu kural bir testle güvence altındadır.

## Organizasyon, Personel Kartı ve Yetki Kapsamı (F-1, T-5)

| Uç nokta | İzin | Açıklama |
|---|---|---|
| `GET /api/v1/org/units` | `org.read` | Tüm birimler: düz liste, `parent_id`, ltree `path`, yönetici, aktif çalışan sayısı |
| `POST /api/v1/org/units` | `org.write` | Birim ekler (`Idempotency-Key` destekli) |
| `PATCH /api/v1/org/units/{id}` | `org.write` | Ad, kod, üst birim (taşıma) veya yönetici değiştirir; alt ağaç birlikte taşınır |
| `GET /api/v1/employees` | kapsam | `?q=&org_unit_id=&include_terminated=&limit=&cursor=` |
| `GET /api/v1/employees/{id}` | kapsam | Kapsam dışındaysa 403, kayıt yoksa 404 |
| `POST /api/v1/employees` | `employee.write` | İsteğe bağlı ilk maaşla birlikte (`Idempotency-Key` destekli) |
| `PATCH /api/v1/employees/{id}` | `employee.write` | Ad, unvan, birim (transfer), işten çıkış tarihi |
| `GET /api/v1/employees/{id}/salaries` | `salary.read` veya kendisi | Maaş geçmişi |
| `POST /api/v1/employees/{id}/salaries` | `salary.write` | `{"amount":"72000.50","valid_from":"2026-07-01"}` |
| `GET /api/v1/delegations` | herkes | İK hepsini, diğerleri verdiği ya da aldığı vekâletleri görür; `?active=true` |
| `POST /api/v1/delegations` | `delegation.manage.self` / `.all` | Yönetici kendi kapsamını, İK herhangi bir yöneticininkini devreder |
| `POST /api/v1/delegations/{id}/revoke` | aynı | Vekâleti hemen sonlandırır |

**Kapsam SQL'de uygulanır.** Satırlar Go tarafında süzülmez; her sorgu aynı koşulu taşır:

```sql
WHERE ($1::bool                     -- .all izni
       OR u.path <@ ANY($2::ltree[]) -- yöneticinin (ve vekâlet verenlerin) alt ağaçları
       OR e.id = $3)                  -- kendisi
```

`path <@` sorgusu `org_units_path_gist` GiST dizinini kullanır. 1.500 çalışanla alınan `EXPLAIN (ANALYZE)` çıktısı simülatör aşamasında buraya eklenecek.

**Vekâlet.** Yönetici izindeyken kapsamını belirli bir tarih aralığı için bir vekile devreder. Vekilin rolü ne olursa olsun, vekâlet süresince yalnızca *ekip* izinlerini alır (ekibi görme, izin ve puantaj onayı); şirket geneli izinler hiçbir zaman devredilmez. Vekil kapsamı başkasına aktaramaz. Kapsam token'dan değil, her istekte veritabanından hesaplanır; vekâlet başlar başlamaz ya da iptal edildiği anda etkili olur. Aynı yöneticinin çakışan iki vekâleti `EXCLUDE USING gist` kısıtıyla engellenir; eşzamanlı isteklerde yalnızca biri kabul edilir (`TestConcurrentOverlappingDelegations`).

**Maaş gizliliği.** Maaşlar API tarafında AES-256-GCM ile şifrelenir; veritabanı açık tutarı hiç görmez. Şifreli değer GCM ek verisiyle (AAD) `(employee_id, valid_from)` ikilisine bağlıdır; bir satırın şifreli değeri başka bir satıra kopyalanırsa çözülemez. Maaşı açık görebilenler `salary.read` iznine sahip roller (İK, bordro) ve çalışanın kendisidir; diğerleri `"salary_masked": true` alır. Tutarlar denetim kaydına yazılmaz, yalnızca değişikliğin yapıldığı kaydedilir.

**Görev 5 kanıtı:**

```bash
make testdb-up
cd backend && go test -v -run 'TestEmployeeListScopes|TestEmployeeCardAccess|TestDelegation|TestConcurrentOverlapping|TestUnitMove|TestSalaryEncrypted' ./internal/server/
```

## Yıllık İzin (F-2, T-1)

| Uç nokta | İzin | Açıklama |
|---|---|---|
| `GET /api/v1/leave/employees/{id}/balance` | kapsam | Bakiye, bekleyen, kullanılabilir, devreden, son ve sonraki hakediş |
| `GET /api/v1/leave/employees/{id}/ledger` | kapsam | Tüm hareketler ve yürüyen bakiye |
| `GET /api/v1/leave/requests` | kapsam | `?status=&employee_id=&for_approval=true&limit=&cursor=` |
| `GET /api/v1/leave/requests/{id}` | kapsam | |
| `POST /api/v1/leave/requests` | kendisi / `leave.manage` | `{"leave_type":"annual","start_date","end_date","half_day":"morning"}` (`Idempotency-Key` destekli) |
| `POST /api/v1/leave/requests/{id}/approve` | ekip yöneticisi, vekil, İK | Kendi talebini onaylayamaz |
| `POST /api/v1/leave/requests/{id}/reject` | aynı | `{"note":"…"}` |
| `POST /api/v1/leave/requests/{id}/cancel` | sahibi (bekleyen) / İK (onaylı dahil) | Onaylı izin iptalinde günler iade edilir |
| `GET /api/v1/leave/holidays?year=` | herkes | |
| `POST /api/v1/leave/holidays` | `leave.manage` | Dini bayram tarihleri açıklandıkça eklenir |

**Hakediş kuralları** (4857 sayılı İş Kanunu md. 53, `internal/leave/engine.go`):

| Kıdem (yıldönümünde tamamlanan yıl) | Gün |
|---|---|
| 1–5 yıl (5 dahil) | 14 |
| 5'ten fazla, 15'ten az | 20 |
| 15 ve üzeri | 26 |
| 18 yaş ve altı ya da 50 yaş ve üstü | en az 20 |

- Hakediş her işe giriş yıldönümünde doğar; yaş, yıldönümü günündeki yaştır. İşten çıkış tarihinden sonra hakediş doğmaz. 29 Şubat'ta işe girenlerin yıldönümü artık olmayan yıllarda 1 Mart'tır.
- **Gün sayımı:** Pazartesi–Cuma çalışılır. Hafta sonu ve resmî tatiller izinden düşülmez. Arife ve 28 Ekim gibi yarım günlük tatillerde yalnızca sabah iş günüdür. Yarım gün izin sabah ya da öğleden sonra olabilir; arifenin öğleden sonrası zaten tatil olduğu için orada alınan yarım gün izin 0 gün tutar.
- **Birim:** Tüm hesaplar tam sayı "yarım gün" ile yapılır (1 gün = 2). Ondalık sayı kullanılmaz.
- **Devreden izin:** Kullanılmayan izin yanmaz (kanunen). Bakiye, eklemeli-yalnız `leave_ledger` tablosundaki hareketlerin toplamıdır. "Devreden" değeri, son hakedişten önceki bakiyedir.
- **İzin sırasında rapor:** Onaylı yıllık izinle çakışan onaylı rapor günleri izinden sayılmaz ve bakiyeye iade edilir. Önce raporun mu yoksa iznin mi onaylandığı sonucu değiştirmez; iade, iznin düşülen gününden fazla olamaz. Onaylı bir rapor iptal edilemez, çünkü verilen iadeler ona bağlıdır.
- **Eşzamanlılık:** Bakiyeyi değiştiren her işlem çalışanın `leave_accounts` satırını `SELECT … FOR UPDATE` ile kilitler. Kilit sırası her yolda aynıdır (önce talep, sonra hesap), bu yüzden kilitlenme oluşmaz. Ek olarak `CHECK (balance_half_days >= 0)` kısıtı vardır; kod hatalı olsa bile bakiye negatife düşemez.
- **Hakedişler tembel işlenir:** Ayrı bir zamanlanmış görev yoktur. Bakiyeye dokunan her işlem, vadesi gelmiş yıldönümü hakedişlerini önce deftere yazar. Benzersiz dizin bu işlemi idempotent yapar.

**Görev 1 kanıtı:**

```bash
make testdb-up
cd backend
go test -v ./internal/leave/                     # kurallar, takvim, demo bakiyeleri, rapor iadesi
go test -v -run TestConcurrentApprovalsNeverOverdraw -count=10 ./internal/leave/
```

`TestConcurrentApprovalsNeverOverdraw`: 34 gün bakiyesi olan çalışanın 10'ar günlük 5 talebi aynı anda onaylanır. Tam olarak 3 talep onaylanır, 2'si `insufficient_balance` alır ve bakiye 4 günde kalır. Her testin sonunda tüm hesaplar için "bakiye = defter toplamı ve ≥ 0" koşulu kontrol edilir.

## Puantaj ve Fazla Mesai (F-3, T-2)

| Uç nokta | İzin | Açıklama |
|---|---|---|
| `POST /api/v1/timesheet/punches` | `timesheet.manage` | Cihaz ya da elle basım aktarımı, en fazla 5.000 basım, hepsi ya da hiçbiri (`Idempotency-Key` destekli) |
| `POST /api/v1/timesheet/clock` | kendisi | Web saati: sunucu zamanıyla `{"direction":"in"}` |
| `GET /api/v1/timesheet/employees/{id}/days?from=&to=` | kapsam | Günlük oturumlar, segmentler, anomaliler ve düzeltme önerileri (en fazla 62 gün) |
| `GET /api/v1/timesheet/employees/{id}/months/{YYYY-MM}` | kapsam | Haftalık döküm, toplamlar, yıllık fazla mesai ve 270 saat uyarısı |
| `POST /api/v1/timesheet/employees/{id}/months/{YYYY-MM}/approve` | ekip yöneticisi, vekil, İK | Biten ve açık anomalisi olmayan ayı dondurur |
| `GET /api/v1/timesheet/corrections` | kapsam | `?status=proposed&for_approval=true&employee_id=` |
| `POST /api/v1/timesheet/corrections/{id}/approve` | ekip yöneticisi, vekil, İK | İsteğe bağlı `{"at":"2026-11-05T18:30:00+03:00"}` ile saat düzeltilir |
| `POST /api/v1/timesheet/corrections/{id}/reject` | aynı | |

**Basımlar değişmez, düzeltmeler önerilir.** Ham basımlar eklemeli-yalnızdır. Hesap, "etkin basımlar" üzerinden yapılır: ham basımlardan onaylı silme önerileri çıkarılır, onaylı ekleme önerileri `source = 'correction'` ile ayrı satır olarak eklenir. Motor (`internal/timesheet/engine.go`) her tutarsızlık için kendiliğinden bir öneri üretir:

| Durum | Tespit | Öneri |
|---|---|---|
| Çift basım | Aynı yönde, 5 dakika içinde ikinci basım | İkincisini yok say (ilki kalır) |
| Eksik çıkış | Girişten sonra yine giriş, ya da 16 saati geçen açık giriş | Girişten bir "tipik vardiya" sonra çıkış ekle |
| Eksik giriş | Önünde giriş olmayan çıkış | Çıkıştan bir tipik vardiya önce giriş ekle |
| 16 saati aşan oturum | Unutulmuş çıkış ve unutulmuş giriş | İkisi birden |

Tipik vardiya, çalışanın temiz oturumlarının medyanıdır (en az 3 oturum varsa); yoksa 9 saattir. Önerilen saat komşu basımı asla geçmez. **Onaylanmamış anomaliye ait süre toplamlara girmez** ve o ay onaylanamaz. Sonradan gelen gerçek bir basım anomaliyi çözerse öneri "obsolete" olur ve uygulanamaz.

**Hesap kuralları** (kural seti `TS-2026.1`):

- **Ara dinlenme (md. 68):** günlük toplam 4 saate kadar 15 dakika, 7.5 saate kadar 30 dakika, daha fazlasında 1 saat. Gün içinde zaten verilmiş aralar bu süreden düşülür; kalan kısım günün en uzun oturumunun ortasından kesilir.
- **Gece vardiyası:** oturum yerel gece yarısında bölünür. Oturumun iş günü, girişin yapıldığı gündür; dakikalar ise ait oldukları takvim gününe yazılır.
- **Sınıflandırma:** Pazartesi–Cumartesi normal gün, Pazar hafta tatili, resmî tatil ise tatildir (yarım günlük tatillerde 13:00'ten sonrası).
- **Fazla mesai (md. 41, 63):** haftanın normal dakikalarından 45 saati aşan kısım fazla mesaidir. Hafta tatili ve resmî tatil dakikaları kendi katsayısıyla ödenir ve 45 saate sayılmaz. Örnek: tatil haftasında 3.300 dakika çalışan birinin 2.280 dakikası normaldir, 1.020 dakikası tatildir ve fazla mesaisi yoktur.
- **Katsayılar:** fazla mesai %150, hafta tatili %150, resmî tatil %200. Yıllık fazla mesai 270 saati aşarsa ay özetinde uyarı verilir.
- **Ay ataması:** bir hafta Pazar gününün düştüğü bordro ayına yazılır (Kasım 2026 = 26 Ekim – 29 Kasım). Böylece haftalık 45 saat kuralı iki aya bölünmez; Pazar 22:00'de başlayıp Pazartesi biten bir vardiyanın Pazar kısmı bir aya, Pazartesi kısmı sonraki aya düşer.
- **Ay onayı:** onaylanan ayın toplamları anlık görüntü (snapshot) olarak saklanır ve bordro bunu kullanır. Bu aya yeni basım, düzeltme ya da ikinci bir onay girilemez. Bir çalışanın puantajına yazan tüm işlemler çalışan bazlı bir advisory lock ile sıraya girer; kilit, satır kilitlerinden önce alınır.

**Görev 2 kanıtı:**

```bash
make testdb-up
cd backend
go test -v ./internal/timesheet/      # motor tabloları + öneri/onay/ay kapanışı uçtan uca
go test -v -run 'Timesheet|WebClock' ./internal/server/
```

## Bordro Hesaplama Motoru (T-3)

| Uç nokta | İzin | Açıklama |
|---|---|---|
| `GET /api/v1/payroll/rule-sets` | `payroll.read.all` | Zaman geçerlilikli parametre setleri |
| `POST /api/v1/payroll/employees/{id}/calculate` | `payroll.calculate` | `{"period":"2026-03"}`; yılın eksik önceki aylarını da hesaplar |
| `POST /api/v1/payroll/employees/{id}/recalculate` | `payroll.calculate` | `{"from":"2026-03"}`; o aydan yılın son hesaplanan ayına kadar, ay ay önce/sonra farkı döner |
| `GET /api/v1/payroll/employees/{id}/results?year=` | `payroll.read.all` veya kendisi | Aylık özet |
| `GET /api/v1/payroll/employees/{id}/results/{YYYY-MM}` | `payroll.read.all` veya kendisi | Satır satır açıklamalı bordro ve hesabın girdileri |

Yönetici, ekibinin bordrosunu göremez. Tutarlar API'de her zaman ondalık metin olarak döner (`"50931.18"`).

**Parametreler** (`payroll_rule_sets`, dönem başına göre seçilir; çakışan geçerlilik aralıkları `EXCLUDE` kısıtıyla engellenir):

| | TR-2025.1 | TR-2026.1 |
|---|---|---|
| Brüt asgari ücret | 26.005,50 | 33.030,00 |
| SGK taban / tavan | 26.005,50 / 195.041,25 (7,5 kat) | 33.030,00 / 297.270,00 (9 kat, 7566 s.K.) |
| SGK işçi + işsizlik işçi | %14 + %1 | %14 + %1 |
| SGK işveren (indirim) + işsizlik işveren | %20,75 (−4 puan) + %2 | %21,75 (−2 puan) + %2 |
| Damga vergisi | %0,759 | %0,759 |
| Ücret gelirleri tarifesi | 158.000 / 330.000 / 1.200.000 / 4.300.000 (GVGT 329) | 190.000 / 400.000 / 1.500.000 / 5.300.000 (GVGT 332) |

İşveren indirimi 5510 s.K. md. 81/(ı) kapsamında, imalat dışı sektör için uygulanmıştır. Yıl ortasında bir parametre değişirse (örneğin asgari ücret ara zammı) yeni bir satır eklenir; motor her ay için o ay geçerli olan seti kullanır.

**Hesap sırası** (`internal/payroll/engine.go`):

1. **Brüt:** temel ücret + fazla mesai + hafta tatili + resmî tatil + vergiye/SGK'ya tabi ek ödemeler. Saatlik ücret aylık ücret ÷ 225'tir. Resmî tatil çalışmasında aylık ücret tatil gününü zaten ödediği için yalnızca %100 ek ödenir.
2. **SGK matrahı:** SGK'ya tabi brüt, gün sayısına oranlanmış taban ile tavan arasına sıkıştırılır. İşçi payı %14, işsizlik işçi payı %1.
3. **Gelir vergisi matrahı:** vergiye tabi brüt − SGK işçi − işsizlik işçi. Bu ayın vergisi `T(önceki kümülatif + matrah) − T(önceki kümülatif)` olarak hesaplanır. T yıllık tarifeyi tam sayı aritmetiğiyle ve yuvarlamadan uygular; yuvarlama farka yalnızca bir kez yapılır.
4. **Asgari ücret istisnası (GVK 23/18):** asgari ücretin kendi kümülatif matrahı üzerinden aynı yöntemle hesaplanan vergi kadar; hesaplanan vergiyi geçemez. Asgari ücretin kümülatif matrahı kişiye değil yıla bağlıdır.
5. **Damga vergisi:** brüt × %0,759, eksi asgari ücretin damga vergisi.
6. **Net:** brüt − SGK işçi − işsizlik işçi − gelir vergisi − damga vergisi − netten kesintiler (avans, borç taksiti).
7. **İşveren maliyeti:** brüt + SGK işveren (indirim düşülmüş) + işsizlik işveren.

**Yuvarlama kuralı:** tüm tutarlar tam sayı kuruştur, oranlar 1/100.000 birimidir; float kullanılmaz. Her kalem bir kez, kuruşa yarım-yukarı yuvarlanır. Toplamlar yuvarlanmış kalemlerin toplamıdır. Bordronun her satırı kendi formülünü taşır (`"140000.00 × %14"`).

**Girdiler** (`internal/payroll/service.go`):

- **Temel ücret, 30 gün kuralıyla:** tam ayda aylık ücret ödenir; ay içinde zam olursa günlere göre ağırlıklandırılır. Kısmi ayda çalışılan her gün için aylık ÷ 30 ödenir. Onaylı ücretsiz izin günleri ücretten ve SGK gününden düşülür.
- **Fazla mesai:** yalnızca **onaylanmış (dondurulmuş) puantaj ayından** gelir. Puantaj onaylanmamışsa fazla mesai bordroya girmez ve bu durum bordro notunda belirtilir.
- **Kümülatif matrah:** yılın önceki ayının kayıtlı sonucundan alınır. Önceki ay eksikse önce o hesaplanır. İlk maaş kaydından önceki aylar (sisteme geçiş öncesi) bordro dışı sayılır.

**Geçmiş ay düzeltmesi:** Haziran'da Mart'tan geçerli bir zam girilirse `recalculate {"from":"2026-03"}` Mart'tan Haziran'a kadar her ayı sırayla yeniden hesaplar. Böylece Nisan–Haziran'ın kümülatif matrahı ve dilimi düzelir. Girdisi değişmeyen ay olduğu gibi kalır. Aynı kural seti sürümüyle tekrar çalıştırıldığında hiçbir şey değişmez (`input_hash`). Kapanmış dönemler bu yolla değiştirilemez; onların düzeltmesi bir sonraki bölümde anlatılıyor.

**Doğrulama:** Motor, yayımlanmış resmî rakamlarla regresyon testine bağlıdır:

| Test | Beklenen (yayımlanan) |
|---|---|
| 2026 asgari ücret neti / işveren maliyeti (imalat dışı) | 28.075,50 / 40.214,03 |
| 2025 asgari ücret neti | 22.104,67 |
| 2026 asgari ücret GV istisnası Ocak–Haziran / Temmuz | 4.211,33 / 4.537,75 |
| 2026 yıllık asgari ücret GV istisnası | 57.881,23 |
| 2026 asgari ücret DV istisnası | 250,70 |

```bash
cd backend && go test -v ./internal/payroll/
```

**Bilinen sınırlamalar:**
- Aynı yıl içinde önceki işverende oluşan kümülatif matrah sisteme girilemiyor.
- SGK tavanını aşan ek ödemenin sonraki iki aya devri (devreden tavan) uygulanmıyor.
- Rapor günlerinde SGK'nın ödediği geçici iş göremezlik ödeneği modellenmiyor; rapor günleri işveren tarafından ücretli sayılıyor.
- Engellilik indirimi uygulanmıyor.

## Bordro İş Akışı ve Dönem Kapanışı (F-4, F-6, T-4)

| Uç nokta | İzin | Geçiş |
|---|---|---|
| `GET /api/v1/payroll/periods`, `/periods/{YYYY-MM}` | `payroll.read.all` | |
| `POST /api/v1/payroll/periods/{p}/run` | `payroll.calculate` | Taslak dönemi paralel hesaplar (dönem yoksa açar) |
| `POST /api/v1/payroll/periods/{p}/submit` | `payroll.calculate` | taslak → kontrol |
| `POST /api/v1/payroll/periods/{p}/approve` | `payroll.approve` | kontrol → onay |
| `POST /api/v1/payroll/periods/{p}/reject` | `payroll.approve` | kontrol → taslak (`{"note"}` zorunlu) |
| `POST /api/v1/payroll/periods/{p}/close` | `payroll.close` | onay → kapalı |

**Kurallar:**

- **Görevler ayrılığı:** bordroyu bordro uzmanı hesaplar ve kontrole gönderir; muhasebe onaylar, reddeder ve kapatır. Kontrole gönderen kişi, rolü ne olursa olsun aynı dönemi onaylayamaz.
- **Kontrole gönderme koşulları:** dönem hatasız hesaplanmış olmalı. Önceki ay açılmışsa kapanmış olmalı, çünkü kümülatif matrah ondan gelir.
- **Değişmezlik veritabanında:** kontrol, onay ve kapalı durumdaki dönemlerin `payroll_results` ve `payroll_adjustments` satırlarına `INSERT`, `UPDATE` ve `DELETE` bir trigger tarafından reddedilir (`55000`). Kapalı bir dönemin satırı değiştirilemez, hiçbir dönem silinemez. Uygulama kodu hata yapsa ya da biri doğrudan SQL çalıştırsa bile kapanmış bordro değişmez. API bu durumda `409 period_frozen` döner.

**Paralel hesap ve ölçüm:** `run`, dönem satırını kilitler. Bu sayede aynı anda gönderim ya da ikinci bir çalıştırma araya giremez. Dönemdeki çalışanlar `DB_MAX_CONNS − 4` işçiye dağıtılır ve her çalışan kendi transaction'ında hesaplanır. Kural setleri çalıştırma başına önbelleğe alınır. Hesap, saklanan verinin saf bir fonksiyonu olduğu için sıra önemli değildir.

| Ölçüm (`TestRun1500EmployeesUnder10Seconds`, yerel Docker Postgres) | Sonuç |
|---|---|
| 1.506 çalışan, 16 işçi, ilk hesap | ~1,0 sn |
| Aynı kural seti ile yeniden hesap | ~0,9 sn, 0 satır değişti, aynı özet |

**Yeniden üretilebilirlik:** her sonuç kural seti sürümü ve girdi özetiyle (`input_hash`) saklanır. Dönem özeti (`digest`), tüm çalışanların girdi özetlerinin çalışan sırasıyla alınan SHA-256'sıdır. Aynı kural seti ve aynı veriyle yeniden hesap aynı özeti verir ve hiçbir satırı güncellemez.

**Geçmiş dönem düzeltmesi:** kapanmış bir ay asla yeniden hesaplanmaz. Bir ay kapandıktan sonra o aya etki eden bir veri değişirse, sıradaki açık bordro bu kapalı ayın brütünü bugünkü verilerle yeniden bulur. Bu tür veri değişiklikleri şunlardır:

- geriye dönük maaş kaydı
- sonradan onaylanan ücretsiz izin
- geç onaylanan puantaj

Bulunan fark, `Geçmiş dönem düzeltmesi (2026-01)` satırı olarak SGK'ya ve vergiye tabi biçimde eklenir. Hangi ayın etkilendiği, verinin oluşturulma ya da onay zamanının dönemin `closed_at` değerinden sonra olup olmamasıyla tespit edilir. Başka dönemlerde daha önce ödenmiş farklar (`payroll_adjustments`) düşülür; böylece bir fark tam olarak bir kez ödenir.

Örnek (`TestPriorPeriodCorrection`): Ocak kapandıktan sonra 15 Ocak'tan geçerli 80.000 TL zam girilir. Kapalı Ocak bordrosu olduğu gibi kalır. Şubat bordrosuna (14 × 65.000 + 17 × 80.000) / 31 − 65.000 = 8.225,81 TL düzeltme satırı eklenir. Mart bordrosunda bu fark tekrar ödenmez.

```bash
cd backend && go test -v -run 'Period|Rerun|PriorPeriod|1500' ./internal/payroll/
```

**Bilinen sınırlama:** geçmiş dönem farkı, ait olduğu ayın SGK bildirgesine (ek APHB) değil, ödendiği ayın bordrosuna yansıtılıyor. Bu, vergi açısından ödeme ayında vergilendirme pratiğiyle uyumludur; ancak SGK açısından basitleştirilmiş bir yaklaşımdır.

## Avans, Borç ve Ek Ödemeler (F-5)

| Uç nokta | İzin | Açıklama |
|---|---|---|
| `GET /api/v1/payroll/items?employee_id=&period=` | `payroll.items.manage` | |
| `POST /api/v1/payroll/items` | `payroll.items.manage` | `{"employee_id","kind","label","amount":"9000.00","instalments":3,"start_period":"2026-01"}` (`Idempotency-Key` destekli) |
| `POST /api/v1/payroll/items/{id}/cancel` | `payroll.items.manage` | |

| Tür | Bordroya etkisi |
|---|---|
| `bonus` (ek ödeme, prim) | Brüte eklenir; SGK'ya ve gelir/damga vergisine tabidir |
| `advance` (avans) | Muhasebe tarafından önceden ödenmiştir; başlangıç ayında netten tek seferde kesilir |
| `loan` (borç) | 1–36 eşit aylık taksitle netten kesilir; kuruş farkı son taksite eklenir, taksitler toplamı her zaman tutara eşittir |

- **Kesinti sınırı:** bir aylık kesinti taksiti, başlangıç ayındaki brüt maaşın dörtte birini aşamaz. Bu, İş Kanunu md. 35'teki oranın şirket politikası olarak uygulanmasıdır ve net ücretin eksiye düşmesini engeller. Aşan talep `422` alır; çözüm taksit sayısını artırmaktır.
- **Donmuş dönem koruması:** taksit takvimi kontrol, onay ya da kapalı bir döneme değen kalem eklenemez ve iptal edilemez (`409 period_frozen`).
- **Bordrodaki görünüm:** kalemler bordroda kendi satırlarıyla görünür, örneğin `Eğitim borcu (1/3)`.

## Raporlar ve Bordro Çıktısı (F-7)

| Uç nokta | İzin | Açıklama |
|---|---|---|
| `GET /api/v1/payroll/employees/{id}/payslips/{YYYY-MM}` | kendisi / `payroll.read.all` | Bordro belgesi; `?format=html` yazdırılabilir sayfa (tarayıcıdan PDF) |
| `GET /api/v1/reports/payroll-summary?period=&level=&format=csv` | `reports.read` | Birim bazında kişi, brüt, kesintiler, net, işveren maliyeti ve toplam |
| `GET /api/v1/reports/employer-cost?year=` | `reports.read` | Aylık brüt, net ve işveren maliyeti (bonus: maaş maliyet raporu) |
| `GET /api/v1/reports/leave-usage?level=` | `reports.read` | Birim bazında hakedilen, kullanılan, iade edilen izin ve kalan bakiye |
| `GET /api/v1/reports/overtime?month=&level=` | `reports.read` | Onaylı puantajlardan fazla mesai, hafta tatili ve tatil dakikaları; 270 saati aşanlar |

- **Bordro belgesi:** çalışan kendi bordrosunu yalnızca dönem **onaylandıktan ya da kapandıktan** sonra görür; taslak kesin olmadığı için gösterilmez. Bordro uzmanı taslağı "KESİNLEŞMEMİŞ" işaretiyle görür. HTML çıktısında tüm değerler `html/template` ile kaçışlanır ve sıkı bir `Content-Security-Policy` başlığı gönderilir.
- **Raporlarda bireysel maaş yok:** raporlar birim bazında toplam verir (`level` = organizasyon ağacında toplanacak derinlik). Bu yüzden maaşı açık görme izni olmayan denetçi ve muhasebe de raporları okuyabilir.
- **CSV:** Türkçe yerel ayarlı tablolama programlarının doğrudan açabilmesi için `;` ayırıcı ve UTF-8 BOM kullanılır.
- **İzin raporu:** hakedişler, tembel yazılan defterden okunmak yerine motorla hesaplanır; böylece rapor eksiksiz olur ve veritabanına hiçbir şey yazmaz.
- **Bilinen sınırlama:** gerçek PDF üretimi (bonus) yapılmadı. Yazdırılabilir HTML sayfası, tarayıcının "PDF olarak kaydet" özelliğiyle aynı belgeyi üretir. Sunucu tarafında PDF için Türkçe karakter desteği olan bir fontun imaja gömülmesi gerekirdi.

## Veri Üretici (Simülatör)

`simulator` ayrı bir compose servisidir. İlk açılışta migrasyon ve demo seed'den sonra tek bir transaction içinde PostgreSQL `COPY` ile şunları yükler:

| Veri | Miktar (tohum 42) |
|---|---|
| Çalışan (19 birim: gece vardiyası, depo, çağrı merkezi, satış, BT …) | 1.500 |
| Maaş kaydı (2025 ve 2026, şifreli) | ≈2.800 |
| Onaylı izin talebi (yıllık, rapor, ücretsiz) ve tutarlı izin defteri | ≈8.500 |
| Basım (2 yıl) | ≈1,4 milyon |
| Yükleme süresi | ≈35 sn |

- **Bilinçli hatalar:** vardiyaların ≈%0,6'sında çıkış, ≈%0,4'ünde giriş basımı eksiktir; ≈%1,6'sında çift basım vardır. Bunlara ek olarak Cumartesi mesaisi, gece vardiyası ve depoda resmî tatil çalışması bulunur.
- **Aynı tohum, aynı veri:** tüm değerler tek bir `SIM_SEED`'den, varlık başına ayrı PCG akışlarıyla (tohum, çalışan sırası, tarih) türetilir. Map sırasına ya da saate bağlı hiçbir şey yoktur. Bu sayede canlı modda üretilen bir gün, toplu yüklemede üretilen aynı günle birebir aynıdır.
- **İzin geçmişi kurallara uyar:** yıllık izin, başlangıç tarihindeki bakiyeye sığmıyorsa üretilmez; izinler çakışmaz; `bakiye = defter toplamı` koşulu korunur.
- **Canlı mod:** her `SIM_TICK` (varsayılan 15 sn) bugünün vardiya planından zamanı gelmiş basımları ekler. Cihaz basımlarındaki benzersiz dizin sayesinde aynı basım iki kez yazılmaz. Onaylı izindeki çalışanlar ve onaylanmış puantaj ayları atlanır. `GET /api/v1/system/status` aktif çalışan sayısını, bugünkü basımları, bekleyen işleri ve simülatörün son çalışma zamanını gösterir.
- Yükleme yapılmışsa (`simulator_state`) tekrar yapılmaz. `make reset-demo` hacmi silip her şeyi aynı tohumla yeniden kurar.

## Hızlı Doğrulama

```bash
make verify        # 17 kontrol: HEALTH, AUTH, AUTHZ, IDEMP, F-1..F-7, T-1..T-5, AUDIT → PASS/FAIL tablosu
make reset-demo    # ilk seed haline dön
```

Ayrıntılar, demo hesapları, her kimlik için "nasıl denenir / beklenen sonuç" tablosu ve kanıt komutları [TESTING.md](TESTING.md) dosyasındadır. Tüm uç noktalar [requests.http](requests.http) dosyasında demo hesaplarla çalışır durumdadır.

## Dizinler ve `EXPLAIN (ANALYZE)`

Ölçümler simülatör verisi üzerinde alındı: 1.405.389 basım, 8.518 izin talebi, 19.002 defter satırı. Ortam yerel Docker'da PostgreSQL 16. "Dizinsiz" ölçüm için dizin bir transaction içinde düşürüldü, sorgu çalıştırıldı ve `ROLLBACK` yapıldı.

**1. `time_punches (employee_id, punched_at)`:** puantaj ekranı ve analizi. Bir çalışanın bir aylık basımlarını, onaylı silme önerileri hariç getirir.

```
Dizinli:   Index Scan using time_punches_employee_time_idx on time_punches p (actual time=0.038..0.045 rows=49 loops=1)
             Index Cond: ((employee_id = $0) AND (punched_at >= '2026-09-26 …') AND (punched_at < '2026-11-03 …'))
             Buffers: shared hit=6 read=3
           Execution Time: 0.142 ms
Dizinsiz:  Parallel Seq Scan on time_punches p (actual time=11.442..19.577 rows=16 loops=3)
             Rows Removed by Filter: 468447
             Buffers: shared hit=12013 read=2476
           Execution Time: 29.525 ms
```

Fark yaklaşık **200 kat** (0,14 ms'ye karşı 29,5 ms) ve okunan sayfa sayısı 10'a karşı 14.599. Dizin, puantaj analizinin çalışan sayısından bağımsız kalmasını sağlıyor. Dizinsiz durumda her istek tüm tabloyu tarar ve maliyet basım sayısıyla doğrusal büyür.

**2. `leave_requests (employee_id, start_date DESC)`:** izin listesi ve bakiye ekranı. Bir çalışanın talepleri, en yeni önce.

```
Dizinli:   Index Scan using leave_requests_employee_idx on leave_requests (actual time=0.021..0.021 rows=3 loops=1)
           Execution Time: 0.042 ms   (6 sayfa)
Dizinsiz:  Seq Scan on leave_requests … Rows Removed by Filter: 8515 → Sort
           Execution Time: 0.367 ms   (145 sayfa)
```

Bu tabloda fark yaklaşık 9 kat ve mutlak süreler küçük. Ancak dizin sıralamayı da karşıladığı için `ORDER BY … LIMIT` ayrı bir sıralama adımı gerektirmiyor.

**3. `org_units USING gist (path)`:** yönetici alt ağacı. `org_units` 22 satırlık bir tablo olduğu için planlayıcı doğru şekilde sıralı taramayı seçiyor (0,34 ms; çalışanlar `employees_org_unit_idx` ile birleştiriliyor). Sıralı tarama kapatıldığında `Index Scan using org_units_path_gist … Index Cond: (path <@ 'genel_mudurluk.operasyon')` planı görülüyor. Bu dizin, ağaç binlerce birime büyüdüğünde devreye girmek üzere orada.

**4. `time_punches (punched_at)`:** paneldeki "bugünkü basım" sayımı. Bu dizin yük testinin bulduğu darboğaz üzerine eklendi (bkz. Yük Testi): sorgu 98,9 ms'den (paralel sıralı tarama, 1,4 milyon satır) 0,29 ms'ye (index-only scan) indi.

Diğer kritik dizinler: `leave_ledger (employee_id, effective_date, id)` (bakiye özeti 0,07 ms), `payroll_results (employee_id, period)` (benzersiz; kümülatif matrah zinciri), kısmi `punch_corrections (employee_id, work_date) WHERE status = 'proposed'` (onay kuyruğu) ve `idempotency_keys (user_id, key)`.

## Yük Testi (k6)

```bash
make load    # compose ağında grafana/k6; 90 sn, 50 eşzamanlı kullanıcıya rampa + saniyede 5 giriş
```

Senaryo (`load/load.js`): kullanıcılar hiç beklemeden (stres) ardı ardına istek atar. Trafik karışımı şöyledir:
- yönetici kapsamında çalışan listesi (ltree alt ağacı)
- rastgele bir simülatör çalışanının izin bakiyesi
- puantaj ay özeti (≈45 günlük basımın eşleştirilmesi)
- bordro sonuçları
- onay kuyruğu ve sistem durumu
- bunlara ek olarak saniyede 5 giriş (argon2id, CPU yoğun)

Veri: simülatörün 1.500 çalışanı ve 1,4 milyon basımı. Ortam: yerel Docker Desktop (16 çekirdek), tüm servisler aynı makinede.

| Uç nokta | p50 (ms) | p95 (ms) | p99 (ms) | maks (ms) |
|---|---:|---:|---:|---:|
| Çalışan listesi (yönetici kapsamı) | 7,6 | 12,9 | 15,6 | 30,2 |
| İzin bakiyesi | 13,3 | 20,6 | 24,1 | 40,6 |
| Puantaj ay özeti | 11,5 | 18,9 | 22,4 | 37,3 |
| Bordro sonuçları | 5,6 | 10,4 | 12,8 | 32,1 |
| Giriş (argon2id) | 102,7 | 116,3 | 125,4 | 142,6 |

**Toplam 316.143 istek, 3.507 istek/sn, hata oranı %0,00.** Eşikler (p95 < 300/500/800 ms, hata < %1) `make load`'un çıkış koduna bağlıdır.

**Ölçümün bulduğu darboğaz:** İlk koşuda verim yalnızca 155 istek/sn idi ve izin bakiyesinin p95 değeri 309 ms ile eşiği aşıyordu. Örnekleme sırasında API %50 CPU kullanırken veritabanı 16 çekirdeğin yaklaşık 15'ini tüketiyordu. Sebep panelin "bugünkü basım" sayımıydı: `WHERE punched_at >= bugün` filtresini `(employee_id, punched_at)` dizini karşılayamıyor, bu yüzden her istek 1,4 milyon satırı 3 paralel süreçle tarıyordu (98,9 ms). `time_punches (punched_at)` dizini eklendi (migrasyon 00010). Sorgu 0,29 ms'ye (index-only scan) indi, verim 22 kat arttı ve tüm gecikmeler yukarıdaki değerlere düştü.

## Dayanıklılık ve Kaos Senaryosu

```bash
make chaos   # scripts/chaos.sh: veritabanını durdurur, geri açar, API'yi yeniden başlatır
```

| Adım | Beklenen | Ölçülen |
|---|---|---|
| 1a. Veritabanı durdurulur | `/health` hemen 503 (`degraded`) | 503, 2 ms |
| 1b. Veritabanı yokken giriş | Asılı kalmadan `503 database_unavailable` + `Retry-After` | 503, ≈2,1 sn |
| 1c. API süreci | Çökmez, yeniden başlamaz | restart sayısı 0 |
| 2a. Veritabanı geri gelir | `/health` kendiliğinden 200 | < 1 sn |
| 2b. Giriş ve veri | API yeniden başlatılmadan çalışır (havuz yeniden bağlanır) | 200 |
| 3a–b. API yeniden başlatılır | nginx vekili (:3000) yeni konteyneri yeniden çözer, giriş çalışır | 200 |
| 3c. Simülatör | Kesintiden sonra çalışmaya devam eder | çalışıyor |

**Kaos testinin bulduğu sorun:** ilk koşuda veritabanı yokken giriş isteği **8 saniye** bekleyip 500 dönüyordu.
- *Sebep:* Durdurulan konteynerin adı için Docker DNS'i "bulunamadı" demek yerine sorguyu zaman aşımına bırakıyor. Bağlantı kurma süresine ise bir sınır yoktu.
- *Düzeltme:* `ConnectTimeout` (3 sn) ve DNS çözümlemesi için ayrı bir bağlam süresi (2 sn) eklendi.
- *Yanıt kodu:* Bağlantı hataları artık genel 500 yerine `503 database_unavailable` ve `Retry-After: 5` döner. Bu geçici, tekrar denenebilir bir durumdur ve istemciye öyle bildirilir.

Diğer dayanıklılık önlemleri:
- her istek `HTTP_REQUEST_TIMEOUT` ile sınırlı bir bağlam taşır
- düzgün kapanış (`SHUTDOWN_TIMEOUT`)
- veritabanı açılışta üstel geri çekilmeyle beklenir
- migrasyon ayrı bir servistir; hatalı bir migrasyon API'yi crash-loop'a sokmaz
- servisler `restart: unless-stopped` ile çalışır

## Testler

```bash
make testdb-up   # 127.0.0.1:55432 üzerinde geçici PostgreSQL 16
make test        # Go testleri + frontend tip kontrolü
make testdb-down
```

Veritabanı testleri sahte nesne (mock) kullanmaz. Her test, gerçek PostgreSQL üzerinde rastgele adlı yeni bir veritabanı açar, tüm migrasyonları uygular ve test bitince veritabanını siler. `TEST_DATABASE_URL` tanımlı değilse bu testler atlanır. CI'da GitHub Actions bir Postgres servisi başlatır ve testleri `-race` ile çalıştırır.

Yarış durumu kanıtı: `TestConcurrentRefreshSingleWinner`. Aynı refresh token'ı 10 goroutine aynı anda kullanır; yalnızca biri başarılı olur ve token ailesi iptal edilir.

```bash
cd backend && go test -run TestConcurrentRefreshSingleWinner -count=10 ./internal/auth/
```
