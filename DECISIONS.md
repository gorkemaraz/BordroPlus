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

**Sonuçlar:** Çalınan bir refresh token'ın ilk kullanımı, meşru kullanıcının sonraki refresh isteğinde ortaya çıkar ve oturum sonlanır. Bilinen sınırlama: iki sekme aynı anda refresh yaparsa biri "yeniden kullanım" sayılır ve oturum düşer. Arayüz bunu önler: bir sekmedeki istekler tek bir refresh'i bekler (single-flight), sekmeler arasında refresh bir Web Lock ile sıraya girer, bekleyen sekme diğerinin aldığı token'ı (BroadcastChannel ile gelir) kullanır ve kullanılmış refresh token'ı ikinci kez sunmaz (`frontend/src/api/client.ts`). `e2e/tabs.mjs`, iki sekmenin aynı anda yenilenmesiyle bunu doğrular; kilit eklenmeden önce bu test ilk turda oturumu düşürüyordu.

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

## ADR-007: Maaşlar uygulama katmanında AES-256-GCM ile şifrelenir

**Bağlam:** Spec, maaş alanlarının şifreli ya da maskeli saklanmasını istiyor. Bordro motoru ise 1.500 maaşı 10 saniyeden kısa sürede okuyup hesaplayabilmeli.

**Karar:** Tutar (kuruş, int64) Go'da AES-256-GCM ile şifrelenir ve `bytea` olarak saklanır. Anahtar `SALARY_ENCRYPTION_KEY` ortam değişkeninden gelir. Şifreli metnin ilk baytı anahtar sürümüdür; böylece anahtar döndürüldüğünde eski kayıtlar okunmaya devam eder. GCM ek verisi `employee_salaries:<employee_id>:<valid_from>` olarak verilir.

**Alternatifler:** (1) `pgcrypto` ile `pgp_sym_encrypt`: anahtar her sorguda SQL parametresi olarak veritabanına gider ve sorgu loglarına ya da `pg_stat_statements`'a düşebilir. (2) Disk ya da tablo alanı şifreleme: veritabanına erişen herkese açık metin gösterir, "maskeli" gereksinimini karşılamaz. (3) Yalnızca API'de maskeleme: veritabanı dökümünde maaşlar açık kalır.

**Sonuçlar:** Veritabanı yedeği ya da SQL erişimi maaşları ele vermez. Bunun bedeli olarak maaşa göre SQL'de sıralama ya da toplama yapılamaz; raporlar ve bordro motoru maaşları Go'da çözer (1.500 çözme işlemi milisaniyeler sürer). Anahtar kaybolursa maaşlar okunamaz; anahtar yönetimi operasyonun sorumluluğundadır.

## ADR-008: Veri kapsamı her istekte hesaplanır ve SQL'de uygulanır

**Bağlam:** Yönetici yalnızca kendi alt ağacını görmeli. Vekâlet başladığı ya da bittiği anda geçerli olmalı. Birim taşındığında görünürlük hemen değişmeli.

**Karar:** Kimlik doğrulama middleware'i her istekte kullanıcının yönettiği birimlerin ve bugün geçerli vekâletlerin ltree yollarını yükler (`Principal.TeamPaths`). Handler'lar `.all`, `.team` ve `.self` izinlerinden bir `Scope` üretir ve bunu her sorguya aynı `WHERE` parçasıyla ekler. Kapsam dışındaki tekil kayıtlar 403, var olmayanlar 404 döner.

**Alternatifler:** (1) Kapsamı JWT içine koymak: vekâlet ya da birim taşıma, token süresi dolana kadar (15 dakika) yansımaz. (2) Satırları Go'da süzmek: sayfalama bozulur ve fazla veri okunur. (3) PostgreSQL Row Level Security: güçlü bir seçenek, ancak bağlantı havuzunda her istek için oturum değişkeni ayarlamayı gerektirir ve hata ayıklamayı zorlaştırır.

**Sonuçlar:** İstek başına bir ek sorgu (iki dizinli arama) maliyeti var. Kapsam kuralı tek bir yerde tanımlı (`org.Scope`) ve izin, puantaj gibi sonraki modüller de aynısını kullanacak. 403 ile 404'ün ayrılması kaydın var olup olmadığını ele verir; spec yetkisiz erişimde 403 istediği için bu bilinçli bir tercih.

## ADR-009: İzin bakiyesi = kilitli hesap satırı + eklemeli-yalnız defter, tembel hakediş

**Bağlam:** Aynı çalışanın iki izni eşzamanlı onaylandığında bakiye negatife düşmemeli. Devreden izin, hakediş ve iadeler açıklanabilir olmalı. Hakedişler yıldönümünde doğar ve bunun için bir zamanlanmış göreve bağımlı olmak istemiyoruz.

**Karar:** Her hareket `leave_ledger` tablosuna yazılır: hakediş, kullanım, rapor iadesi, iptal ve düzeltme. Defter eklemeli-yalnızdır; güncelleme ve silme trigger ile reddedilir. Güncel bakiye `leave_accounts` satırında tutulur ve bakiyeyi değiştiren her işlem bu satırı önce `FOR UPDATE` ile kilitler. Satırdaki `CHECK (balance >= 0)` kodun yanılabileceği duruma karşı ikinci güvencedir. Vadesi gelmiş hakedişler, bakiyeye dokunan ilk işlemde deftere yazılır; benzersiz dizin aynı hakedişin iki kez yazılmasını engeller.

**Alternatifler:** (1) Bakiyeyi her seferinde defterden toplamak ve `SERIALIZABLE` kullanmak: doğru olur, ancak yoğun onay anlarında yeniden deneme döngüsü gerekir ve hatalar daha az okunaklıdır. (2) Gece çalışan bir hakediş görevi: görev atlanırsa bakiye yanlış görünür ve geçmişe dönük düzeltme zorlaşır. (3) Tek bir bakiye sütunu, defter olmadan: "bu 34 gün nereden geldi?" sorusu cevapsız kalır.

**Sonuçlar:** Her bakiye satır satır açıklanabilir; testler her seferinde "hesap bakiyesi = defter toplamı" koşulunu kontrol eder. Bakiye okuma işlemi de yazma yapar (hakediş yazımı); bu deterministik ve idempotent olduğu için kabul edildi. Bilinen sınırlama: onaylandıktan sonra eklenen bir resmî tatil, onaylı iznin düşülen gününü geriye dönük değiştirmez. Bekleyen talepler ise onay anında yeniden hesaplanır.

## ADR-010: Puantaj ham basımlardan her seferinde yeniden hesaplanır; düzeltmeler öneri olarak saklanır

**Bağlam:** Basımlar eksik ya da çift olabilir. Sistem düzeltme önermeli ama onaysız hiçbir şeyi değiştirmemeli. Gece vardiyaları gün ve hafta sınırlarını aşar. Bordro, onaylanmış ve artık değişmeyecek toplamlara ihtiyaç duyar.

**Karar:** Ham basımlar hiç değişmez. Oturumlar, anomaliler ve toplamlar saf bir motorla her okumada etkin basımlardan yeniden hesaplanır. Bulunan her anomali, kararlı bir anahtarla (`out:<giriş basımı id>` gibi) `punch_corrections` tablosuna öneri olarak yazılır; aynı analiz tekrar çalıştığında yeni kayıt oluşmaz. Onaylanan bir öneri ya bir basımı gizler ya da yeni bir "correction" basımı ekler. Ay onayı toplamları JSON olarak dondurur. Bir çalışanın puantajına yazan işlemler, satır kilitlerinden önce alınan bir advisory lock ile sıraya girer.

**Alternatifler:** (1) Oturumları ayrı bir tabloda kalıcı tutmak: her basım ve her düzeltmede yeniden eşleştirme ve tutarlılık sorunu doğar. (2) Basımı yerinde düzeltmek: denetim izi kaybolur ve "onaysız değiştirmeme" kuralı ihlal edilir. (3) Haftayı takvim ayına göre bölmek: 45 saat kuralı iki aya parçalanır ve fazla mesai yanlış hesaplanır.

**Sonuçlar:** Kural değişikliği ya da onaylı düzeltme anında yansır ve geçmiş yeniden üretilebilir; donmuş aylar ise değişmez. Okuma işlemi öneri tablosuna yazar; bu idempotent olduğu için kabul edildi. Maliyet, okunan aralığın her istekte yeniden eşleştirilmesidir. Bu da çalışan başına günde birkaç basım için önemsizdir (62 günlük sınır var). Bilinen sınırlama: vardiya planı tutulmuyor; öneriler planlanan vardiya yerine tarihsel medyana dayanıyor.

## ADR-011: Bordro motoru saf fonksiyon; tam sayı kuruş, kümülatif vergi tek yuvarlama ile

**Bağlam:** Bir kuruşluk fark hukuki sorundur. Hesap yeniden üretilebilir ve açıklanabilir olmalı. Oranlar ve tavanlar yıl içinde değişebilir. Geçmiş bir aydaki düzeltme, sonraki ayların kümülatif matrahını değiştirmelidir.

**Karar:** `payroll.Calculate(RuleSet, Input) Result` veritabanına dokunmayan, deterministik bir fonksiyondur. Tutarlar `int64` kuruş, oranlar 1/100.000 birimindedir. Her kalem kuruşa bir kez yarım-yukarı yuvarlanır. Aylık gelir vergisi, yıllık tarifenin kesin değerleri üzerinden `T(kümülatif) − T(önceki kümülatif)` olarak hesaplanır ve yalnızca bu fark yuvarlanır. Parametreler `payroll_rule_sets` tablosunda geçerlilik aralığıyla tutulur. Her sonuç girdileriyle, kural seti sürümüyle ve girdi özetiyle (`input_hash`) birlikte saklanır. Bir düzeltme, düzeltilen aydan yılın sonuna kadar sırayla yeniden hesaplanır.

**Alternatifler:** (1) `numeric` ile SQL'de hesap: yuvarlama noktaları dağınık olur ve test etmek zorlaşır. (2) `float64`: 0,1 + 0,2 sorunu, kuruş farkları. (3) Aylık vergiyi o ayın matrahına uygulanan dilim oranıyla hesaplamak: dilim geçişinde yanlış sonuç verir. (4) Parametreleri koda gömmek: yıl ortası değişiklik deploy gerektirir ve geçmiş ayların hangi kurallarla hesaplandığı izlenemez.

**Sonuçlar:** Motor, yayımlanmış resmî rakamlarla (asgari ücret neti, aylık istisna tutarları, yıllık toplam) birebir doğrulanabildi. Aynı girdi her zaman aynı sonucu verir; bu, 1.500 çalışanın paralel hesabını (T-4) güvenli kılar. Bunun bedeli olarak her satırın formül metniyle birlikte saklanması sonuç JSON'unu büyütür, ancak bu bilinçli bir tercihtir: bordro sonradan açıklanabilir olmalı.

## ADR-012: Kapalı dönem veritabanında dondurulur; düzeltmeler sonraki bordroya fark satırı olarak yansır

**Bağlam:** Kapanan bordro dönemi değişmemeli; sonradan gelen düzeltmeler "geçmiş dönem düzeltmesi" olarak sonraki bordroya yansımalı. 1.500 çalışanın bordrosu 10 saniyenin altında hesaplanmalı ve aynı kural seti sürümüyle tekrar hesap aynı sonucu vermeli.

**Karar:** Dönem durumu `payroll_periods` tablosunda tutulur. Kontrol, onay ve kapalı dönemlerin sonuç ve düzeltme satırlarını bir `BEFORE` trigger korur; kapalı dönem satırının kendisi de trigger ile değişmez hale gelir. Düzeltme, kapalı ayın sonucunu değiştirmez. Açık dönemin hesabı sırasında, kapanıştan sonra değişen veri kaynakları (maaş, ücretsiz izin, puantaj onayı) taranır, etkilenen kapalı ayın brütü bugünkü veriyle yeniden türetilir ve fark bir satır olarak eklenir. Daha önce ödenen farklar `payroll_adjustments` tablosunda izlenir ve düşülür. Dönem hesabı, çalışan başına ayrı transaction açan sabit boyutlu bir goroutine havuzuyla yapılır; dönem satırı çalıştırma boyunca kilitli tutulur.

**Alternatifler:** (1) Değişmezliği yalnızca uygulamada kontrol etmek: bir hata ya da elle yapılan bir SQL güncellemesi kapanmış bordroyu bozabilir. (2) Kapalı ayı yeniden hesaplayıp yeni sürüm olarak saklamak: hukuken ödenmiş ve bildirilmiş bordro değişmiş gibi görünür ve kümülatif zincir karmaşıklaşır. (3) Tüm dönemi tek transaction'da sırayla hesaplamak: basittir ama 1.500 çalışan için uzun süren tek bir transaction ve tek çekirdek demektir.

**Sonuçlar:** Kapalı bordro gerçekten değişmez. Düzeltmeler izlenebilir: kaynak ay, hedef ay, tutar ve gerekçe saklanır. Paralel hesap yerelde 1.506 çalışan için yaklaşık 1 saniye sürüyor. Bunun bedeli olarak açık dönemin hesabı, etkilenen kapalı aylar için ek girdi türetir; değişiklik takibi sayesinde bu yalnızca veri gerçekten değiştiğinde yapılır. Bilinen sınırlama: fark SGK açısından ait olduğu aya değil, ödendiği aya bildirilir.

## ADR-013: Simülatör, veritabanına doğrudan COPY ile yazar; veri varlık bazlı tohum akışlarından türetilir

**Bağlam:** 1.500 çalışan, 2 yıllık puantaj ve izin geçmişi üretilmeli. Aynı tohum aynı veriyi vermeli. Sistem, soğuk makinede 5 dakika içinde hazır olmalı. Arayüzde sistemin canlı olduğu görülebilmeli.

**Karar:** Simülatör, API yerine domain paketlerini (izin motoru, şifreleme, eşleştirme) kullanarak veriyi kendisi üretir ve tek bir transaction içinde `COPY` ile yazar. Rastgelelik tek bir global akıştan değil, `(tohum, çalışan sırası, tarih)` üçlüsünden türetilen ayrı PCG akışlarından gelir. Canlı mod aynı `Day` fonksiyonunu kullanır ve cihaz basımlarındaki benzersiz dizin sayesinde idempotenttir.

**Alternatifler:** (1) Veriyi API üzerinden yüklemek: yaklaşık 1,4 milyon basım için saatler sürer ve bu ölçekte bir fayda sağlamaz. (2) Tek bir global rastgele akış: üretim sırası değiştiğinde (örneğin bir birim eklendiğinde) tüm veri değişir; canlı mod da geçmişle tutarlı olmaz. (3) Hazır SQL dökümü: tohumla değiştirilemez ve maaşları kendi anahtarıyla şifreleyemez.

**Sonuçlar:** Yükleme yaklaşık 35 saniye sürüyor; soğuk başlangıç ile `make reset-demo` toplamda 1 dakikanın altında kalıyor. Simülatör iş kurallarını API dışında uyguladığı için, ürettiği verinin kurallara uyduğu testlerle doğrulanır: çakışma yok, bakiye eksiye düşmüyor, defter tutarlı, anomali oranları planlanan aralıkta. Bunun bedeli olarak simülatör, domain paketlerine derleme zamanında bağımlıdır.

## ADR-014: `make verify` ayrı bir Go ikilisi; her çalıştırma kendi verisini oluşturur

**Bağlam:** Ekip yöneticisi tüm özellikleri tek komutla denemeli. Komut tekrarlanabilir olmalı, sonunda bir PASS/FAIL tablosu basmalı ve 15 dakikayı aşmamalı.

**Karar:** `cmd/verify`, API'ye yalnızca HTTP üzerinden ve demo hesaplarla konuşan bir program olarak yazıldı ve compose'un `verify` profilinde konteyner olarak çalışıyor. Her çalıştırma kendine ait çalışanlar oluşturuyor (`V<zaman>`, `W<zaman>`); izin, puantaj ve bordro senaryoları bu çalışanlar üzerinde yürüyor. Bordro iş akışı ise henüz kapanmamış ilk ayı kullanıyor.

**Alternatifler:** (1) Kabuk betiği ile `curl`/`jq`: eşzamanlılık senaryoları ve tutar karşılaştırmaları kırılgan olur. (2) Her doğrulamadan önce veriyi sıfırlamak: daha basit olurdu ama birkaç dakika ekler ve "çalışan sistem üzerinde doğrulama" ruhuna aykırıdır. (3) Go entegrasyon testlerini yeniden kullanmak: onlar `httptest` üzerinde çalışır, Docker'daki gerçek yığını denemez.

**Sonuçlar:** `make verify` yaklaşık 3 saniyede 17 kontrolü çalıştırıyor ve art arda çalıştırılabiliyor; iki ardışık çalıştırma 17/17 geçti. İkinci çalıştırma, gerçek bir eksiği ortaya çıkardı: dönem kapandıktan sonra girilen ve işe girişi o aya düşen çalışan. Bu eksik düzeltildi ve regresyon testi eklendi. Bilinen sınırlama: 2025–2026'daki 24 ay kapandıktan sonra `make reset-demo` gerekir.
