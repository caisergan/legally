import { type CSSProperties, useCallback, useEffect, useMemo, useRef, useState } from "react";
import { apiFetch, apiJson, withQuery } from "../lib/api";
import {
  citationText,
  copyText,
  displayDate,
  encodeMeta,
  formatTimestamp,
  reconstructDocRef,
  sourceColor,
} from "../lib/format";
import type { BookmarkLookup, DocumentResponse } from "../lib/types";
import { useApp } from "../state/app";
import { Icon } from "./Icon";

type MarkdownBlock =
  | { kind: "heading"; text: string }
  | { kind: "paragraph"; text: string }
  | { kind: "quote"; text: string }
  | { kind: "list"; items: string[] };

function markdownBlocks(markdown: string): MarkdownBlock[] {
  const blocks: MarkdownBlock[] = [];
  let paragraph: string[] = [];
  let list: string[] = [];
  const flushParagraph = () => {
    if (paragraph.length) blocks.push({ kind: "paragraph", text: paragraph.join(" ").trim() });
    paragraph = [];
  };
  const flushList = () => {
    if (list.length) blocks.push({ kind: "list", items: list });
    list = [];
  };

  markdown.replace(/\r\n/g, "\n").split("\n").forEach((line) => {
    const trimmed = line.trim();
    if (!trimmed) {
      flushParagraph();
      flushList();
      return;
    }
    const heading = /^#{1,6}\s+(.+)$/.exec(trimmed);
    if (heading?.[1]) {
      flushParagraph();
      flushList();
      blocks.push({ kind: "heading", text: heading[1] });
      return;
    }
    const bullet = /^[-*]\s+(.+)$/.exec(trimmed);
    if (bullet?.[1]) {
      flushParagraph();
      list.push(bullet[1]);
      return;
    }
    if (trimmed.startsWith(">")) {
      flushParagraph();
      flushList();
      blocks.push({ kind: "quote", text: trimmed.slice(1).trim() });
      return;
    }
    flushList();
    paragraph.push(trimmed);
  });
  flushParagraph();
  flushList();
  return blocks;
}

function DocumentMarkdown({ markdown }: { markdown: string }) {
  const blocks = useMemo(() => markdownBlocks(markdown), [markdown]);
  return (
    <div className="doc-content">
      {blocks.map((block, index) => {
        if (block.kind === "heading") return <h3 key={index}>{block.text}</h3>;
        if (block.kind === "quote") return <blockquote key={index}>{block.text}</blockquote>;
        if (block.kind === "list") return <ul key={index}>{block.items.map((item, itemIndex) => <li key={itemIndex}>{item}</li>)}</ul>;
        return <p key={index}>{block.text}</p>;
      })}
    </div>
  );
}

export default function DocPanel() {
  const { documentTarget: target, closeDocument } = useApp();
  const [document, setDocument] = useState<DocumentResponse | null>(null);
  const [page, setPage] = useState(1);
  const [loading, setLoading] = useState(true);
  const [refreshing, setRefreshing] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [bookmarkId, setBookmarkId] = useState<number | null>(null);
  const [bookmarkBusy, setBookmarkBusy] = useState(false);
  const [copied, setCopied] = useState(false);
  const scrollRef = useRef<HTMLDivElement | null>(null);

  const loadDocument = useCallback(async (nextPage: number, signal?: AbortSignal) => {
    if (!target) return;
    setLoading(true);
    setError(null);
    try {
      const path = withQuery(
        `/api/documents/${encodeURIComponent(target.source_db)}/${encodeURIComponent(target.doc_key)}`,
        { page: nextPage, meta: encodeMeta(target) },
      );
      const nextDocument = await apiFetch<DocumentResponse>(path, { signal });
      setDocument(nextDocument);
      setPage(nextDocument.page || nextPage);
      scrollRef.current?.scrollTo({ top: 0, behavior: "smooth" });
    } catch (caught) {
      if (!(caught instanceof DOMException && caught.name === "AbortError")) {
        setError(caught instanceof Error ? caught.message : "Karar metni alınamadı.");
      }
    } finally {
      setLoading(false);
    }
  }, [target]);

  useEffect(() => {
    if (!target) return;
    const controller = new AbortController();
    setDocument(null);
    setPage(1);
    setBookmarkId(null);
    const documentPromise = loadDocument(1, controller.signal);
    const lookupPromise = apiFetch<BookmarkLookup>(withQuery("/api/bookmarks/lookup", {
      source_db: target.source_db,
      doc_key: target.doc_key,
    }), { signal: controller.signal })
      .then((lookup) => setBookmarkId(lookup.bookmarked ? lookup.id ?? null : null))
      .catch(() => setBookmarkId(null));
    void Promise.allSettled([documentPromise, lookupPromise]);
    return () => controller.abort();
  }, [loadDocument, target]);

  useEffect(() => {
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === "Escape") closeDocument();
    };
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, [closeDocument]);

  if (!target) return null;

  const court = document?.meta.court || target.court || document?.title || target.title;
  const esas = document?.meta.esas_no || target.esas_no;
  const karar = document?.meta.karar_no || target.karar_no;
  const date = document?.meta.decision_date || target.decision_date || target.decision_date_raw;
  const sourceUrl = document?.source_url || target.source_url;
  const refTarget = { ...target, court, esas_no: esas, karar_no: karar, decision_date: date };
  const totalPages = document?.total_pages ?? null;
  const hasPrevious = page > 1;
  const hasNext = Boolean(document?.chunked && (totalPages === null || page < totalPages));

  const toggleBookmark = async () => {
    setBookmarkBusy(true);
    setError(null);
    try {
      if (bookmarkId) {
        await apiFetch<void>(`/api/bookmarks/${bookmarkId}`, { method: "DELETE" });
        setBookmarkId(null);
      } else {
        const docRef = target.doc_ref ?? reconstructDocRef(target.source_db, target.doc_key);
        if (!docRef) throw new Error("Bu belge için kaynak başvurusu yeniden oluşturulamadı.");
        const created = await apiJson<{ id?: number }>("/api/bookmarks", "POST", {
          source_db: target.source_db,
          doc_key: target.doc_key,
          doc_ref: docRef,
          title: document?.title || target.title,
          meta: {
            court,
            esas_no: esas,
            karar_no: karar,
            decision_date: date,
            source_url: sourceUrl,
          },
          tags: [],
        });
        if (created.id) {
          setBookmarkId(created.id);
        } else {
          const lookup = await apiFetch<BookmarkLookup>(withQuery("/api/bookmarks/lookup", {
            source_db: target.source_db,
            doc_key: target.doc_key,
          }));
          setBookmarkId(lookup.id ?? null);
        }
      }
      window.dispatchEvent(new CustomEvent("ya:bookmarks-changed"));
    } catch (caught) {
      setError(caught instanceof Error ? caught.message : "Kayıt durumu değiştirilemedi.");
    } finally {
      setBookmarkBusy(false);
    }
  };

  const copyCitation = async () => {
    try {
      await copyText(citationText(refTarget));
      setCopied(true);
      window.setTimeout(() => setCopied(false), 1_600);
    } catch {
      setError("Atıf panoya kopyalanamadı.");
    }
  };

  const refresh = async () => {
    setRefreshing(true);
    setError(null);
    try {
      const refreshPath = withQuery(
        `/api/documents/${encodeURIComponent(target.source_db)}/${encodeURIComponent(target.doc_key)}/refresh`,
        { meta: encodeMeta(target) },
      );
      const nextDocument = await apiJson<DocumentResponse>(refreshPath, "POST");
      setDocument(nextDocument);
      setPage(nextDocument.page || 1);
      scrollRef.current?.scrollTo({ top: 0, behavior: "smooth" });
    } catch (caught) {
      setError(caught instanceof Error ? caught.message : "Belge yenilenemedi.");
    } finally {
      setRefreshing(false);
    }
  };

  return (
    <aside className="doc-panel" aria-label="Karar metni" aria-live="polite">
      <div className="doc-header">
        <span className="db-dot db-dot--md" style={{ "--db-color": sourceColor(target.source_db) } as CSSProperties} />
        <div className="doc-header-title">{court}</div>
        <button type="button" className="icon-button" onClick={closeDocument} aria-label="Karar panelini kapat">
          <Icon name="close" size={16} />
        </button>
      </div>

      <div className="doc-body" ref={scrollRef}>
        {loading && !document ? (
          <div className="doc-loading"><span className="spinner spinner--large" /></div>
        ) : error && !document ? (
          <div className="error-state">
            {error}
            <button type="button" className="button button--ghost" onClick={() => void loadDocument(page)}>Yeniden dene</button>
          </div>
        ) : document ? (
          <>
            {document.cached && (
              <div className="cache-badge"><Icon name="refresh" size={12} />önbellekten · {displayDate(document.fetched_at)}</div>
            )}
            <h2 className="doc-title">{document.title || target.title}</h2>
            <div className="doc-meta">
              <span><span className="doc-meta-label">Esas</span> {esas || "—"}</span>
              <span><span className="doc-meta-label">Karar</span> {karar || "—"}</span>
              <span><span className="doc-meta-label">Tarih</span> {displayDate(date)}</span>
            </div>
            <div className="doc-actions">
              <button
                type="button"
                className={`button button--ghost bookmark-toggle ${bookmarkId ? "active" : ""}`}
                onClick={() => void toggleBookmark()}
                disabled={bookmarkBusy}
              >
                {bookmarkBusy ? <span className="spinner" /> : <Icon name="bookmark" size={15} filled={Boolean(bookmarkId)} />}
                {bookmarkId ? "Kaydedildi" : "Kaydet"}
              </button>
              <button type="button" className="button button--ghost" onClick={() => void copyCitation()}>
                <Icon name={copied ? "check" : "copy"} size={15} />{copied ? "Kopyalandı" : "Atıf kopyala"}
              </button>
              {sourceUrl ? (
                <a className="button button--ghost" href={sourceUrl} target="_blank" rel="noreferrer">
                  <Icon name="external" size={15} />Kaynak
                </a>
              ) : (
                <button type="button" className="button button--ghost" disabled><Icon name="external" size={15} />Kaynak</button>
              )}
              <button type="button" className="button button--quiet" onClick={() => void refresh()} disabled={refreshing} title="Önbelleği atlayıp yenile">
                {refreshing ? <span className="spinner" /> : <Icon name="refresh" size={15} />}Yenile
              </button>
            </div>
            {error && <div className="inline-notice inline-notice--error" style={{ marginBottom: 14 }}>{error}</div>}
            <DocumentMarkdown markdown={document.markdown} />
            {document.chunked && (
              <div className="doc-pager">
                <button type="button" className="button button--ghost" disabled={!hasPrevious || loading} onClick={() => void loadDocument(page - 1)}>
                  <Icon name="arrow-left" size={15} />Önceki sayfa
                </button>
                <div className="doc-page-label">Sayfa {page}{totalPages ? ` / ${totalPages}` : ""}</div>
                <button type="button" className="button button--primary" disabled={!hasNext || loading} onClick={() => void loadDocument(page + 1)}>
                  Sonraki sayfa<Icon name="chevron-right" size={15} />
                </button>
              </div>
            )}
            <div className="sr-only">Belge alınma zamanı: {formatTimestamp(document.fetched_at)}</div>
          </>
        ) : null}
      </div>
    </aside>
  );
}
