# Mimari Karar Kayıtları (ADR)

Her kayıt: bağlam → karar → alternatifler → sonuçlar.

## ADR-001: Tek Go modülü, çok sayıda `cmd/` ikilisi

**Bağlam:** API, migrasyon aracı, veri üretici (simülatör) ve doğrulama aracı aynı domain kurallarını (izin hakedişi, bordro parametreleri) paylaşacak.

**Karar:** `backend/` altında tek bir Go modülü kullanılır: `cmd/api`, `cmd/migrate` ve sonraki aşamalarda `cmd/simulator`, `cmd/verify`. Tek bir Dockerfile çok aşamalı derlenir; her compose servisi kendi `target`'ını kullanır.

**Alternatifler:** Her servis için ayrı modül/repo. Bu, paylaşılan domain kodunun kopyalanmasına ya da sürüm yönetimine yol açar ve bu ölçekte gereksizdir.

**Sonuçlar:** Derleme önbelleği bir kez dolar. Simülatör ile API aynı domain koduyla aynı kuralları uygular. Her imajın içinde yalnızca kendi ikilisi bulunur.

## ADR-002: Migrasyonlar ayrı bir tek seferlik compose servisi

**Bağlam:** Migrasyonlar elle adım gerektirmeden otomatik çalışmalı. API birden fazla kopya ile ölçeklenirse eşzamanlı migrasyon yarışı oluşmamalı.

**Karar:** `migrate` servisi goose ile gömülü SQL dosyalarını uygular ve çıkar. `api`, `depends_on: migrate: service_completed_successfully` ile bekler.

**Alternatifler:** Migrasyonu API açılışında çalıştırmak. Bu yöntem çok kopyada yarışa yol açar (goose kilidiyle çözülebilir ama hatalı migrasyon API'yi crash-loop'a sokar).

**Sonuçlar:** Başarısız migrasyon açıkça görünür ve API hiç başlamaz. Şema sürümü tek bir yerde yönetilir.

## ADR-003: Aynı origin ters vekil (nginx → API)

**Bağlam:** Arayüz :3000'de, API :8080'de çalışır. Tarayıcı iki origin ile konuşursa CORS ve kimlik bilgisi taşıma (çerez) karmaşıklaşır.

**Karar:** nginx, `/api/*` ve `/health` isteklerini `api:8080`'e iletir. Docker DNS'i her istekte yeniden çözülür (`resolver 127.0.0.11` + değişkenli `proxy_pass`). Böylece API konteyneri yeniden başladığında (kaos senaryosu) vekil eski IP'de takılı kalmaz.

**Alternatifler:** Tarayıcının doğrudan :8080'e gitmesi + CORS. API yine de `CORS_ORIGINS` ile doğrudan kullanıma izin verir.

**Sonuçlar:** Refresh token'ı `HttpOnly` + `SameSite=Strict` çerezde tutmak mümkün olur.
