import { useEffect, useState } from "react";
import { apiFetch } from "../lib/api";
import type { HealthResponse } from "../lib/types";
import { useApp, type AppView } from "../state/app";
import { Icon } from "./Icon";

const titles: Record<AppView, string> = {
  chat: "Sohbet",
  search: "Arama",
  history: "Geçmiş",
  bookmarks: "Kayıtlı",
  settings: "Ayarlar",
};

const healthLabels: Record<HealthResponse["status"], string> = {
  ok: "Bedesten · Yargıtay çalışıyor",
  degraded: "Bedesten · Yargıtay yavaş yanıt veriyor",
  down: "Bedesten · Yargıtay erişilemiyor",
};

export function Header() {
  const { view, theme, setTheme, toggleSidebar } = useApp();
  const [health, setHealth] = useState<HealthResponse["status"]>("degraded");

  useEffect(() => {
    let active = true;
    const load = async () => {
      try {
        const next = await apiFetch<HealthResponse>("/api/meta/health");
        if (active) setHealth(next.status);
      } catch {
        if (active) setHealth("down");
      }
    };
    void load();
    const timer = window.setInterval(load, 60_000);
    return () => {
      active = false;
      window.clearInterval(timer);
    };
  }, []);

  return (
    <header className="app-header">
      <button type="button" className="icon-button menu-button" onClick={toggleSidebar} aria-label="Menüyü aç">
        <Icon name="menu" size={20} />
      </button>
      <div className="header-title">{titles[view]}</div>
      <div className="header-spacer" />
      <div className={`health-chip ${health}`} title={healthLabels[health]} role="status">
        <span className="health-dot" />
        <span className="health-label">{healthLabels[health]}</span>
      </div>
      <button
        type="button"
        className="icon-button"
        onClick={() => setTheme(theme === "light" ? "dark" : "light")}
        title={theme === "light" ? "Koyu temaya geç" : "Açık temaya geç"}
        aria-label={theme === "light" ? "Koyu temaya geç" : "Açık temaya geç"}
      >
        <Icon name={theme === "light" ? "moon" : "sun"} size={17} />
      </button>
    </header>
  );
}
