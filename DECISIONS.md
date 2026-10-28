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

## ADR-004: Kısa ömürlü JWT + dönen (rotating) opak refresh token

**Bağlam:** Spec JWT (access + refresh) ya da sunucu taraflı oturum istiyor. Arayüz bir SPA'dır. Token'ların XSS ile çalınması ve sızan bir refresh token'ın sınırsız süre kullanılması engellenmeli.

**Karar:** Access token 15 dakikalık HS256 JWT'dir ve bellekte tutulur. Refresh token opak, rastgele bir değerdir; veritabanında yalnızca SHA-256 özeti saklanır ve `HttpOnly`, `SameSite=Strict`, `Path=/api/v1/auth` niteliklerine sahip bir cookie'de taşınır. Her refresh işleminde token "kullanıldı" olarak işaretlenir ve aynı ailede yeni bir token verilir. Kullanılmış bir token tekrar gelirse aile bütünüyle iptal edilir. Aynı token'la gelen eşzamanlı istekler `SELECT … FOR UPDATE` ile sıraya girer ve yalnızca biri kazanır.

**Alternatifler:** (1) Yalnızca sunucu taraflı oturum: her istekte oturum tablosu okunur. Bu tasarımda da hesap her istekte okunuyor, ama JWT ileride servisler arası doğrulamaya olanak bırakıyor. (2) Refresh token'ı `localStorage`'da tutmak: XSS ile çalınabilir. (3) Refresh token'ı JWT yapmak: iptal edilebilmesi için yine de veritabanında tutulması gerekir, opak token'a göre bir avantajı kalmaz.

**Sonuçlar:** Çalınan bir refresh token'ın ilk kullanımı, meşru kullanıcının sonraki refresh isteğinde ortaya çıkar ve oturum sonlanır. Bilinen sınırlama: iki sekme aynı anda refresh yaparsa biri "yeniden kullanım" sayılır ve oturum düşer. Arayüz refresh isteklerini tek bir uçuşa (single-flight) indirerek bunu önleyecek.

## ADR-005: Rol değil izin kontrolü, matris kodda

**Bağlam:** Altı rol ve her uç noktada yetki kontrolü gerekiyor. Yöneticinin "ekip" kapsamı, çalışanın "kendi" kapsamı gibi veri seviyesinde ayrımlar da var.

**Karar:** Her kullanıcının tek bir rolü vardır (`users.role`, `CHECK` kısıtlı). Handler'lar rolü değil izni (`payroll.approve`) sorar. Rol → izin eşlemesi `internal/auth/rbac.go` dosyasında, kodla birlikte sürümlenir. İzin adındaki `.self`, `.team` ve `.all` son ekleri veri kapsamını belirtir; kapsam filtresi sorgu katmanında uygulanır.

**Alternatifler:** İzinleri veritabanındaki `roles`/`role_permissions` tablolarında tutmak. Çalışma anında düzenlenebilir olurdu, ancak bu projede rol tanımı bir ürün kararıdır; matrisin kodda olması onu test edilebilir ve gözden geçirilebilir kılar.

**Sonuçlar:** Görevler ayrılığı (hesaplayan onaylayamaz) ve denetçinin salt okunur olması birim testleriyle korunur. Yeni bir rol eklemek kod değişikliği gerektirir.

## ADR-006: Entegrasyon testleri gerçek PostgreSQL'de, test başına yeni veritabanı

**Bağlam:** Kritik kurallar (ltree yolları, eklemeli-yalnız denetim kaydı, kilitler, kısıtlar) veritabanında uygulanıyor. Sahte nesnelerle yazılan testler bu kuralları kanıtlayamaz.

**Karar:** `internal/testdb` her test için rastgele adlı bir veritabanı oluşturur, gömülü migrasyonları uygular ve test sonunda veritabanını `DROP … WITH (FORCE)` ile siler. CI'da GitHub Actions Postgres servisi kullanılır; yerelde `make testdb-up` ile geçici bir konteyner açılır.

**Alternatifler:** testcontainers-go (ek bağımlılık ve Docker-in-Docker gereksinimi), tek ortak veritabanı ile transaction rollback (eşzamanlılık testleri birden fazla bağlantı gerektirdiği için uygun değil).

**Sonuçlar:** Testler birbirinden yalıtılmıştır ve paralel çalışabilir. Bir testin maliyeti yaklaşık 250 ms'dir. Migrasyonlar her test çalışmasında baştan uygulandığı için onlar da sürekli test edilmiş olur.
