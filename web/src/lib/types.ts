export type JsonPrimitive = string | number | boolean | null;
export type JsonValue = JsonPrimitive | JsonValue[] | { [key: string]: JsonValue };
export type JsonObject = Record<string, unknown>;

export interface DocRef {
  tool: string;
  args: Record<string, unknown>;
  chunked: boolean;
}

export interface NormalizedDecision {
  source_db: string;
  doc_key: string;
  doc_ref: DocRef;
  title: string;
  court: string | null;
  esas_no: string | null;
  karar_no: string | null;
  decision_date: string | null;
  decision_date_raw: string | null;
  snippet: string | null;
  source_url: string | null;
  extra: Record<string, unknown>;
}

export interface PageInfo {
  page: number;
  page_size: number;
  total: number | null;
  has_more: boolean;
}

export interface SearchParams {
  phrase: string;
  date_from: string | null;
  date_to: string | null;
  page: number;
  extra: Record<string, unknown>;
}

export type SearchStatus = "ok" | "empty" | "rate_limited" | "source_error";

export interface SearchOutcome {
  status: SearchStatus;
  results: NormalizedDecision[];
  page_info: PageInfo | null;
  error: string | null;
  retry_after: number | null;
}

export interface SearchResponse extends SearchOutcome {
  search_id?: string;
}

export interface UserOut {
  id: number;
  email: string;
  display_name: string | null;
  role: string;
  locale: string;
  theme: "light" | "dark" | string;
  preferred_model: "sonnet5" | "opus" | "haiku" | string | null;
  created_at: string;
}

export interface ModelOption {
  key: "sonnet5" | "opus" | "haiku";
  model: string;
  name: string;
  description: string;
}

export interface MetaOption {
  value?: string;
  id?: string;
  key?: string;
  label: string;
  group?: string;
  disabled?: boolean;
}

export interface SourceMeta {
  id: string;
  name: string;
  sub: string;
  desc: string;
  color: string;
  available: boolean;
  gated_reason?: string | null;
  decision_types?: Array<MetaOption | string>;
  court_types?: Array<MetaOption | string>;
  birim_options?: Array<MetaOption | string>;
  karar_turu_options?: Array<MetaOption | string>;
  [key: string]: unknown;
}

export interface HealthResponse {
  status: "ok" | "degraded" | "down";
  detail: unknown;
}

export interface UsageResponse {
  llm_input_tokens?: number;
  llm_output_tokens?: number;
  tool_calls?: number;
  daily_llm_token_quota?: number;
  daily_tool_call_quota?: number;
  token_limit?: number;
  tool_call_limit?: number;
  limits?: {
    llm_tokens?: number;
    tool_calls?: number;
  };
  usage?: {
    llm_input_tokens?: number;
    llm_output_tokens?: number;
    tool_calls?: number;
  };
  [key: string]: unknown;
}

export interface AuthSessionOut {
  token_prefix: string;
  user_agent: string | null;
  created_at: string;
  last_seen_at: string;
  current: boolean;
}

export interface ConversationSummary {
  id: string;
  title: string | null;
  updated_at: string;
  archived: boolean;
}

export interface ToolHit extends NormalizedDecision {
  n: number;
}

export interface ToolStartPayload {
  step: number;
  tool: string;
  source_db: string;
  summary: string;
}

export interface RateWaitPayload {
  source_db: string;
  retry_after: number;
}

export interface ToolResultPayload {
  step: number;
  status: SearchStatus | string;
  count: number;
  top_hits: ToolHit[];
}

export interface TextDeltaPayload {
  delta: string;
}

export interface CitationPayload {
  marker: number;
  decision: NormalizedDecision;
}

export interface SseUsagePayload {
  input_tokens: number;
  output_tokens: number;
}

export interface DonePayload {
  message_id: string;
  stop_reason: string;
  conversation_title?: string;
}

export interface ErrorPayload {
  message: string;
}

export interface SseEventMap {
  text_delta: TextDeltaPayload;
  tool_start: ToolStartPayload;
  rate_wait: RateWaitPayload;
  tool_result: ToolResultPayload;
  citation: CitationPayload;
  usage: SseUsagePayload;
  done: DonePayload;
  error: ErrorPayload;
}

export type SseEventName = keyof SseEventMap;
export type SseEvent = {
  [K in SseEventName]: { event: K; data: SseEventMap[K] };
}[SseEventName];

export interface StoredToolStep {
  step: number;
  tool: string;
  source_db: string;
  summary?: string;
  status?: string;
  count?: number;
  retry_after?: number;
  top_hits?: ToolHit[];
}

export interface StoredMessage {
  id: string;
  role: "user" | "assistant" | string;
  content: string;
  status: string;
  tool_calls: StoredToolStep[] | null;
  citations: Array<CitationPayload | NormalizedDecision> | null;
  created_at: string;
}

export interface ConversationDetail {
  id: string;
  title: string | null;
  updated_at?: string;
  archived?: boolean;
  messages: StoredMessage[];
}

export interface DocumentResponse {
  source_db: string;
  doc_key: string;
  title: string;
  markdown: string;
  page: number;
  total_pages: number | null;
  chunked: boolean;
  cached: boolean;
  fetched_at: string;
  source_url: string | null;
  meta: {
    court?: string | null;
    esas_no?: string | null;
    karar_no?: string | null;
    decision_date?: string | null;
    [key: string]: unknown;
  };
}

export interface BookmarkOut {
  id: number;
  source_db: string;
  doc_key: string;
  title: string;
  meta: Record<string, unknown> | null;
  note: string | null;
  tags: string[];
  created_at: string;
}

export interface BookmarkLookup {
  bookmarked: boolean;
  id?: number;
}

export interface SearchHistoryItem {
  id: string;
  query_text: string;
  source_db: string;
  result_count: number | null;
  status: string;
  created_at: string;
}

export interface DocumentHistoryItem {
  id?: number;
  source_db: string;
  doc_key: string;
  title: string;
  meta?: Record<string, unknown> | null;
  viewed_at?: string;
  created_at?: string;
  last_viewed_at?: string;
  opened_at?: string;
  source_url?: string | null;
  [key: string]: unknown;
}

export interface SearchSnapshot {
  id?: string;
  search_id?: string;
  query_text?: string;
  source_db?: string;
  status?: string;
  result_count?: number;
  created_at?: string;
  results?: NormalizedDecision[];
  rows?: NormalizedDecision[];
  [key: string]: unknown;
}

export interface DocumentTarget {
  source_db: string;
  doc_key: string;
  title: string;
  doc_ref?: DocRef;
  court?: string | null;
  esas_no?: string | null;
  karar_no?: string | null;
  decision_date?: string | null;
  decision_date_raw?: string | null;
  snippet?: string | null;
  source_url?: string | null;
  extra?: Record<string, unknown>;
}
