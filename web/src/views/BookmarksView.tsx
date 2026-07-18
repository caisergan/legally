import { type CSSProperties, useCallback, useEffect, useMemo, useState } from "react";
import { Icon } from "../components/Icon";
import { apiFetch, withQuery } from "../lib/api";
import {
  decisionRef,
  displayDate,
  normalizeTags,
  reconstructDocRef,
  sourceColor,
} from "../lib/format";
import type { BookmarkOut, DocumentTarget } from "../lib/types";
import { useApp } from "../state/app";

function targetFromBookmark(bookmark: BookmarkOut): DocumentTarget {
  const meta = bookmark.meta ?? {};
  const text = (key: string) => typeof meta[key] === "string" ? meta[key] as string : null;
  return {
    source_db: bookmark.source_db,
    doc_key: bookmark.doc_key,
    title: bookmark.title,
    doc_ref: reconstructDocRef(bookmark.source_db, bookmark.doc_key),
    court: text("court"),
    esas_no: text("esas_no"),
    karar_no: text("karar_no"),
    decision_date: text("decision_date"),
    source_url: text("source_url"),
  };
}

export default function BookmarksView() {
  const { openDocument } = useApp();
  const [bookmarks, setBookmarks] = useState<BookmarkOut[]>([]);
  const [tags, setTags] = useState<string[]>([]);
  const [activeTag, setActiveTag] = useState("");
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const load = useCallback(async (tag = activeTag) => {
    setLoading(true);
    setError(null);
    try {
      const [bookmarkResponse, tagResponse] = await Promise.all([
        apiFetch<BookmarkOut[] | { bookmarks: BookmarkOut[] }>(withQuery("/api/bookmarks", { tag })),
        apiFetch<string[] | { tags: string[] }>("/api/bookmarks/tags"),
      ]);
      setBookmarks(Array.isArray(bookmarkResponse) ? bookmarkResponse : bookmarkResponse.bookmarks);
      setTags(Array.isArray(tagResponse) ? tagResponse : tagResponse.tags);
    } catch (caught) {
      setError(caught instanceof Error ? caught.message : "Kayıtlı kararlar yüklenemedi.");
    } finally {
      setLoading(false);
    }
  }, [activeTag]);

  useEffect(() => {
    void load(activeTag);
    const refresh = () => { void load(activeTag); };
    window.addEventListener("ya:bookmarks-changed", refresh);
    return () => window.removeEventListener("ya:bookmarks-changed", refresh);
  }, [activeTag, load]);

  const visibleTags = useMemo(() => Array.from(new Set(tags.filter(Boolean))), [tags]);

  return (
    <div className="view-scroll">
      <div className="view-container view-container--bookmarks">
        <h1 className="view-heading">Kayıtlı kararlar</h1>
        <p className="view-subtitle bookmarks-subtitle">Not ve etiketlerle kaydettiğiniz kararlar. Etikete göre filtreleyin.</p>

        <div className="tag-filter" aria-label="Etiket filtresi">
          <button type="button" className={`tag-pill ${activeTag === "" ? "active" : ""}`} onClick={() => setActiveTag("")}>tümü</button>
          {visibleTags.map((tag) => (
            <button type="button" className={`tag-pill ${activeTag === tag ? "active" : ""}`} key={tag} onClick={() => setActiveTag(tag)}>{tag}</button>
          ))}
        </div>

        {error && <div className="inline-notice inline-notice--error" style={{ marginBottom: 12 }}>{error}</div>}
        {loading ? (
          <div className="loading-state"><span className="spinner spinner--large" />Kayıtlı kararlar yükleniyor…</div>
        ) : bookmarks.length === 0 ? (
          <div className="empty-state surface-card">
            <Icon name="bookmark" size={26} />
            <h2>{activeTag ? "Bu etikette karar yok" : "Henüz karar kaydetmediniz"}</h2>
            <p>Arama sonuçlarından veya sohbet kaynaklarından bir kararı açıp “Kaydet”i seçin.</p>
          </div>
        ) : (
          <div className="bookmark-list">
            {bookmarks.map((bookmark) => {
              const target = targetFromBookmark(bookmark);
              const itemTags = normalizeTags(bookmark.tags);
              return (
                <article className="bookmark-card" key={bookmark.id}>
                  <button type="button" className="bookmark-open" onClick={() => openDocument(target)}>
                    <span className="db-dot db-dot--md" style={{ "--db-color": sourceColor(bookmark.source_db) } as CSSProperties} />
                    <span className="bookmark-title">
                      <span className="bookmark-court">{target.court || bookmark.title}</span>
                      <span className="bookmark-ref">{decisionRef({ esas_no: target.esas_no ?? null, karar_no: target.karar_no ?? null })} · {displayDate(target.decision_date)}</span>
                    </span>
                    <Icon name="bookmark" size={17} filled className="muted" style={{ color: "var(--primary)" }} />
                  </button>
                  <div className={`bookmark-note ${bookmark.note ? "" : "empty"}`}>{bookmark.note || "Bu karar için not eklenmemiş."}</div>
                  {itemTags.length > 0 && (
                    <div className="bookmark-tags">
                      {itemTags.map((tag) => <span className="bookmark-tag" key={tag}>#{tag}</span>)}
                    </div>
                  )}
                </article>
              );
            })}
          </div>
        )}
      </div>
    </div>
  );
}
