import { type FormEvent, useState } from "react";
import { useAuth } from "../state/auth";
import { Icon } from "../components/Icon";

type AuthMode = "login" | "register";

export function AuthView() {
  const { login, register } = useAuth();
  const [mode, setMode] = useState<AuthMode>("login");
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [displayName, setDisplayName] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);

  const submit = async (event: FormEvent) => {
    event.preventDefault();
    setError(null);
    setSubmitting(true);
    try {
      if (mode === "login") {
        await login({ email: email.trim(), password });
      } else {
        await register({
          email: email.trim(),
          password,
          display_name: displayName.trim() || undefined,
        });
      }
    } catch (caught) {
      setError(caught instanceof Error ? caught.message : "İşlem tamamlanamadı.");
    } finally {
      setSubmitting(false);
    }
  };

  const switchMode = () => {
    setMode((current) => current === "login" ? "register" : "login");
    setError(null);
  };

  return (
    <main className="auth-page">
      <section className="auth-card" aria-labelledby="auth-title">
        <div className="auth-brand">
          <div className="brand-mark brand-mark--large"><Icon name="scales" size={25} /></div>
          <h1 className="auth-title" id="auth-title">{mode === "login" ? "Giriş yap" : "Hesap oluştur"}</h1>
          <p className="auth-subtitle">Yargı Asistan · hukuki araştırma çalışma alanı</p>
        </div>

        <form className="auth-form" onSubmit={submit}>
          {mode === "register" && (
            <label className="field">
              <span className="field-label">Ad soyad <span className="field-hint">· isteğe bağlı</span></span>
              <input
                className="input"
                value={displayName}
                onChange={(event) => setDisplayName(event.target.value)}
                autoComplete="name"
                placeholder="Av. Selin Aydın"
              />
            </label>
          )}
          <label className="field">
            <span className="field-label">E-posta</span>
            <input
              className="input"
              type="email"
              value={email}
              onChange={(event) => setEmail(event.target.value)}
              autoComplete="email"
              placeholder="ad@kurum.com"
              required
            />
          </label>
          <label className="field">
            <span className="field-label">Şifre</span>
            <input
              className="input"
              type="password"
              value={password}
              onChange={(event) => setPassword(event.target.value)}
              autoComplete={mode === "login" ? "current-password" : "new-password"}
              minLength={8}
              placeholder="En az 8 karakter"
              required
            />
          </label>
          {error && <div className="form-error" role="alert">{error}</div>}
          <button className="button button--primary auth-submit" type="submit" disabled={submitting}>
            {submitting && <span className="spinner" />}
            {mode === "login" ? "Giriş yap" : "Hesap oluştur"}
          </button>
        </form>

        <div className="auth-switch">
          {mode === "login" ? "Hesabınız yok mu?" : "Zaten hesabınız var mı?"}
          {" "}
          <button type="button" onClick={switchMode}>
            {mode === "login" ? "Hesap oluştur" : "Giriş yap"}
          </button>
        </div>
      </section>
    </main>
  );
}
