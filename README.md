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
                                                          ▲
                                       migrate (tek seferlik, goose) ──┘
```

- **db**: `postgres:16-alpine`, yalnızca iç ağda. `pg_isready` sağlık kontrolü.
- **migrate**: gömülü (embed) goose migrasyonlarını uygular ve tamamlanınca çıkar. API, `service_completed_successfully` koşuluyla bunu bekler.
- **api**: Go + chi + pgx. `/health` (DB ping dahil), `/metrics` (Prometheus), JSON log, istek bazlı zaman aşımı, düzgün kapanış.
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
