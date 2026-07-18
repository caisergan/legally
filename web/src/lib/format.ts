import type {
  CitationPayload,
  DocRef,
  DocumentHistoryItem,
  DocumentTarget,
  NormalizedDecision,
} from "./types";

export const SOURCE_COLORS: Record<string, string> = {
  bedesten: "#8A2A33",
  anayasa: "#3B4B8C",
  emsal: "#2E6E6A",
  kik: "#8A5A1E",
  rekabet: "#3F7A55",
  sayistay: "#556072",
  kvkk: "#6B3FA0",
  bddk: "#2A5B8A",
  btk: "#1E7A8A",
  gib: "#8A6A2A",
  uyusmazlik: "#A03D5E",
  sigorta: "#4A6E2E",
};

export const SOURCE_NAMES: Record<string, string> = {
  bedesten: "Bedesten",
  anayasa: "Anayasa Mahkemesi",
  emsal: "Emsal",
  kik: "Kamu İhale Kurulu",
  rekabet: "Rekabet Kurumu",
  sayistay: "Sayıştay",
  kvkk: "KVKK",
  bddk: "BDDK",
  btk: "BTK",
  gib: "Gelir İdaresi",
  uyusmazlik: "Uyuşmazlık Mahkemesi",
  sigorta: "Sigorta Tahkim",
};

const DOC_TOOLS: Record<string, { tool: string; chunked: boolean }> = {
  bedesten: { tool: "get_bedesten_document_markdown", chunked: false },
  anayasa: { tool: "get_anayasa_document_unified", chunked: true },
  emsal: { tool: "get_emsal_document_markdown", chunked: false },
  kik: { tool: "get_kik_v2_document_markdown", chunked: false },
  rekabet: { tool: "get_rekabet_kurumu_document", chunked: true },
  sayistay: { tool: "get_sayistay_document_unified", chunked: false },
  kvkk: { tool: "get_kvkk_document_markdown", chunked: true },
  bddk: { tool: "get_bddk_document_markdown", chunked: true },
  btk: { tool: "get_btk_document_markdown", chunked: true },
  gib: { tool: "get_gib_ozelge_document_markdown", chunked: true },
  uyusmazlik: { tool: "get_uyusmazlik_document_markdown_from_url", chunked: false },
  sigorta: { tool: "get_sigorta_tahkim_document_markdown", chunked: true },
};

export function sourceColor(sourceDb: string): string {
  return SOURCE_COLORS[sourceDb] ?? "var(--ink-3)";
}

export function sourceName(sourceDb: string): string {
  return SOURCE_NAMES[sourceDb] ?? sourceDb;
}

export function displayDate(value?: string | null): string {
  if (!value) return "—";
  const iso = /^(\d{4})-(\d{2})-(\d{2})/.exec(value);
  if (iso) return `${iso[3]}.${iso[2]}.${iso[1]}`;
  return value;
}

export function formatTimestamp(value?: string | null): string {
  if (!value) return "—";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return displayDate(value);
  return new Intl.DateTimeFormat("tr-TR", {
    day: "2-digit",
    month: "2-digit",
    year: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  }).format(date);
}

export function relativeTime(value?: string | null): string {
  if (!value) return "—";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return displayDate(value);
  const seconds = Math.round((date.getTime() - Date.now()) / 1000);
  const formatter = new Intl.RelativeTimeFormat("tr", { numeric: "auto" });
  const units: Array<[Intl.RelativeTimeFormatUnit, number]> = [
    ["year", 31_536_000],
    ["month", 2_592_000],
    ["week", 604_800],
    ["day", 86_400],
    ["hour", 3_600],
    ["minute", 60],
  ];
  for (const [unit, divisor] of units) {
    if (Math.abs(seconds) >= divisor) return formatter.format(Math.round(seconds / divisor), unit);
  }
  return "şimdi";
}

export function initials(name?: string | null, email?: string): string {
  const parts = (name || email || "YA").trim().split(/\s+/).filter(Boolean);
  return parts.slice(0, 2).map((part) => part[0]?.toLocaleUpperCase("tr-TR") ?? "").join("") || "YA";
}

export function decisionRef(decision: Pick<NormalizedDecision, "esas_no" | "karar_no">): string {
  if (!decision.esas_no && !decision.karar_no) return "Referans yok";
  if (!decision.karar_no) return decision.esas_no ?? "—";
  return `E. ${decision.esas_no ?? "—"} · K. ${decision.karar_no}`;
}

export function shortDecisionRef(decision: Pick<NormalizedDecision, "esas_no" | "karar_no">): string {
  if (!decision.karar_no) return decision.esas_no ?? "—";
  return `E.${decision.esas_no ?? "—"} K.${decision.karar_no}`;
}

export function decisionToTarget(decision: NormalizedDecision): DocumentTarget {
  return { ...decision };
}

export function citationToTarget(citation: CitationPayload): DocumentTarget {
  return decisionToTarget(citation.decision);
}

function decodeDocArgs(docKey: string): Record<string, unknown> {
  try {
    const base64 = docKey.replace(/-/g, "+").replace(/_/g, "/");
    const padded = base64 + "=".repeat((4 - (base64.length % 4)) % 4);
    const bytes = Uint8Array.from(atob(padded), (character) => character.charCodeAt(0));
    return JSON.parse(new TextDecoder().decode(bytes)) as Record<string, unknown>;
  } catch {
    return {};
  }
}

export function reconstructDocRef(sourceDb: string, docKey: string): DocRef | undefined {
  const config = DOC_TOOLS[sourceDb];
  if (!config) return undefined;
  const args = decodeDocArgs(docKey);
  if (Object.keys(args).length === 0) return undefined;
  return { tool: config.tool, args, chunked: config.chunked };
}

export function historyDocumentToTarget(item: DocumentHistoryItem): DocumentTarget {
  const meta = item.meta ?? {};
  const text = (key: string): string | null => typeof meta[key] === "string" ? meta[key] as string : null;
  return {
    source_db: item.source_db,
    doc_key: item.doc_key,
    title: item.title,
    doc_ref: reconstructDocRef(item.source_db, item.doc_key),
    court: text("court"),
    esas_no: text("esas_no"),
    karar_no: text("karar_no"),
    decision_date: text("decision_date"),
    source_url: item.source_url ?? null,
  };
}

export function encodeMeta(target: DocumentTarget): string {
  const payload = JSON.stringify({
    court: target.court ?? null,
    esas_no: target.esas_no ?? null,
    karar_no: target.karar_no ?? null,
    decision_date: target.decision_date ?? null,
    source_url: target.source_url ?? null,
  });
  const bytes = new TextEncoder().encode(payload);
  let binary = "";
  bytes.forEach((byte) => { binary += String.fromCharCode(byte); });
  return btoa(binary).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
}

export function citationText(target: DocumentTarget): string {
  const court = target.court || target.title || sourceName(target.source_db);
  const pieces = [court];
  if (target.esas_no) pieces.push(`E. ${target.esas_no}`);
  if (target.karar_no) pieces.push(`K. ${target.karar_no}`);
  if (target.decision_date) pieces.push(`T. ${displayDate(target.decision_date)}`);
  return pieces.join(", ");
}

export async function copyText(value: string): Promise<void> {
  if (navigator.clipboard?.writeText) {
    await navigator.clipboard.writeText(value);
    return;
  }
  const textarea = document.createElement("textarea");
  textarea.value = value;
  textarea.style.position = "fixed";
  textarea.style.opacity = "0";
  document.body.appendChild(textarea);
  textarea.select();
  const copied = document.execCommand("copy");
  textarea.remove();
  if (!copied) throw new Error("Metin panoya kopyalanamadı.");
}

export function normalizeTags(tags: unknown): string[] {
  if (Array.isArray(tags)) return tags.filter((tag): tag is string => typeof tag === "string");
  if (typeof tags === "string") {
    try {
      const parsed = JSON.parse(tags) as unknown;
      if (Array.isArray(parsed)) return parsed.filter((tag): tag is string => typeof tag === "string");
    } catch {
      return tags.split(",").map((tag) => tag.trim()).filter(Boolean);
    }
  }
  return [];
}

export function snapshotResults(snapshot: unknown): NormalizedDecision[] {
  if (!snapshot || typeof snapshot !== "object") return [];
  const candidate = snapshot as { results?: unknown; rows?: unknown; snapshots?: unknown };
  const rows = candidate.results ?? candidate.rows ?? candidate.snapshots;
  if (!Array.isArray(rows)) return [];
  return rows.flatMap((row) => {
    if (!row || typeof row !== "object") return [];
    if ("normalized" in row && typeof (row as { normalized?: unknown }).normalized === "object") {
      return [(row as { normalized: NormalizedDecision }).normalized];
    }
    if ("normalized_json" in row) {
      const raw = (row as { normalized_json?: unknown }).normalized_json;
      if (typeof raw === "string") {
        try { return [JSON.parse(raw) as NormalizedDecision]; } catch { return []; }
      }
      if (raw && typeof raw === "object") return [raw as NormalizedDecision];
    }
    return [row as NormalizedDecision];
  });
}
