# E-İmza Fizibilite Raporu — Merkezi İmza Sunucusu

*Tarih: 2026-07-18 · Yöntem: 4 paralel araştırma ajanı (kod tabanı analizi, eimza-go kod incelemesi, Türk e-imza ekosistemi, mimari + hukuk) + alt-ajan bulguları (CSC/eIDAS standartları, PKCS#11 ağ aktarımı, akıllı kart eşzamanlılığı). Hukuk bölümü araştırma özetidir, hukuki mütalaa değildir.*

---

## 1. Yönetici özeti

**Soru:** Ofisteki herkes, farklı bilgisayarlardan, merkezi bir sunucuya takılı USB e-imza(ları) aynı anda kullanabilir mi? Yargı Asistan'a bu eklenebilir mi? eimza-go işe yarar mı?

**Cevap:**

| Konu | Hüküm |
|---|---|
| Teknik olarak yapılabilir mi? | **Evet** — belge imzalama tarafı tamamen inşa edilebilir; UYAP'a giriş tarafı ancak uzak masaüstü veya USB-over-IP ile (pilot şart). |
| "Tek ortak e-imzayı herkes kullansın" modeli | **Önerilmez.** Teknik olarak mümkün ama hukuken kırılgan: imzaların "güvenli elektronik imza" vasfı (5070 md.4) tartışmalı hale gelir, yönetmelik md.15(e) ihlali doğar, tek PIN kilidi tüm ofisi durdurur. |
| Önerilen model | **Her avukatın kendi token'ı merkezi sunucuya takılı; her imza işlemini yalnızca sertifika sahibi kendi PIN'iyle uzaktan tetikliyor.** Teknik ve hukuki riski kişi başına sınırlar; eIDAS/CSC "uzak imza" desenine (SCAL2 tarzı işlem-başı yetkilendirme) yakınsar. |
| eimza-go | **Fork'la ve genişlet.** MIT lisanslı, derlenen, test edilen ciddi bir temel; ama (a) kritik bir ETSI uyum hatası düzeltilmeli, (b) sunucu katmanı (REST, kuyruk, TLS, audit) sıfırdan bizim inşamız. |
| UYAP kapsamı | Dilekçe imzalama/gönderme **otomatize edilemez** (kapalı sistem). Ama **ekler serbest**: uygulama PAdES imzalı PDF ek üretebilir; UDF içeriği hazırlayıp avukatın UYAP Editör'de imzalamasına bırakabilir. |

---

## 2. İki ayrı problem — bunu ayırmak kritik

Avukatlar USB e-imzayı iki iş için kullanıyor; ikisi farklı mühendislik problemleri:

### (A) Belge imzalama (uygulama içinden)
Sözleşme, ihtarname, yazışma, UYAP eki vb. bir PDF/dosyanın imzalanması. **Temiz çözüm var:** token'ların takılı olduğu sunucuda çalışan bir "imza servisi" — istemciden yalnızca belge/hash gider, imza döner. PKCS#11 hiç ağa çıkmaz; tek HTTPS endpoint'i korursun. Ticari CSC-API ürünleri ve AB'nin açık kaynak DSS projesi aynı desendedir.

### (B) UYAP'a e-imza ile giriş + dilekçe imzalama
UYAP, imzalamayı ve girişi **yalnızca kendi yerel yazılımıyla** yapıyor (UYAP Editör masaüstü uygulaması + "Adalet E-İmza" localhost daemon'u; tarayıcı localhost'taki daemon'la konuşuyor, PIN oraya giriliyor — kaynak: uyap.gov.tr/uyap-eimza, eimza.adalet.gov.tr). Yani token, avukatın oturduğu bilgisayara **yerel bir USB aygıtı gibi** görünmek zorunda; REST imza servisi bu senaryoyu çözemez. Seçenekler:

1. **Uzak masaüstü (bugün çalışan fiili yöntem):** Token'ın takılı olduğu her zaman açık ofis makinesine Chrome Remote Desktop/AnyDesk ile bağlanıp UYAP işini orada yapmak. Türkiye'de avukatların belgelenmiş gerçek pratiği bu (bir avukat telefondan bile imzalıyor: avukatmehmetoban.substack.com). Artı: güvenilir, hemen kurulur. Eksi: aynı anda tek kişi, "o makinede çalışma" deneyimi.
2. **USB-over-IP (gerçek aygıt aktarımı):** USB/IP, VirtualHere veya donanım USB aygıt sunucuları (Digi AnywhereUSB — trafiği varsayılan TLS 1.2 şifreli; SEH/Silex). Token istemci PC'ye gerçekten "takılı" görünür, UYAP uygulaması çalışır. **Riskler belgeli:** VirtualHere'de bazı akıllı kart token'ları hiç çalışmıyor (vendor forumunda çözümsüz vaka), Windows RDP'nin kendi smart-card yönlendirmesi üçüncü parti USB aktarımıyla çakışıyor, macOS desteği kısıtlı, ve aygıt **her an tek kullanıcıya kilitli**. AKİS kartların bu araçlarla uyumu hiçbir yerde teyit edilmemiş — **satın almadan önce 1 token'la pilot şart.**
3. ~~PKCS#11 remoting (p11-kit remote, pkcs11-proxy)~~: UYAP tam yerel sürücü yığını istediği için login senaryosunda işe yaramaz; ayrıca pkcs11-proxy şifresiz ve bakımsız.

**Sonuç:** (A) için imza sunucusu inşa et; (B) için kısa vadede uzak masaüstü + orta vadede Digi tipi TLS'li USB aygıt sunucusu pilotu.

---

## 3. Eşzamanlılık, performans ve PIN gerçekleri

- **"Aynı anda" = kuyruk demek.** Akıllı kart seri bir aygıttır; PKCS#11 spesifikasyonu tek oturuma paralel çağrıyı tanımsız sayar. Kart sınıfı token ~0.3–1.3 sn/imza (RSA-2048 için ~1.5 imza/sn ölçümü var). Çözüm: token başına **tek işçili iş kuyruğu** — 5 kişi aynı anda istek atar, imzalar sırayla ~1'er saniyede atılır; kullanıcı deneyiminde fark hissedilmez. Yargı Asistan'daki `mcp_bridge.py` (asyncio.Lock ile serileştirilmiş tekil köprü) bu desenin hazır şablonu.
- **PIN kilidi ciddi risk:** AKİS tipi kartlarda 3 yanlış PIN kartı kilitler; PUK'ta 3 yanlış deneme kartı **kalıcı ve geri döndürülemez** şekilde öldürür (Kamu SM: mm.kamusm.gov.tr/kilit_cozme). Sunucu tarafında PIN-retry throttling zorunlu; hatalı PIN'i asla otomatik yeniden deneme.
- **PIN saklama politikası (güvenlikten hukuka uzanır):**
  - *İşlem başına sahibi girer* → endüstri standardı ("sole control" kanıtı; CSC API'de SAD belge hash'ine bağlanır, tekrar kullanılamaz). **Önerilen.**
  - *Bellekte süreli önbellek* → Vault/Thales'in belgelediği bilinçli zayıflatma; kısa süreli "imza oturumu" için kabul edilebilir orta yol.
  - *Config/veritabanında kayıtlı PIN* → en zayıf; hiçbir nitelikli imza ürünü bunu yapmıyor. **Yapma.**

---

## 4. eimza-go değerlendirmesi (github.com/KilimcininKorOglu/eimza-go)

**Hüküm: (b) Fork'la ve imza sunucusuna dönüştür — bir zorunlu düzeltmeyle.** Kod satır satır incelendi, derlendi, testleri koşuldu.

**Güçlü yanlar:**
- 4 Go modülü (~26.400 satır): çekirdek kütüphane + CLI + Fyne GUI + e-Yazışma (EYP) paketi. MIT lisanslı, tamamı temiz derleniyor, 200+ test yeşil.
- PKCS#11 katmanı `miekg/pkcs11` üstüne; AKİS dahil yaygın sürücü yolları tanımlı (`akisp11.dll/.so/.dylib`).
- CAdES/XAdES/PAdES/ASiC/CMS üretimi gerçek (ham PKCS#1 değil, gerçek konteynerler); PDF incremental-update motoru, sertifika zinciri + CRL/Delta CRL/OCSP + QCStatements doğrulaması, KamuSM kök sertifikaları gömülü, RFC 3161 istemcisi var.
- **En değerli tasarım kararı:** akıllı kart (`P11Signer`) ve PFX yazılım anahtarı aynı stdlib `crypto.Signer` arayüzünü uyguluyor — imza formatı kodu donanımdan bağımsız; sunucu inşası için ideal.

**Kritik bulgu (yayına çıkmadan düzeltilmeli):**
- `cades/signeddata.go` içindeki gerçek imzalama yolu, ETSI EN 319 122'nin zorunlu kıldığı **`signing-certificate-v2` imzalı özniteliğini eklemiyor.** Kod yazılmış (`attrs_signed.go`) ama hiçbir yerden çağrılmıyor; `ValidateProfile()` da yalnızca testlerden çağrılıyor. Üretilen PKCS#7 kriptografik olarak geçerli ama gerçek dünyadaki CAdES-BES doğrulayıcıları tarafından **reddedilebilir**. 180 test kendi iç tutarlılığını sınadığı için hatayı yakalayamıyor. Düzeltme küçük (fonksiyonu `buildSignedAttributes()`e bağlamak) ama sonrasında harici bir doğrulayıcıyla bağımsız test şart.

**Diğer eksikler / notlar:**
- **Sunucu katmanı sıfır:** hiçbir HTTP sunucusu, kuyruk, goroutine yok — tek kullanıcılık yerel araç. REST API, kimlik doğrulama, TLS, denetim kaydı, oran sınırlama tamamen bizim inşamız (CLI katmanı `os.Exit` çağırdığı için sunucuda CLI değil, doğrudan kütüphane paketleri kullanılmalı).
- **UYAP/UDF desteği yok** (EYP ≠ UDF; EYP kurumlar arası resmî yazışma formatı).
- Tek slot varsayımı (`slots[0]`) — çoklu token için slot seçimi eklenecek; CLI'da sürücü yolu override'ı yok.
- TSA yanıtının kendi imza zinciri doğrulanmıyor; PAdES-EPES yok (README iddiasına rağmen); algoritma kayıtlarında MD5/SHA1 hâlâ seçilebilir (yeni API yüzeyinde kilitlenmeli).
- Olgunluk: tek yazar, 6 commit (ilki 27.495 satırlık tek atış), CI yok, 5 yıldız. Fork = kodu sahiplenmek demek. `kamusm-go` bağımlılığının LICENSE dosyası yok — fork'ta o da ele alınmalı.

**Alternatifler:** EU DSS (Java, LGPL, `Pkcs11SignatureToken` ile en güçlü ücretsiz seçenek) ve pyHanko (Python/MIT, PAdES + PKCS#11). Go tabanlı bağımsız bir servis isteniyorsa eimza-go fork'u; JVM kabulse DSS daha kanıtlanmış. **TÜBİTAK ESYA/MA3 API'ye yaslanma:** ücretsiz dönem 01.06.2026'da bitiyor ilan edildi (sayfa hâlâ gelecek zaman kipinde — `api@kamusm.gov.tr`'den güncel durum teyit edilmeli), sonrası pazarlıklı ücretli lisans.

---

## 5. Format ve ekosistem özeti

- **Profiller:** BTK'nın P1–P4 rehberi (2012/DK-15/299). P1=BES (anlık), P2=ES-T, P3/P4=ES-XL (uzun ömürlü; P4 OCSP'li, BTK'ya göre "en güvenilir"). CAdES=XAdES=PAdES hukuken eşdeğer (2012'den beri).
- **Pratik öneri:** Uygulama içi imzada varsayılan **PAdES** (imzalı PDF'i herkes Adobe'da görür); KEP/EBYS entegrasyonu gerekirse CAdES. Uzun ömür isteyen belgelerde zaman damgalı P4/LTV hedeflenmeli.
- **Zaman damgası:** KamuSM RFC 3161, self-servis (`zdportal.kamusm.gov.tr`), min. 10.000 kontör, kontör süresiz. Rakip fiyat anlık görüntüsü: E-Tuğra 10K ≈ ₺10.899; e-Güven 10K ≈ ₺8.260. Sertifika iptal/bitiminden sonra imzanın kanıtlanabilirliği için pratikte şart.
- **UYAP/UDF sınırı:**
  - Dilekçe **imzalı UDF** olmak zorunda; imza yalnızca kapalı kaynak UYAP Editör / Adalet E-İmza'da atılıyor; bağımsız bir imzalama implementasyonunun kabul edildiğine dair tek örnek yok; **UYAP'ın resmi API'si yok** (yalnızca 48 kurumluk anlaşmalı SOAP entegrasyonları; listede tek bir legal-tech firması yok).
  - **Ekler serbest:** imzalı/imzasız PDF kabul ediliyor (avukat forum teyidi). → Yargı Asistan'ın serbest alanı burası.
  - İmzasız UDF üretimi mümkün (format reverse-engineered: ZIP içinde `content.xml`; imzalıda + `sign.sgn` PKCS#7). `saidsurucu/UDF-Toolkit` (76★) DOCX/PDF↔UDF dönüştürüyor ama **hiçbir repo imzalamıyor**. → "Uygulama UDF'i hazırlar, avukat UYAP Editör'de imzalar/yükler" akışı gerçekçi tavan.
- **Sürücü ekosistemi:** Tek evrensel PKCS#11 modülü yok — en az `akisp11.*` (AKİS: Kamu SM, e-Güven, TürkTrust, E-Tuğra kart hatları), `eTPkcs11`/SAC (SafeNet) ve `aetpkss1.*` (SafeSign) yollarını tarayan otomatik tespit katmanı gerekir. AKİS'in Windows/macOS(Intel+ARM)/Linux native sürücüsü var; TürkTrust ve E-Tuğra token aktivasyonu **yalnızca Windows** (tam Mac/Linux ofis için önemli). Arksigner en iyi çapraz platform desteğini belgeliyor.

---

## 6. Hukuki değerlendirme (araştırma özeti — mütalaa değildir)

**Birincil metinlerden okunan çerçeve (5070 sayılı Kanun + uygulama yönetmeliği):**
- **Md.3(d) + md.4:** "İmza oluşturma verisi" *münhasıran imza sahibine ait* ve güvenli e-imza aracı *"sadece imza sahibinin tasarrufunda"* olmalı. **Md.5:** güvenli e-imza = elle atılan imza.
- **Md.16:** İmza oluşturma verisinin **"rıza dışı"** elde edilmesi/kullanılması suç (1–3 yıl). Lafzen, avukatın *rızasıyla* personelin kullanması bu suçun unsurunu oluşturmuyor görünüyor — ANCAK:
- **Yönetmelik md.15(e):** Sertifika sahibi, imza oluşturma verisini **"başkalarına kullandırmamakla"** yükümlü — bu yüküm **rızadan bağımsız**; gönüllü paylaşım da ihlaldir. ESHS'ler bu uyarıyı yazılı yapmak zorunda (Kanun md.10(f), Yönetmelik md.14).
- **Asıl tehlike delil gücünde:** PIN'i fiilen personel giriyorsa, md.4'ün "sadece sahibinin tasarrufunda" unsuru üzerinden imzanın "güvenli elektronik imza" **vasfı** tartışmaya açılabilir — bu, ofisin *kendi* imzalarının mahkemede sorgulanabilirliği demek (çift taraflı keskin bıçak: sahtecilikte savunma imkânı, ama meşru imzalarda zayıflık).
- **UYAP tarafı:** UYAP bilgi sistemi kuralları kimlik/şifre paylaşımını yasaklıyor ve yapılan her işlemin sorumluluğunu kimin bilgileri kullanıldıysa ona yüklüyor; Avukat Portal sözleşmesinde aynı e-imzayla kısa aralıkta farklı IP'lerden girişte **3 saatlik otomatik bloke** hükmü raporlanıyor (eralp.av.tr ders notları — sözleşme metni birincil kaynaktan ayrıca teyit edilmeli). Paylaşımlı sunucu + avukatın kendi masasından girişi karışırsa bu blokeye takılma riski var.
- **Boşluklar:** Paylaşımlı e-imza kullanımına dair Yargıtay kararı veya baro disiplin kararı **bulunamadı** (yokluk tespiti, "sorun yok" anlamına gelmez). HMK inkâr prosedürüyle kesişim taranmadı. → Uygulamaya geçmeden ofis avukatına şu üç soru sorulmalı: md.4/md.5 vasıf riski, HMK imza inkârı senaryosu, baro/TBB perspektifi.

**Model karşılaştırması:**

| | Tek ortak sertifika, herkes kullanır | Kişi başı token merkezde, yalnızca sahibi tetikler |
|---|---|---|
| Md.4 "sole control" | Yapısal ihlal — neredeyse her imza tartışmalı | İşlem-başı sahibi-PIN'i ile korunur (en güçlü savunma) |
| Md.16 / md.15(e) | Tüm ofis tek kimlik üzerinde birikir | Kişi başına sınırlı |
| PIN kilidi (3 yanlış) | Tüm ofis durur | Tek avukat etkilenir |
| UYAP çoklu-IP blokesi | Sürekli risk | Yönetilebilir |
| Meşru muadili | Kurumsal e-mühür (ama avukatın şahsi imzasının yerini tutmaz) | eIDAS/CSC uzak imza deseni; Türkiye'de mobil imza aynı ilkeyle çalışır |

**Kritik uyarı:** "Merkezileştirme" pratikte *personelin avukatların PIN'lerini girmesine* dönüşürse, ikinci modelin tüm hukuki avantajı sıfırlanır — tasarım gereksinimi fiziksel konsolidasyon değil, **işlem-başı sahibi doğrulamasıdır**. (Türkiye'de ESHS'lerin bireysel nitelikli sertifika için sunucu-HSM'li "uzak imza" ürünü tespit edilemedi; yerleşik meşru uzak imza örneği **mobil imza**dır — SIM içinde anahtar, her işlemi sahibi onaylar. Gerçek ofis-dışı imza ihtiyacı doğarsa değerlendirilecek alternatif budur.)

---

## 7. Yargı Asistan'a entegrasyon planı

Mevcut mimari bu iş için şanslı: uygulama zaten "tek merkezi sunucu + LAN istemcileri" olarak tasarlanmış (SPA'yı FastAPI servis ediyor, prod'da CORS yok), tek eksik `--host 0.0.0.0` ile LAN'a açmak. `mcp_bridge.py` (kilitle serileştirilmiş tekil dış kaynak köprüsü) imza köprüsünün şablonu.

```
[Avukat tarayıcısı] ──HTTPS──▶ [Yargı Asistan FastAPI :8600]
                                    │  iç servis token'ı, localhost/LAN
                                    ▼
                              [İmza Servisi (Go, eimza-go fork)]
                                    │  PKCS#11 (akisp11 vb.)
                                    ▼
                            [USB token'lar — kişi başı slot]
```

**Backend (yeni):**
- `server/app/signing_bridge.py` — mcp_bridge deseninde tekil köprü; Go servisine httpx ile konuşur; token başına kuyruk durumunu izler.
- `server/app/api/signing.py` — `POST /api/signing/documents` (yükleme — repo'da ilk dosya yükleme kodu), `POST /api/signing/requests` (imza talebi; PIN *istek gövdesinde değil*, sahibinin ayrı onay adımında), `GET /api/signing/requests/{id}` veya SSE ile durum. `api/__init__.py`'a kayıt.
- `models.py` — `signing_requests`, `signed_documents`, **append-only `sign_audit_log`** (kim, hangi belge hash'i, hangi sertifika seri no, ne zaman, sonuç; `document_views` yapısı örnek ama silme akışlarına dahil edilmemeli).
- `config.py`/`.env` — imza servisi URL'i, servis token'ı, yükleme limitleri. Quotas deseni imza işlemlerine de uygulanabilir.

**Go imza servisi (fork üstüne inşa):** REST + TLS; sertifika/slot listeleme; token-başına tek işçili kuyruk; işlem-başı PIN akışı (PIN yalnızca sahibinden, kısa ömürlü imza oturumu opsiyonel); PIN-retry throttle; `signing-certificate-v2` düzeltmesi; zaman damgası entegrasyonu; yapılandırılabilir PKCS#11 yol listesi + çoklu slot; yapısal denetim kaydı.

**Frontend:** `AppView`'a `"sign"` üyesi + `SignView` (yükle → imzala → indir → geçmiş); `DocPanel` aksiyon satırına "İmzala"; Sidebar/Header/Icon eklemeleri; durum akışı için mevcut `lib/sse.ts`.

**UYAP girişi (ayrı iş kalemi):** Faz 0'da bir token'la USB-over-IP pilotu (tercihen Digi AnywhereUSB tipi TLS'li donanım; VirtualHere yedek deneme); çalışmazsa resmî çözüm uzak masaüstü rehberi (ofis içi standart kurulum + tek-kullanıcı kilidi görünürlüğü Yargı Asistan'da gösterilebilir: "X token'ı şu an Av. Y kullanıyor").

---

## 8. Yol haritası

| Faz | İçerik | Kaba efor |
|---|---|---|
| **0 — Pilot (önce bu)** | 1 gerçek token'la: eimza-go ile imza + harici doğrulayıcı testi; `signing-certificate-v2` düzeltme + yeniden doğrulama; USB-over-IP/uzak masaüstü UYAP login denemesi | 1–2 hafta |
| **1 — İmza servisi MVP** | Go REST servisi: kuyruk, TLS, servis auth, PIN akışı, audit log, PAdES varsayılan | 2–4 hafta |
| **2 — Yargı Asistan entegrasyonu** | Upload + signing router + bridge + SignView/DocPanel; `--host 0.0.0.0` geçişi | 2–3 hafta |
| **3 — Kurumsal sağlamlaştırma** | KamuSM zaman damgası (kontör hesabı), P4/LTV, çoklu token/slot, kişi-başı yetki matrisi | 2–3 hafta |
| **4 — UYAP yardımcı akışları** | UDF hazırlama (UDF-Toolkit deseni, imzasız) + "UYAP Editör'de tamamla" yönergesi; ops. KEP | ayrı değerlendirme |
| **Sürekli** | Ofis avukatından hukuki teyit (md.4/md.5, HMK inkâr, baro perspektifi); ESYA/MA3 lisans durumu takibi | — |

## 9. Açık sorular / riskler

1. **AKİS + USB-over-IP uyumu hiçbir yerde teyit edilmedi** — satın alma öncesi pilot zorunlu (Faz 0).
2. **UYAP'ın UDF imza doğrulamasının kapsamı bilinmiyor** (salt PKCS#7 mü, sunucu-bağlı doğrulama kodu mu) — dilekçe otomasyonunu "imkânsız" değil "gösterilmemiş" yapan tek belirsizlik; ürün stratejisi buna yaslanmamalı.
3. **Yargıtay içtihadı boşluğu** — paylaşımlı kullanım senaryosu için avukat görüşü alınmadan üretime çıkılmamalı.
4. **eimza-go tek yazarlı** — fork sonrası bakım sahipliği bizde; `kamusm-go` bağımlılığının lisanssızlığı çözülmeli.
5. **UYAP çoklu-IP bloke hükmü** birincil kaynaktan (portal sözleşme metni) teyit edilmeli.
