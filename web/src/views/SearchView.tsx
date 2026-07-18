import { type CSSProperties, useCallback, useEffect, useMemo, useState } from "react";
import { Icon } from "../components/Icon";
import { BedestenForm } from "../components/search/BedestenForm";
import { GenericForm } from "../components/search/GenericForm";
import type { SearchDraft } from "../components/search/types";
import { apiJson } from "../lib/api";
import { decisionRef, decisionToTarget, displayDate, sourceColor } from "../lib/format";
import type { SearchParams, SearchResponse, SourceMeta } from "../lib/types";
import { useApp } from "../state/app";

function defaultExtra(sourceId: string): Record<string, unknown> {
  switch (sourceId) {
    case "bedesten": return { court_types: ["YARGITAYKARARI"], birimAdi: "ALL" };
    case "anayasa": return { decision_type: "bireysel_basvuru" };
    case "kik": return { decision_type: "uyusmazlik" };
    case "sayistay": return { decision_type: "genel_kurul" };
    case "rekabet": return { KararTuru: "ALL" };
    default: return {};
  }
}

function freshDraft(sourceId: string): SearchDraft {
  return { phrase: "", dateFrom: "", dateTo: "", page: 1, extra: defaultExtra(sourceId) };
}

function paramsFromDraft(draft: SearchDraft, page = draft.page): SearchParams {
  const extra = Object.fromEntries(Object.entries(draft.extra).filter(([, value]) => value !== undefined && value !== ""));
  return {
    phrase: draft.phrase.trim(),
    date_from: draft.dateFrom.trim() || null,
    date_to: draft.dateTo.trim() || null,
    page,
    extra,
  };
}

function draftFromParams(sourceId: string, params?: SearchParams): SearchDraft {
  if (!params) return freshDraft(sourceId);
  return {
    phrase: params.phrase,
    dateFrom: params.date_from ?? "",
    dateTo: params.date_to ?? "",
    page: params.page,
    extra: { ...defaultExtra(sourceId), ...params.extra },
  };
}

function resultRange(response: SearchResponse): string {
  const info = response.page_info;
  const count = response.results.length;
  if (!count) return "0 sonuç";
  const page = info?.page ?? 1;
  const pageSize = info?.page_size ?? count;
  const start = (page - 1) * pageSize + 1;
  const end = start + count - 1;
  return info?.total === null || info?.total === undefined
    ? `${start}–${end} sonuç`
    : `${start}–${end} / ${info.total} sonuç`;
}

export default function SearchView() {
  const {
    sources,
    sourcesLoading,
    refreshSources,
    openDocument,
    sendToChat,
    searchHandoff,
    setSearchHandoff,
  } = useApp();
  const [sourceId, setSourceId] = useState<string | null>(null);
  const [draft, setDraft] = useState<SearchDraft>(freshDraft("bedesten"));
  const [response, setResponse] = useState<SearchResponse | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const source = useMemo(() => sources.find((item) => item.id === sourceId) ?? null, [sourceId, sources]);

  useEffect(() => {
    if (!searchHandoff) return;
    setSourceId(searchHandoff.sourceDb);
    setDraft(draftFromParams(searchHandoff.sourceDb, searchHandoff.params));
    setResponse(searchHandoff.response);
    setError(null);
    setSearchHandoff(null);
  }, [searchHandoff, setSearchHandoff]);

  const selectSource = (next: SourceMeta) => {
    if (!next.available) return;
    setSourceId(next.id);
    setDraft(freshDraft(next.id));
    setResponse(null);
    setError(null);
  };

  const backToSources = () => {
    setSourceId(null);
    setResponse(null);
    setError(null);
  };

  const runSearch = useCallback(async (page = 1) => {
    if (!sourceId) return;
    setLoading(true);
    setError(null);
    const nextDraft = { ...draft, page };
    setDraft(nextDraft);
    try {
      const nextResponse = await apiJson<SearchResponse>(
        `/api/search/${encodeURIComponent(sourceId)}`,
        "POST",
        paramsFromDraft(nextDraft, page),
      );
      setResponse(nextResponse);
    } catch (caught) {
      setResponse(null);
      setError(caught instanceof Error ? caught.message : "Arama tamamlanamadı.");
    } finally {
      setLoading(false);
    }
  }, [draft, sourceId]);

  const continueInChat = () => {
    if (!source || !response) return;
    const resultRefs = response.results.slice(0, 5).map((decision) => decisionRef(decision)).join("; ");
    const searchReference = response.search_id ? ` Arama kimliği: ${response.search_id}.` : "";
    sendToChat(
      `${source.name} veritabanında “${draft.phrase || "son kararlar"}” aramasını sohbet içinde sürdür. ` +
      `Kararları hukuki gerekçeleriyle özetle ve her kararı [n] biçiminde kaynaklandır.${searchReference}` +
      (resultRefs ? ` İlk sonuç referansları: ${resultRefs}.` : ""),
    );
  };

  if (sourcesLoading && sources.length === 0) {
    return <div className="loading-state"><span className="spinner spinner--large" />Veritabanları yükleniyor…</div>;
  }

  return (
    <div className="view-scroll">
      <div className="view-container view-container--search">
        {!sourceId ? (
          <>
            <div className="search-intro">
              <h1 className="view-heading">Yapılandırılmış arama</h1>
              <p className="view-subtitle">Bir veritabanı seçin ve mahkeme, daire, tarih ve esas/karar numarasına göre doğrudan arayın. Formlar canlı araç şemalarından üretilir.</p>
            </div>
            {sources.length === 0 ? (
              <div className="error-state">
                Veritabanı listesi alınamadı.
                <button type="button" className="button button--ghost" onClick={() => void refreshSources()}>Yeniden dene</button>
              </div>
            ) : (
              <div className="source-grid">
                {sources.map((item) => (
                  <button
                    type="button"
                    className="source-card"
                    key={item.id}
                    disabled={!item.available}
                    onClick={() => selectSource(item)}
                    style={{ "--db-color": item.color || sourceColor(item.id) } as CSSProperties}
                  >
                    <span className="source-card-head">
                      <span className="source-square" />
                      <span className="source-name">{item.name}</span>
                      {item.sub && <span className="source-sub">{item.sub}</span>}
                    </span>
                    <span className="source-desc">{item.desc}</span>
                    {!item.available && (
                      <span className="source-gated" title={item.gated_reason || undefined}>
                        <Icon name="lock" size={12} />Sunucuda yapılandırılmamış
                      </span>
                    )}
                  </button>
                ))}
              </div>
            )}
          </>
        ) : source ? (
          <>
            <button type="button" className="search-back" onClick={backToSources}><Icon name="arrow-left" size={15} />Tüm veritabanları</button>
            <div className="search-source-heading" style={{ "--db-color": source.color || sourceColor(source.id) } as CSSProperties}>
              <span className="source-square" />
              <div>
                <h1>{source.name}</h1>
                <p>{source.desc}</p>
              </div>
            </div>

            <section className="search-form" aria-label={`${source.name} arama formu`}>
              {source.id === "bedesten" ? (
                <BedestenForm source={source} draft={draft} onChange={setDraft} />
              ) : (
                <GenericForm source={source} draft={draft} onChange={setDraft} />
              )}
              {error && <div className="form-error" role="alert" style={{ marginBottom: 12 }}>{error}</div>}
              <div className="form-actions">
                <button type="button" className="button button--primary" onClick={() => void runSearch(1)} disabled={loading}>
                  {loading ? <span className="spinner" /> : <Icon name="search" size={16} />}Ara
                </button>
                <button
                  type="button"
                  className="button button--quiet"
                  onClick={() => { setDraft(freshDraft(source.id)); setResponse(null); setError(null); }}
                  disabled={loading}
                >
                  Temizle
                </button>
              </div>
            </section>

            {response && (
              <section className="search-results" aria-live="polite">
                <div className="results-heading">
                  <div className="results-count">{resultRange(response)}</div>
                  <button type="button" className="button button--soft button--compact" onClick={continueInChat}>
                    <Icon name="chat" size={14} />Sohbette devam et
                  </button>
                </div>

                {response.status === "rate_limited" ? (
                  <div className="inline-notice inline-notice--amber">
                    <Icon name="clock" size={15} />Kaynak hız sınırında. {response.retry_after ? `${response.retry_after} saniye sonra yeniden deneyin.` : "Biraz sonra yeniden deneyin."}
                  </div>
                ) : response.status === "source_error" ? (
                  <div className="inline-notice inline-notice--error">{response.error || "Kaynak aramaya yanıt veremedi."}</div>
                ) : response.results.length === 0 ? (
                  <div className="empty-state surface-card">
                    <Icon name="search" size={26} />
                    <h2>Sonuç bulunamadı</h2>
                    <p>{response.error || "Daha geniş bir ifade veya farklı tarih aralığı deneyin."}</p>
                  </div>
                ) : (
                  <div className="results-list">
                    {response.results.map((decision) => (
                      <button
                        type="button"
                        className="result-row"
                        key={decision.doc_key}
                        onClick={() => openDocument(decisionToTarget(decision))}
                      >
                        <span className="db-dot db-dot--sm" style={{ "--db-color": sourceColor(decision.source_db) } as CSSProperties} />
                        <span className="result-main">
                          <span className="result-court">{decision.court || decision.title}</span>
                          <span className="result-snippet">{decision.snippet || decision.title}</span>
                        </span>
                        <span className="result-ref">{decisionRef(decision)}</span>
                        <span className="result-date">{displayDate(decision.decision_date || decision.decision_date_raw)}</span>
                        <Icon name="chevron-right" size={16} className="muted" />
                      </button>
                    ))}
                    <div className="results-footer">
                      <span className="results-note">Metadata-only sonuçlar · özet için karara girin</span>
                      <span className="results-pagination">
                        {(response.page_info?.page ?? 1) > 1 && (
                          <button type="button" className="button button--ghost button--compact" onClick={() => void runSearch((response.page_info?.page ?? 1) - 1)} disabled={loading}>
                            <Icon name="arrow-left" size={14} />Önceki
                          </button>
                        )}
                        <span className="pagination-label">Sayfa {response.page_info?.page ?? draft.page}</span>
                        {response.page_info?.has_more && (
                          <button type="button" className="button button--ghost button--compact" onClick={() => void runSearch((response.page_info?.page ?? 1) + 1)} disabled={loading}>
                            Sonraki sayfa<Icon name="chevron-right" size={14} />
                          </button>
                        )}
                      </span>
                    </div>
                  </div>
                )}
              </section>
            )}
          </>
        ) : (
          <div className="error-state">Veritabanı bilgisi bulunamadı.<button type="button" className="button button--ghost" onClick={backToSources}>Geri dön</button></div>
        )}
      </div>
    </div>
  );
}
