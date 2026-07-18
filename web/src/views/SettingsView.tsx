import { type CSSProperties, type FormEvent, type MouseEvent, useEffect, useMemo, useState } from "react";
import { Icon } from "../components/Icon";
import { apiDownload, apiFetch, apiJson } from "../lib/api";
import { initials, relativeTime } from "../lib/format";
import type { AuthSessionOut, UsageResponse } from "../lib/types";
import { useApp, type Theme } from "../state/app";
import { useAuth } from "../state/auth";

type ConfirmMode = "history" | "account" | null;

const models = [
  { id: "sonnet5", name: "Claude Sonnet 5", note: "Dengeli · varsayılan" },
  { id: "opus", name: "Claude Opus", note: "En yetenekli · daha yavaş" },
  { id: "haiku", name: "Claude Haiku", note: "En hızlı · kısa görevler" },
];

function formatNumber(value: number): string {
  return new Intl.NumberFormat("tr-TR").format(Math.max(0, value));
}

function sessionDevice(userAgent: string | null): string {
  if (!userAgent) return "Bilinmeyen tarayıcı";
  const browser = userAgent.includes("Edg/") ? "Edge"
    : userAgent.includes("Chrome/") ? "Chrome"
      : userAgent.includes("Firefox/") ? "Firefox"
        : userAgent.includes("Safari/") ? "Safari" : "Tarayıcı";
  const os = userAgent.includes("iPhone") ? "iPhone"
    : userAgent.includes("Android") ? "Android"
      : userAgent.includes("Mac OS") ? "macOS"
        : userAgent.includes("Windows") ? "Windows"
          : userAgent.includes("Linux") ? "Linux" : "cihaz";
  return `${browser} · ${os}`;
}

function usageValues(usage: UsageResponse | null) {
  const input = usage?.usage?.llm_input_tokens ?? usage?.llm_input_tokens ?? 0;
  const output = usage?.usage?.llm_output_tokens ?? usage?.llm_output_tokens ?? 0;
  const tools = usage?.usage?.tool_calls ?? usage?.tool_calls ?? 0;
  const tokenLimit = usage?.limits?.llm_tokens ?? usage?.daily_llm_token_quota ?? usage?.token_limit ?? 300_000;
  const toolLimit = usage?.limits?.tool_calls ?? usage?.daily_tool_call_quota ?? usage?.tool_call_limit ?? 300;
  return {
    tokens: input + output,
    tools,
    tokenLimit,
    toolLimit,
  };
}

function UsageMeter({ label, used, total }: { label: string; used: number; total: number }) {
  const percentage = total > 0 ? Math.min(100, (used / total) * 100) : 0;
  const level = percentage > 80 ? "high" : percentage > 50 ? "medium" : "";
  return (
    <div>
      <div className="usage-head">
        <span className="usage-label">{label}</span>
        <span className="usage-value">{formatNumber(used)} / {formatNumber(total)}</span>
      </div>
      <div className="usage-track" role="meter" aria-valuemin={0} aria-valuemax={total} aria-valuenow={used} aria-label={label}>
        <div className={`usage-bar ${level}`} style={{ width: `${percentage}%` }} />
      </div>
    </div>
  );
}

export default function SettingsView() {
  const { user, updateProfile, logout, refreshUser } = useAuth();
  const { theme, setTheme, refreshConversations } = useApp();
  const [sessions, setSessions] = useState<AuthSessionOut[]>([]);
  const [usage, setUsage] = useState<UsageResponse | null>(null);
  const [loading, setLoading] = useState(true);
  const [editingProfile, setEditingProfile] = useState(false);
  const [displayName, setDisplayName] = useState(user?.display_name || "");
  const [saving, setSaving] = useState(false);
  const [busySession, setBusySession] = useState<string | null>(null);
  const [confirmMode, setConfirmMode] = useState<ConfirmMode>(null);
  const [password, setPassword] = useState("");
  const [notice, setNotice] = useState<{ type: "error" | "success"; text: string } | null>(null);

  const loadSettings = async () => {
    setLoading(true);
    try {
      const [sessionRows, usageRow] = await Promise.all([
        apiFetch<AuthSessionOut[] | { sessions: AuthSessionOut[] }>("/api/auth/sessions"),
        apiFetch<UsageResponse>("/api/meta/usage"),
      ]);
      setSessions(Array.isArray(sessionRows) ? sessionRows : sessionRows.sessions);
      setUsage(usageRow);
    } catch (caught) {
      setNotice({ type: "error", text: caught instanceof Error ? caught.message : "Ayarlar yüklenemedi." });
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => { void loadSettings(); }, []);
  useEffect(() => { setDisplayName(user?.display_name || ""); }, [user?.display_name]);

  const usageData = useMemo(() => usageValues(usage), [usage]);
  if (!user) return null;

  const saveProfile = async (event: FormEvent) => {
    event.preventDefault();
    setSaving(true);
    setNotice(null);
    try {
      await updateProfile({ display_name: displayName.trim() || null });
      setEditingProfile(false);
      setNotice({ type: "success", text: "Profil güncellendi." });
    } catch (caught) {
      setNotice({ type: "error", text: caught instanceof Error ? caught.message : "Profil güncellenemedi." });
    } finally {
      setSaving(false);
    }
  };

  const updatePreference = async (field: "locale" | "preferred_model", value: string) => {
    setSaving(true);
    setNotice(null);
    try {
      await updateProfile({ [field]: value });
    } catch (caught) {
      setNotice({ type: "error", text: caught instanceof Error ? caught.message : "Tercih kaydedilemedi." });
    } finally {
      setSaving(false);
    }
  };

  const updateTheme = (next: Theme) => {
    setTheme(next);
    setNotice(null);
  };

  const revokeSession = async (tokenPrefix: string) => {
    setBusySession(tokenPrefix);
    setNotice(null);
    try {
      await apiFetch<void>(`/api/auth/sessions/${encodeURIComponent(tokenPrefix)}`, { method: "DELETE" });
      setSessions((rows) => rows.filter((session) => session.token_prefix !== tokenPrefix));
    } catch (caught) {
      setNotice({ type: "error", text: caught instanceof Error ? caught.message : "Oturum kapatılamadı." });
    } finally {
      setBusySession(null);
    }
  };

  const exportData = async () => {
    setNotice(null);
    try {
      await apiDownload("/api/account/export", `yargi-asistan-verilerim-${new Date().toISOString().slice(0, 10)}.json`);
      setNotice({ type: "success", text: "Veri dışa aktarımı hazırlandı." });
    } catch (caught) {
      setNotice({ type: "error", text: caught instanceof Error ? caught.message : "Veriler indirilemedi." });
    }
  };

  const deleteHistory = async () => {
    setSaving(true);
    try {
      await apiJson<void>("/api/account/delete-history", "POST");
      setConfirmMode(null);
      await refreshConversations();
      setNotice({ type: "success", text: "Arama, belge ve sohbet geçmişi silindi." });
    } catch (caught) {
      setNotice({ type: "error", text: caught instanceof Error ? caught.message : "Geçmiş silinemedi." });
    } finally {
      setSaving(false);
    }
  };

  const deleteAccount = async (event: FormEvent) => {
    event.preventDefault();
    setSaving(true);
    setNotice(null);
    try {
      await apiJson<void>("/api/account/delete", "POST", { password });
      setConfirmMode(null);
      setPassword("");
      await refreshUser();
    } catch (caught) {
      setNotice({ type: "error", text: caught instanceof Error ? caught.message : "Hesap silinemedi." });
    } finally {
      setSaving(false);
    }
  };

  const closeOnBackdrop = (event: MouseEvent<HTMLDivElement>) => {
    if (event.target === event.currentTarget) setConfirmMode(null);
  };

  return (
    <div className="view-scroll">
      <div className="view-container view-container--settings">
        <h1 className="view-heading settings-heading">Ayarlar</h1>
        {notice && <div className={notice.type === "error" ? "inline-notice inline-notice--error" : "inline-notice"} style={{ marginBottom: 14 }}>{notice.text}</div>}

        <section className="settings-card">
          <div className="settings-section-label">Profil</div>
          <div className="profile-row">
            <div className="avatar avatar--large">{initials(user.display_name, user.email)}</div>
            <div className="profile-row-main">
              <div className="profile-row-name">{user.display_name || "İsimsiz kullanıcı"}</div>
              <div className="profile-row-email">{user.email}</div>
            </div>
            <button type="button" className="button button--ghost" onClick={() => setEditingProfile((current) => !current)}>Düzenle</button>
          </div>
          {editingProfile && (
            <form className="profile-edit-form" onSubmit={saveProfile}>
              <input className="input" value={displayName} onChange={(event) => setDisplayName(event.target.value)} placeholder="Ad soyad" autoFocus />
              <button type="submit" className="button button--primary" disabled={saving}>{saving ? <span className="spinner" /> : <Icon name="check" size={15} />}Kaydet</button>
            </form>
          )}
        </section>

        <section className="settings-card">
          <div className="settings-section-label">Aktif oturumlar</div>
          {loading ? <div className="loading-state" style={{ minHeight: 90 }}><span className="spinner" /></div> : (
            <div className="session-list">
              {sessions.map((session) => (
                <div className="session-row" key={session.token_prefix}>
                  <span className="db-dot db-dot--md" style={{ "--db-color": session.current ? "var(--green)" : "var(--ink-3)" } as CSSProperties} />
                  <div className="session-main">
                    <div className="session-device">{sessionDevice(session.user_agent)}</div>
                    <div className="session-meta">Yerel oturum · {relativeTime(session.last_seen_at)}</div>
                  </div>
                  {session.current ? (
                    <span className="current-session">Bu cihaz</span>
                  ) : (
                    <button type="button" className="button button--quiet button--compact" onClick={() => void revokeSession(session.token_prefix)} disabled={busySession === session.token_prefix}>
                      {busySession === session.token_prefix ? <span className="spinner" /> : "Oturumu kapat"}
                    </button>
                  )}
                </div>
              ))}
            </div>
          )}
        </section>

        <div className="settings-split">
          <section className="settings-card">
            <div className="settings-section-label">Dil</div>
            <div className="segment">
              <button type="button" className={`segment-button ${user.locale !== "en" ? "active" : ""}`} disabled={saving} onClick={() => void updatePreference("locale", "tr")}>Türkçe</button>
              <button type="button" className={`segment-button ${user.locale === "en" ? "active" : ""}`} disabled={saving} onClick={() => void updatePreference("locale", "en")}>English</button>
            </div>
          </section>
          <section className="settings-card">
            <div className="settings-section-label">Tema</div>
            <div className="segment">
              <button type="button" className={`segment-button ${theme === "light" ? "active" : ""}`} onClick={() => updateTheme("light")}>Açık</button>
              <button type="button" className={`segment-button ${theme === "dark" ? "active" : ""}`} onClick={() => updateTheme("dark")}>Koyu</button>
            </div>
          </section>
        </div>

        <section className="settings-card">
          <div className="settings-section-label">Model ve günlük kullanım</div>
          <div className="model-list">
            {models.map((model) => {
              const active = (user.preferred_model || "sonnet5") === model.id;
              return (
                <button type="button" className={`model-card ${active ? "active" : ""}`} key={model.id} disabled={saving} onClick={() => void updatePreference("preferred_model", model.id)}>
                  <span className="radio-dot" />
                  <span><span className="model-name">{model.name}</span><span className="model-note">{model.note}</span></span>
                </button>
              );
            })}
          </div>
          <div className="usage-list">
            <UsageMeter label="Dil modeli jetonu" used={usageData.tokens} total={usageData.tokenLimit} />
            <UsageMeter label="Araç çağrısı" used={usageData.tools} total={usageData.toolLimit} />
          </div>
        </section>

        <section className="settings-card">
          <div className="settings-section-label">Veri denetimi</div>
          <p className="data-copy">KVKK kapsamında verileriniz üzerinde tam denetime sahipsiniz. Sorgu geçmişiniz gizli tutulur.</p>
          <div className="data-actions">
            <button type="button" className="button button--ghost" onClick={() => void exportData()}><Icon name="download" size={15} />Tüm verilerimi indir</button>
            <button type="button" className="button button--ghost" onClick={() => setConfirmMode("history")}>Geçmişi sil</button>
            <button type="button" className="button button--danger" onClick={() => setConfirmMode("account")}><Icon name="trash" size={15} />Hesabı sil</button>
            <button type="button" className="button button--quiet" onClick={() => void logout()}>Çıkış yap</button>
          </div>
        </section>
      </div>

      {confirmMode === "history" && (
        <div className="confirm-overlay" role="presentation" onMouseDown={closeOnBackdrop}>
          <div className="confirm-dialog" role="dialog" aria-modal="true" aria-labelledby="history-delete-title">
            <h2 id="history-delete-title">Geçmiş silinsin mi?</h2>
            <p>Arama anlık görüntüleri, görüntülenen kararlar ve sohbetler kalıcı olarak silinecek. Kayıtlı kararlar korunur.</p>
            <div className="confirm-actions">
              <button type="button" className="button button--quiet" onClick={() => setConfirmMode(null)}>Vazgeç</button>
              <button type="button" className="button button--danger" onClick={() => void deleteHistory()} disabled={saving}>{saving && <span className="spinner" />}Geçmişi sil</button>
            </div>
          </div>
        </div>
      )}

      {confirmMode === "account" && (
        <div className="confirm-overlay" role="presentation" onMouseDown={closeOnBackdrop}>
          <form className="confirm-dialog" role="dialog" aria-modal="true" aria-labelledby="account-delete-title" onSubmit={deleteAccount}>
            <h2 id="account-delete-title">Hesap kalıcı olarak silinsin mi?</h2>
            <p>Hesabınız ve ilişkili tüm veriler geri alınamayacak biçimde silinir. Devam etmek için şifrenizi doğrulayın.</p>
            <label className="field">
              <span className="field-label">Mevcut şifre</span>
              <input className="input" type="password" value={password} onChange={(event) => setPassword(event.target.value)} autoComplete="current-password" required autoFocus />
            </label>
            <div className="confirm-actions">
              <button type="button" className="button button--quiet" onClick={() => { setConfirmMode(null); setPassword(""); }}>Vazgeç</button>
              <button type="submit" className="button button--danger" disabled={saving || !password}>{saving && <span className="spinner" />}Hesabı sil</button>
            </div>
          </form>
        </div>
      )}
    </div>
  );
}
