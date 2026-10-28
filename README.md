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
