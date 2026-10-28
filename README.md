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
