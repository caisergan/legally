import { type FormEvent, type MouseEvent, useCallback, useEffect, useRef, useState } from "react";
import { Icon } from "../components/Icon";
import { encryptPinEnvelope, verifyChallenge, type VerifiedChallenge } from "../lib/pinEnvelope";
import {
  authorizeDownload,
  cancelRequest,
  confirmRequest,
  createRequest,
  downloadSigned,
  getCapabilities,
  getChallengeKeyset,
  getEvents,
  getPinChallenge,
  getRequest,
  listCertificates,
  listRequests,
  sendPinEnvelope,
  uploadArtifact,
  type SigningCapabilities,
  type SigningCertificate,
  type SigningEvent,
  type SigningRequest,
} from "../lib/signing";

type Notice = { type: "error" | "success" | "info"; text: string } | null;
type Dialog = "confirm" | "download" | null;

const TERMINAL_STATES = new Set([
  "COMPLETED", "REJECTED_INPUT", "DECLINED", "CANCELLED", "CONFIRMATION_EXPIRED",
  "APPROVAL_EXPIRED", "PIN_WINDOW_EXPIRED", "PIN_REJECTED", "TOKEN_LOCKED",
  "TOKEN_UNAVAILABLE", "FAILED_PRE_SIGN", "FAILED_POST_SIGN", "OUTCOME_UNKNOWN", "QUARANTINED",
]);

function statusTone(state: string): "pending" | "success" | "warn" | "error" {
  if (state === "COMPLETED") return "success";
  if (state === "QUARANTINED") return "warn";
  if (!TERMINAL_STATES.has(state)) return "pending";
  return "error";
}

function formatBytes(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
}

function hashSuffix(hash: string | null): string {
  return hash ? `…${hash.slice(-16)}` : "—";
}

function noticeFromError(error: unknown, fallback: string): Notice {
  return { type: "error", text: error instanceof Error && error.message ? error.message : fallback };
}

export default function SigningView() {
  const [caps, setCaps] = useState<SigningCapabilities | null>(null);
  const [certificates, setCertificates] = useState<SigningCertificate[]>([]);
  const [requests, setRequests] = useState<SigningRequest[]>([]);
  const [active, setActive] = useState<SigningRequest | null>(null);
  const [events, setEvents] = useState<SigningEvent[]>([]);
  const [loading, setLoading] = useState(true);
  const [notice, setNotice] = useState<Notice>(null);

  const [certId, setCertId] = useState("");
  const [file, setFile] = useState<File | null>(null);
  const [busy, setBusy] = useState<string | null>(null);

  const [dialog, setDialog] = useState<Dialog>(null);
  const [password, setPassword] = useState("");
  const [pinOpen, setPinOpen] = useState(false);
  const [pin, setPin] = useState("");
  const [challenge, setChallenge] = useState<{ verified: VerifiedChallenge; id: string } | null>(null);

  const lastSequence = useRef(0);
  const pinnedKeys = useRef<JsonWebKey[] | null>(null);
  const pinInFlight = useRef(false);

  const bootstrap = useCallback(async () => {
    setLoading(true);
    try {
      const capabilities = await getCapabilities();
      setCaps(capabilities);
      if (capabilities.enabled) {
        const [certs, reqs] = await Promise.all([listCertificates(), listRequests(20)]);
        setCertificates(certs.items);
        setRequests(reqs.items);
        const onlyCert = certs.items[0];
        if (certs.items.length === 1 && onlyCert) setCertId(onlyCert.id);
      }
    } catch (error) {
      setNotice(noticeFromError(error, "E-imza durumu yüklenemedi."));
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => { void bootstrap(); }, [bootstrap]);

  const openRequest = useCallback(async (request: SigningRequest) => {
    setActive(request);
    setEvents([]);
    lastSequence.current = 0;
    setNotice(null);
    try {
      const projected = await getEvents(request.id, 0);
      setEvents(projected.events);
      lastSequence.current = projected.last_sequence;
    } catch {
      // timeline will fill in on the next poll
    }
  }, []);

  const beginPin = useCallback(async (requestId: string) => {
    if (pinInFlight.current) return;
    pinInFlight.current = true;
    try {
      if (!pinnedKeys.current) pinnedKeys.current = (await getChallengeKeyset()).keys;
      const response = await getPinChallenge(requestId);
      const verified = await verifyChallenge(response.challenge_jws, pinnedKeys.current ?? []);
      setChallenge({ verified, id: response.challenge_id });
      setPinOpen(true);
    } catch (error) {
      setNotice(noticeFromError(error, "PIN adımı başlatılamadı."));
    } finally {
      pinInFlight.current = false;
    }
  }, []);

  useEffect(() => {
    if (!active || TERMINAL_STATES.has(active.state)) return;
    let cancelled = false;
    const poll = async () => {
      try {
        const projected = await getEvents(active.id, lastSequence.current);
        if (cancelled) return;
        if (projected.events.length) {
          setEvents((prev) => [...prev, ...projected.events]);
          lastSequence.current = projected.last_sequence;
        }
        if (projected.state !== active.state) {
          const fresh = await getRequest(active.id);
          if (!cancelled) setActive(fresh);
        }
        if (projected.state === "PIN_REQUIRED" && !pinOpen) void beginPin(active.id);
      } catch {
        // ignore transient polling errors
      }
    };
    void poll();
    const timer = window.setInterval(poll, 2500);
    return () => { cancelled = true; window.clearInterval(timer); };
  }, [active, pinOpen, beginPin]);

  const startRequest = async (event: FormEvent) => {
    event.preventDefault();
    if (!file || !certId) return;
    setBusy("create");
    setNotice(null);
    try {
      const artifact = await uploadArtifact(file);
      const request = await createRequest(artifact.id, certId, crypto.randomUUID());
      setRequests((prev) => [request, ...prev.filter((row) => row.id !== request.id)]);
      setFile(null);
      await openRequest(request);
      setNotice({ type: "success", text: "İmza talebi oluşturuldu. İmzalamak için onaylayın." });
    } catch (error) {
      setNotice(noticeFromError(error, "İmza talebi oluşturulamadı."));
    } finally {
      setBusy(null);
    }
  };

  const submitConfirm = async (event: FormEvent) => {
    event.preventDefault();
    if (!active) return;
    setBusy("confirm");
    try {
      const updated = await confirmRequest(active.id, password);
      setPassword("");
      setDialog(null);
      setActive(updated);
      setNotice({ type: "success", text: "Talep imza sırasına alındı." });
    } catch (error) {
      setNotice(noticeFromError(error, "Onaylanamadı."));
    } finally {
      setBusy(null);
    }
  };

  const submitPin = async (event: FormEvent) => {
    event.preventDefault();
    if (!active || !challenge) return;
    setBusy("pin");
    try {
      const envelope = await encryptPinEnvelope(pin, challenge.verified.recipientJwk, challenge.id);
      const result = await sendPinEnvelope(active.id, challenge.id, envelope);
      setPin("");
      setPinOpen(false);
      setChallenge(null);
      if (result.authorization_status === "delivery_unknown") {
        setNotice({ type: "error", text: "PIN gönderimi belirsiz; işlem durumunu izleyin." });
      } else {
        setNotice({ type: "success", text: "PIN alındı, imzalama sürüyor." });
      }
      setActive(await getRequest(active.id));
    } catch (error) {
      setNotice(noticeFromError(error, "PIN gönderilemedi."));
    } finally {
      setBusy(null);
    }
  };

  const submitDownload = async (event: FormEvent) => {
    event.preventDefault();
    if (!active) return;
    setBusy("download");
    try {
      const authorization = await authorizeDownload(active.id, password);
      await downloadSigned(active.id, authorization.download_token, `imzali-${active.id.slice(0, 12)}-signed.pdf`);
      setPassword("");
      setDialog(null);
      setNotice({ type: "success", text: "İmzalı belge indirildi." });
    } catch (error) {
      setNotice(noticeFromError(error, "İndirilemedi."));
    } finally {
      setBusy(null);
    }
  };

  const cancel = async () => {
    if (!active) return;
    setBusy("cancel");
    try {
      const updated = await cancelRequest(active.id);
      setActive(updated);
      setNotice({ type: "info", text: "Talep iptal edildi." });
    } catch (error) {
      setNotice(noticeFromError(error, "İptal edilemedi."));
    } finally {
      setBusy(null);
    }
  };

  const closeDialog = (event: MouseEvent<HTMLDivElement>) => {
    if (event.target === event.currentTarget) { setDialog(null); setPassword(""); }
  };

  if (loading) {
    return (
      <div className="view-scroll"><div className="view-container">
        <div className="loading-state" style={{ minHeight: 200 }}><span className="spinner spinner--large" /></div>
      </div></div>
    );
  }

  return (
    <div className="view-scroll">
      <div className="view-container view-container--settings">
        <div className="signing-head">
          <h1 className="view-heading" style={{ marginBottom: 0 }}>E-İmza</h1>
          {caps?.test_only && <span className="signing-badge">Test ortamı</span>}
        </div>
        <p className="data-copy" style={{ marginTop: 4 }}>
          PDF belgelerinizi nitelikli e-imza token'ınızla imzalayın. PIN'iniz tarayıcıda şifrelenir ve hiçbir zaman kaydedilmez.
        </p>

        {notice && (
          <div className={`inline-notice ${notice.type === "error" ? "inline-notice--error" : ""}`} style={{ margin: "12px 0" }}>
            {notice.text}
          </div>
        )}

        {!caps?.enabled ? (
          <section className="settings-card">
            <div className="settings-section-label">Durum</div>
            <div className="signing-status signing-status--warn">
              <Icon name="lock" size={16} />
              <span>Bu ortamda e-imza şu anda kapalı.</span>
            </div>
            <p className="data-copy" style={{ marginTop: 10 }}>
              Özellik etkinleştirildiğinde sertifikanız, belge yükleme ve imzalama adımları burada görünecek.
            </p>
          </section>
        ) : (
          <>
            <section className="settings-card">
              <div className="settings-section-label">Yeni imza talebi</div>
              {certificates.length === 0 ? (
                <div className="signing-status signing-status--warn">
                  <Icon name="lock" size={16} />
                  <span>Size atanmış kullanılabilir bir sertifika yok.</span>
                </div>
              ) : (
                <form className="signing-form" onSubmit={startRequest}>
                  <label className="field">
                    <span className="field-label">Sertifika</span>
                    {certificates.length === 1 ? (
                      <div className="signing-cert-single">
                        {certificates[0]?.subject_display} · {certificates[0]?.fingerprint_suffix}
                      </div>
                    ) : (
                      <select className="input" value={certId} onChange={(e) => setCertId(e.target.value)} required>
                        <option value="" disabled>Sertifika seçin</option>
                        {certificates.map((cert) => (
                          <option key={cert.id} value={cert.id}>
                            {cert.subject_display} · {cert.fingerprint_suffix}
                          </option>
                        ))}
                      </select>
                    )}
                  </label>
                  <label className="field">
                    <span className="field-label">PDF belge {caps && <span className="field-hint">(en fazla {formatBytes(caps.max_upload_bytes)})</span>}</span>
                    <input
                      className="input signing-file"
                      type="file"
                      accept="application/pdf,.pdf"
                      onChange={(e) => setFile(e.target.files?.[0] ?? null)}
                    />
                  </label>
                  <div className="signing-form-actions">
                    <button type="submit" className="button button--primary" disabled={!file || !certId || busy === "create"}>
                      {busy === "create" ? <span className="spinner" /> : <Icon name="pen" size={15} />}
                      İmza talebi oluştur
                    </button>
                  </div>
                </form>
              )}
            </section>

            {active && <ActiveRequestCard
              request={active}
              events={events}
              busy={busy}
              onConfirm={() => setDialog("confirm")}
              onPin={() => void beginPin(active.id)}
              onDownload={() => setDialog("download")}
              onCancel={() => void cancel()}
            />}

            {requests.length > 0 && (
              <section className="settings-card">
                <div className="settings-section-label">Son talepler</div>
                <div className="signing-request-list">
                  {requests.map((request) => (
                    <button
                      type="button"
                      key={request.id}
                      className={`signing-request-row ${active?.id === request.id ? "active" : ""}`}
                      onClick={() => void openRequest(request)}
                    >
                      <span className={`signing-dot signing-dot--${statusTone(request.state)}`} />
                      <span className="signing-request-main">
                        <span className="signing-request-id mono">{request.id.slice(0, 12)}</span>
                        <span className="signing-request-meta">{formatBytes(request.input_byte_count)} · {new Date(request.created_at).toLocaleString("tr-TR")}</span>
                      </span>
                      <span className="signing-request-state">{request.state_label}</span>
                    </button>
                  ))}
                </div>
              </section>
            )}
          </>
        )}
      </div>

      {dialog === "confirm" && (
        <div className="confirm-overlay" role="presentation" onMouseDown={closeDialog}>
          <form className="confirm-dialog" role="dialog" aria-modal="true" aria-labelledby="signing-confirm-title" onSubmit={submitConfirm}>
            <h2 id="signing-confirm-title">İmzayı onaylayın</h2>
            <p>Bu belgeyi e-imza token'ınızla imzalamak üzere sıraya alacaksınız. Devam etmek için şifrenizi doğrulayın.</p>
            <label className="field">
              <span className="field-label">Mevcut şifre</span>
              <input className="input" type="password" value={password} onChange={(e) => setPassword(e.target.value)} autoComplete="current-password" required autoFocus />
            </label>
            <div className="confirm-actions">
              <button type="button" className="button button--quiet" onClick={() => { setDialog(null); setPassword(""); }}>Vazgeç</button>
              <button type="submit" className="button button--primary" disabled={busy === "confirm" || !password}>{busy === "confirm" && <span className="spinner" />}Onayla ve sıraya al</button>
            </div>
          </form>
        </div>
      )}

      {dialog === "download" && (
        <div className="confirm-overlay" role="presentation" onMouseDown={closeDialog}>
          <form className="confirm-dialog" role="dialog" aria-modal="true" aria-labelledby="signing-download-title" onSubmit={submitDownload}>
            <h2 id="signing-download-title">İmzalı belgeyi indir</h2>
            <p>Tek kullanımlık indirme yetkisi için şifrenizi doğrulayın.</p>
            <label className="field">
              <span className="field-label">Mevcut şifre</span>
              <input className="input" type="password" value={password} onChange={(e) => setPassword(e.target.value)} autoComplete="current-password" required autoFocus />
            </label>
            <div className="confirm-actions">
              <button type="button" className="button button--quiet" onClick={() => { setDialog(null); setPassword(""); }}>Vazgeç</button>
              <button type="submit" className="button button--primary" disabled={busy === "download" || !password}>{busy === "download" && <span className="spinner" />}İndir</button>
            </div>
          </form>
        </div>
      )}

      {pinOpen && challenge && (
        <div className="confirm-overlay" role="presentation">
          <form className="confirm-dialog" role="dialog" aria-modal="true" aria-labelledby="signing-pin-title" onSubmit={submitPin}>
            <h2 id="signing-pin-title">Token PIN'inizi girin</h2>
            <p>PIN'iniz tarayıcınızda tek kullanımlık anahtarla şifrelenir; sunucu veya günlükler PIN'i asla görmez.</p>
            <label className="field">
              <span className="field-label">Token PIN</span>
              <input className="input" type="password" inputMode="numeric" autoComplete="off" value={pin} onChange={(e) => setPin(e.target.value)} required autoFocus />
            </label>
            <div className="confirm-actions">
              <button type="button" className="button button--quiet" onClick={() => { setPinOpen(false); setChallenge(null); setPin(""); }}>Vazgeç</button>
              <button type="submit" className="button button--primary" disabled={busy === "pin" || !pin}>{busy === "pin" && <span className="spinner" />}PIN gönder</button>
            </div>
          </form>
        </div>
      )}
    </div>
  );
}

function ActiveRequestCard({ request, events, busy, onConfirm, onPin, onDownload, onCancel }: {
  request: SigningRequest;
  events: SigningEvent[];
  busy: string | null;
  onConfirm: () => void;
  onPin: () => void;
  onDownload: () => void;
  onCancel: () => void;
}) {
  const tone = statusTone(request.state);
  return (
    <section className="settings-card">
      <div className="settings-section-label">Aktif talep</div>
      <div className={`signing-status signing-status--${tone}`}>
        {request.state === "COMPLETED" ? <Icon name="check" size={16} /> : tone === "pending" ? <span className="spinner" /> : <Icon name="lock" size={16} />}
        <span>{request.state_label}</span>
        {request.state === "QUARANTINED" && <span className="signing-badge">Test çıktısı</span>}
      </div>

      <dl className="signing-manifest">
        <div><dt>Profil</dt><dd className="mono">{request.profile}</dd></div>
        <div><dt>Algoritma</dt><dd className="mono">{request.algorithm}</dd></div>
        <div><dt>Politika</dt><dd className="mono">{request.policy_version}</dd></div>
        <div><dt>Sertifika parmak izi</dt><dd className="mono">{hashSuffix(request.certificate_fingerprint_sha256)}</dd></div>
        <div><dt>Belge özeti</dt><dd className="mono">{hashSuffix(request.input_sha256)}</dd></div>
        <div><dt>Belge boyutu</dt><dd>{formatBytes(request.input_byte_count)}</dd></div>
        {request.output_sha256 && <div><dt>İmzalı çıktı özeti</dt><dd className="mono">{hashSuffix(request.output_sha256)}</dd></div>}
        {request.failure_code && <div><dt>Hata kodu</dt><dd className="mono">{request.failure_code}</dd></div>}
      </dl>

      <div className="signing-actions">
        {request.state === "AWAITING_OWNER_CONFIRMATION" && (
          <button type="button" className="button button--primary" onClick={onConfirm} disabled={busy !== null}><Icon name="check" size={15} />Onayla ve imzala</button>
        )}
        {request.state === "PIN_REQUIRED" && (
          <button type="button" className="button button--primary" onClick={onPin} disabled={busy !== null}><Icon name="lock" size={15} />PIN gir</button>
        )}
        {request.state === "COMPLETED" && (
          <button type="button" className="button button--primary" onClick={onDownload} disabled={busy !== null}><Icon name="download" size={15} />İmzalı belgeyi indir</button>
        )}
        {request.cancellable && (
          <button type="button" className="button button--quiet" onClick={onCancel} disabled={busy !== null}>{busy === "cancel" && <span className="spinner" />}İptal et</button>
        )}
      </div>

      {events.length > 0 && (
        <div className="signing-timeline">
          {events.map((event) => (
            <div className="signing-timeline-row" key={event.sequence}>
              <span className="signing-timeline-dot" />
              <span className="signing-timeline-label">{event.state_label ?? event.type}</span>
              <span className="signing-timeline-time">{new Date(event.created_at).toLocaleTimeString("tr-TR")}</span>
            </div>
          ))}
        </div>
      )}
    </section>
  );
}
