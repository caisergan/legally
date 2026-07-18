import { type CSSProperties, useEffect, useMemo, useState } from "react";
import { Icon } from "../components/Icon";
import { apiFetch, apiJson } from "../lib/api";
import {
  decisionRef,
  displayDate,
  historyDocumentToTarget,
  relativeTime,
  snapshotResults,
  sourceColor,
  sourceName,
} from "../lib/format";
import type {
  ConversationDetail,
  DocumentHistoryItem,
  SearchHistoryItem,
  SearchResponse,
  SearchSnapshot,
} from "../lib/types";
import { useApp } from "../state/app";

type HistoryTab = "searches" | "chats" | "documents";

interface ChatPreview {
  conversationId: string;
  text: string;
}

function statusLabel(item: SearchHistoryItem): string {
  if (item.status === "ok") return `${item.result_count ?? 0} sonuç`;
  if (item.status === "empty") return "sonuç yok";
  if (item.status === "rate_limited" || item.status === "rate") return "hız sınırı";
  return "kaynak hatası";
}

function historyDate(item: DocumentHistoryItem): string | undefined {
  return item.last_viewed_at || item.opened_at || item.viewed_at || item.created_at;
}

export default function HistoryView() {
  const {
    conversations,
    refreshConversations,
    openConversation,
    openDocument,
    setSearchHandoff,
    setView,
  } = useApp();
  const [tab, setTab] = useState<HistoryTab>("searches");
  const [query, setQuery] = useState("");
  const [searches, setSearches] = useState<SearchHistoryItem[]>([]);
  const [documents, setDocuments] = useState<DocumentHistoryItem[]>([]);
  const [chatPreviews, setChatPreviews] = useState<Record<string, string>>({});
  const [snapshot, setSnapshot] = useState<{ searchId: string; data: SearchSnapshot } | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [busyId, setBusyId] = useState<string | null>(null);

  const loadHistory = async () => {
    setLoading(true);
    setError(null);
    try {
      const [searchResponse, documentResponse] = await Promise.all([
        apiFetch<SearchHistoryItem[] | { searches: SearchHistoryItem[] }>("/api/history/searches?limit=50&offset=0"),
        apiFetch<DocumentHistoryItem[] | { documents: DocumentHistoryItem[] }>("/api/history/documents?limit=50"),
        refreshConversations(),
      ]);
      setSearches(Array.isArray(searchResponse) ? searchResponse : searchResponse.searches);
      setDocuments(Array.isArray(documentResponse) ? documentResponse : documentResponse.documents);
    } catch (caught) {
      setError(caught instanceof Error ? caught.message : "Geçmiş yüklenemedi.");
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => { void loadHistory(); }, []);

  useEffect(() => {
    if (tab !== "chats" || conversations.length === 0) return;
    let active = true;
    const loadPreviews = async () => {
      const details = await Promise.allSettled(
        conversations.slice(0, 20).map((conversation) => apiFetch<ConversationDetail>(`/api/conversations/${conversation.id}`)),
      );
      if (!active) return;
      const next: Record<string, string> = {};
      details.forEach((result, index) => {
        const conversation = conversations[index];
        if (!conversation || result.status !== "fulfilled") return;
        const last = [...result.value.messages].reverse().find((message) => message.content.trim());
        next[conversation.id] = last?.content || "Henüz mesaj yok.";
      });
      setChatPreviews(next);
    };
    void loadPreviews();
    return () => { active = false; };
  }, [conversations, tab]);

  const normalizedQuery = query.trim().toLocaleLowerCase("tr-TR");
  const filteredSearches = useMemo(() => searches.filter((item) => !normalizedQuery ||
    item.query_text.toLocaleLowerCase("tr-TR").includes(normalizedQuery) ||
    sourceName(item.source_db).toLocaleLowerCase("tr-TR").includes(normalizedQuery)), [normalizedQuery, searches]);
  const filteredChats = useMemo(() => conversations.filter((conversation) => !normalizedQuery ||
    (conversation.title || "Başlıksız sohbet").toLocaleLowerCase("tr-TR").includes(normalizedQuery) ||
    (chatPreviews[conversation.id] || "").toLocaleLowerCase("tr-TR").includes(normalizedQuery)), [chatPreviews, conversations, normalizedQuery]);
  const filteredDocuments = useMemo(() => documents.filter((item) => !normalizedQuery ||
    item.title.toLocaleLowerCase("tr-TR").includes(normalizedQuery) ||
    String(item.meta?.court || "").toLocaleLowerCase("tr-TR").includes(normalizedQuery)), [documents, normalizedQuery]);

  const rerun = async (item: SearchHistoryItem) => {
    setBusyId(item.id);
    setError(null);
    try {
      const response = await apiJson<SearchResponse>(`/api/search/${item.id}/rerun`, "POST");
      setSearchHandoff({
        sourceDb: item.source_db,
        params: { phrase: item.query_text, date_from: null, date_to: null, page: 1, extra: {} },
        response,
      });
      setView("search");
    } catch (caught) {
      setError(caught instanceof Error ? caught.message : "Arama yeniden çalıştırılamadı.");
    } finally {
      setBusyId(null);
    }
  };

  const openSnapshot = async (searchId: string) => {
    if (snapshot?.searchId === searchId) {
      setSnapshot(null);
      return;
    }
    setBusyId(searchId);
    setError(null);
    try {
      const data = await apiFetch<SearchSnapshot>(`/api/search/${searchId}/snapshot`);
      setSnapshot({ searchId, data });
    } catch (caught) {
      setError(caught instanceof Error ? caught.message : "Anlık görüntü yüklenemedi.");
    } finally {
      setBusyId(null);
    }
  };

  const snapshotRows = snapshot ? snapshotResults(snapshot.data) : [];

  return (
    <div className="view-scroll">
      <div className="view-container view-container--history">
        <h1 className="view-heading">Geçmiş</h1>
        <p className="view-subtitle history-subtitle">Yerel olarak saklanan aramalar, sohbetler ve görüntülenen kararlar. Sonuçlar anlık görünür; güncel veri için yeniden çalıştırın.</p>

        <div className="history-toolbar">
          <div className="segment" role="tablist" aria-label="Geçmiş türü">
            <button type="button" role="tab" className={`segment-button ${tab === "searches" ? "active" : ""}`} onClick={() => setTab("searches")}>Aramalar</button>
            <button type="button" role="tab" className={`segment-button ${tab === "chats" ? "active" : ""}`} onClick={() => setTab("chats")}>Sohbetler</button>
            <button type="button" role="tab" className={`segment-button ${tab === "documents" ? "active" : ""}`} onClick={() => setTab("documents")}>Görüntülenen</button>
          </div>
          <label className="history-search">
            <Icon name="search" size={15} className="muted" />
            <span className="sr-only">Geçmişte ara</span>
            <input value={query} onChange={(event) => setQuery(event.target.value)} placeholder="Geçmişte ara…" />
          </label>
        </div>

        {error && <div className="inline-notice inline-notice--error" style={{ marginBottom: 12 }}>{error}</div>}
        {loading ? (
          <div className="loading-state"><span className="spinner spinner--large" />Geçmiş yükleniyor…</div>
        ) : tab === "searches" ? (
          filteredSearches.length === 0 ? <div className="empty-state surface-card"><Icon name="search" size={24} /><h2>Arama geçmişi boş</h2></div> : (
            <div className="history-list">
              {filteredSearches.map((item) => (
                <div key={item.id}>
                  <div className="history-search-row">
                    <span className="db-dot db-dot--sm" style={{ "--db-color": sourceColor(item.source_db) } as CSSProperties} />
                    <div className="history-item-main">
                      <div className="history-query-line">
                        <span className="history-query">«{item.query_text || "Tüm kararlar"}»</span>
                        <span className="history-db">{sourceName(item.source_db)}</span>
                      </div>
                      <div className="history-meta">{relativeTime(item.created_at)} · anlık görüntü {displayDate(item.created_at)}</div>
                    </div>
                    <span className={`history-status ${item.status}`}>{statusLabel(item)}</span>
                    <button type="button" className="button button--ghost button--compact" onClick={() => void rerun(item)} disabled={busyId === item.id}>
                      {busyId === item.id ? <span className="spinner" /> : <Icon name="refresh" size={13} />}Yeniden
                    </button>
                    <button type="button" className="button button--quiet button--compact" onClick={() => void openSnapshot(item.id)} disabled={busyId === item.id}>
                      Anlık görüntü
                    </button>
                  </div>
                  {snapshot?.searchId === item.id && (
                    <div className="snapshot-panel">
                      <div className="snapshot-head">
                        <Icon name="database" size={14} />
                        <span className="snapshot-title">Saklanan anlık görüntü · {snapshotRows.length} karar</span>
                        <button type="button" className="icon-button" onClick={() => setSnapshot(null)} aria-label="Anlık görüntüyü kapat"><Icon name="close" size={14} /></button>
                      </div>
                      {snapshotRows.length === 0 ? (
                        <div className="muted" style={{ fontSize: 12 }}>Bu aramada saklanan sonuç yok.</div>
                      ) : (
                        <div className="results-list">
                          {snapshotRows.map((decision) => (
                            <button type="button" className="result-row" key={decision.doc_key} onClick={() => openDocument(decision)}>
                              <span className="db-dot db-dot--sm" style={{ "--db-color": sourceColor(decision.source_db) } as CSSProperties} />
                              <span className="result-main"><span className="result-court">{decision.court || decision.title}</span><span className="result-snippet">{decision.snippet || decision.title}</span></span>
                              <span className="result-ref">{decisionRef(decision)}</span>
                              <span className="result-date">{displayDate(decision.decision_date || decision.decision_date_raw)}</span>
                              <Icon name="chevron-right" size={16} className="muted" />
                            </button>
                          ))}
                        </div>
                      )}
                    </div>
                  )}
                </div>
              ))}
            </div>
          )
        ) : tab === "chats" ? (
          filteredChats.length === 0 ? <div className="empty-state surface-card"><Icon name="chat" size={24} /><h2>Sohbet geçmişi boş</h2></div> : (
            <div className="history-list">
              {filteredChats.map((conversation) => (
                <button type="button" className="chat-history-row" key={conversation.id} onClick={() => openConversation(conversation.id)}>
                  <div className="chat-history-icon"><Icon name="chat" size={17} /></div>
                  <div className="chat-history-copy">
                    <div className="chat-history-title-row">
                      <span className="chat-history-title">{conversation.title || "Başlıksız sohbet"}</span>
                      <span className="chat-history-time">{relativeTime(conversation.updated_at)}</span>
                    </div>
                    <div className="chat-history-preview">{chatPreviews[conversation.id] || "Sohbet önizlemesi yükleniyor…"}</div>
                  </div>
                </button>
              ))}
            </div>
          )
        ) : (
          filteredDocuments.length === 0 ? <div className="empty-state surface-card"><Icon name="database" size={24} /><h2>Görüntülenen karar yok</h2></div> : (
            <div className="results-list">
              {filteredDocuments.map((item) => {
                const target = historyDocumentToTarget(item);
                return (
                  <button type="button" className="result-row" key={`${item.source_db}-${item.doc_key}`} onClick={() => openDocument(target)}>
                    <span className="db-dot db-dot--sm" style={{ "--db-color": sourceColor(item.source_db) } as CSSProperties} />
                    <span className="result-main">
                      <span className="result-court">{String(item.meta?.court || item.title)}</span>
                      <span className="result-snippet mono">{decisionRef({ esas_no: target.esas_no ?? null, karar_no: target.karar_no ?? null })} · {displayDate(target.decision_date)}</span>
                    </span>
                    <span className="history-meta">{relativeTime(historyDate(item))}</span>
                    <Icon name="chevron-right" size={16} className="muted" />
                  </button>
                );
              })}
            </div>
          )
        )}
      </div>
    </div>
  );
}
