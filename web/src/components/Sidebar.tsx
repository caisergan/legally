import { useMemo } from "react";
import { initials } from "../lib/format";
import type { ConversationSummary } from "../lib/types";
import { useApp, type AppView } from "../state/app";
import { useAuth } from "../state/auth";
import { Icon, type IconName } from "./Icon";

const navItems: Array<{ view: AppView; label: string; icon: IconName }> = [
  { view: "chat", label: "Sohbet", icon: "chat" },
  { view: "search", label: "Arama", icon: "search" },
  { view: "history", label: "Geçmiş", icon: "history" },
  { view: "bookmarks", label: "Kayıtlı", icon: "bookmark" },
  { view: "signing", label: "E-İmza", icon: "pen" },
];

interface ConversationGroup {
  label: string;
  items: ConversationSummary[];
}

function startOfDay(date: Date): number {
  return new Date(date.getFullYear(), date.getMonth(), date.getDate()).getTime();
}

function groupConversations(rows: ConversationSummary[]): ConversationGroup[] {
  const groups = new Map<string, ConversationSummary[]>();
  const nowDay = startOfDay(new Date());
  rows.forEach((conversation) => {
    const stamp = new Date(conversation.updated_at);
    const days = Number.isNaN(stamp.getTime()) ? 999 : Math.floor((nowDay - startOfDay(stamp)) / 86_400_000);
    const label = days <= 0 ? "Bugün" : days === 1 ? "Dün" : days <= 7 ? "Bu hafta" : "Daha eski";
    const current = groups.get(label) ?? [];
    current.push(conversation);
    groups.set(label, current);
  });
  return ["Bugün", "Dün", "Bu hafta", "Daha eski"]
    .map((label) => ({ label, items: groups.get(label) ?? [] }))
    .filter((group) => group.items.length > 0);
}

export function Sidebar() {
  const { user } = useAuth();
  const {
    view,
    setView,
    sidebarOpen,
    setSidebarOpen,
    conversations,
    conversationsLoading,
    conversationId,
    openConversation,
    beginNewChat,
  } = useApp();
  const groups = useMemo(() => groupConversations(conversations), [conversations]);

  return (
    <>
      <button
        type="button"
        className={`mobile-backdrop ${sidebarOpen ? "visible" : ""}`}
        aria-label="Kenar çubuğunu kapat"
        onClick={() => setSidebarOpen(false)}
      />
      <aside className={`sidebar ${sidebarOpen ? "open" : ""}`} aria-label="Ana gezinme">
        <div className="sidebar-brand">
          <div className="brand">
            <div className="brand-mark"><Icon name="scales" size={19} /></div>
            <div className="brand-copy">
              <div className="brand-name">Yargı Asistan</div>
              <div className="brand-tagline">Hukuki araştırma çalışma alanı</div>
            </div>
          </div>
        </div>

        <div className="new-chat-wrap">
          <button type="button" className="new-chat-button" onClick={beginNewChat}>
            <Icon name="plus" size={16} />
            Yeni sohbet
          </button>
        </div>

        <nav className="sidebar-nav">
          {navItems.map((item) => (
            <button
              type="button"
              key={item.view}
              className={`nav-button ${view === item.view ? "active" : ""}`}
              onClick={() => setView(item.view)}
              aria-current={view === item.view ? "page" : undefined}
            >
              <Icon name={item.icon} size={17} />
              <span>{item.label}</span>
            </button>
          ))}
        </nav>

        <div className="sidebar-section-label">Sohbetler</div>
        <div className="conversation-list">
          {conversationsLoading ? (
            <div className="conversation-empty">Sohbetler yükleniyor…</div>
          ) : groups.length === 0 ? (
            <div className="conversation-empty">İlk araştırmanıza yeni bir sohbetle başlayın.</div>
          ) : groups.map((group) => (
            <div className="conversation-group" key={group.label}>
              <div className="conversation-group-label">{group.label}</div>
              {group.items.map((conversation) => (
                <button
                  type="button"
                  key={conversation.id}
                  className={`conversation-button ${conversationId === conversation.id && view === "chat" ? "active" : ""}`}
                  onClick={() => openConversation(conversation.id)}
                  title={conversation.title || "Başlıksız sohbet"}
                >
                  <span>{conversation.title || "Başlıksız sohbet"}</span>
                </button>
              ))}
            </div>
          ))}
        </div>

        <div className="sidebar-footer">
          <button
            type="button"
            className={`profile-button ${view === "settings" ? "active" : ""}`}
            onClick={() => setView("settings")}
            title="Ayarlar"
          >
            <div className="avatar avatar--small">{initials(user?.display_name, user?.email)}</div>
            <div className="profile-copy">
              <div className="profile-name">{user?.display_name || user?.email || "Kullanıcı"}</div>
              <div className="profile-plan">{user?.role === "admin" ? "Yönetici · yerel" : "Ücretsiz plan"}</div>
            </div>
            <Icon name="settings" size={17} className="muted" />
          </button>
        </div>
      </aside>
    </>
  );
}
