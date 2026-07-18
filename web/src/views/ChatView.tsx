import {
  type KeyboardEvent,
  type Reducer,
  useCallback,
  useEffect,
  useLayoutEffect,
  useReducer,
  useRef,
  useState,
} from "react";
import { Icon } from "../components/Icon";
import { CitationCard } from "../components/chat/CitationCard";
import { CitationChip } from "../components/chat/CitationChip";
import { ToolStepCard, type ToolStep } from "../components/chat/ToolStepCard";
import { apiFetch, apiJson } from "../lib/api";
import { copyText } from "../lib/format";
import { streamSse } from "../lib/sse";
import type {
  CitationPayload,
  ConversationDetail,
  NormalizedDecision,
  SseEvent,
  StoredMessage,
} from "../lib/types";
import { useApp } from "../state/app";

type ChatMessage = UserMessage | AssistantMessage;

interface BaseMessage {
  localId: string;
  id?: string;
  role: "user" | "assistant";
  content: string;
  status: string;
}

interface UserMessage extends BaseMessage {
  role: "user";
}

interface AssistantMessage extends BaseMessage {
  role: "assistant";
  steps: ToolStep[];
  citations: CitationPayload[];
  thinking: boolean;
  streaming: boolean;
  error?: string;
}

interface ChatState {
  messages: ChatMessage[];
  streaming: boolean;
}

type ChatAction =
  | { type: "reset" }
  | { type: "load"; messages: ChatMessage[] }
  | { type: "begin"; content: string }
  | { type: "event"; event: SseEvent }
  | { type: "fail"; message: string }
  | { type: "interrupt" };

const initialState: ChatState = { messages: [], streaming: false };

function localId(prefix: string): string {
  return `${prefix}-${Date.now()}-${Math.random().toString(36).slice(2)}`;
}

function lastMatchingIndex<T>(items: T[], predicate: (item: T) => boolean): number {
  for (let index = items.length - 1; index >= 0; index -= 1) {
    const item = items[index];
    if (item !== undefined && predicate(item)) return index;
  }
  return -1;
}

function updateLastAssistant(state: ChatState, update: (message: AssistantMessage) => AssistantMessage): ChatState {
  const index = lastMatchingIndex(state.messages, (message) => message.role === "assistant");
  if (index < 0) return state;
  const current = state.messages[index];
  if (!current || current.role !== "assistant") return state;
  const messages = state.messages.slice();
  messages[index] = update(current);
  return { ...state, messages };
}

const chatReducer: Reducer<ChatState, ChatAction> = (state, action) => {
  if (action.type === "reset") return initialState;
  if (action.type === "load") return { messages: action.messages, streaming: false };
  if (action.type === "begin") {
    return {
      streaming: true,
      messages: [
        ...state.messages,
        { localId: localId("user"), role: "user", content: action.content, status: "complete" },
        {
          localId: localId("assistant"),
          role: "assistant",
          content: "",
          status: "streaming",
          steps: [],
          citations: [],
          thinking: true,
          streaming: true,
        },
      ],
    };
  }
  if (action.type === "fail") {
    const next = updateLastAssistant(state, (message) => ({
      ...message,
      status: "error",
      streaming: false,
      thinking: false,
      error: action.message,
    }));
    return { ...next, streaming: false };
  }
  if (action.type === "interrupt") {
    const next = updateLastAssistant(state, (message) => ({
      ...message,
      status: "interrupted",
      streaming: false,
      thinking: false,
    }));
    return { ...next, streaming: false };
  }

  const event = action.event;
  if (event.event === "text_delta") {
    return updateLastAssistant(state, (message) => ({
      ...message,
      content: message.content + event.data.delta,
      thinking: false,
    }));
  }
  if (event.event === "tool_start") {
    return updateLastAssistant(state, (message) => ({
      ...message,
      thinking: false,
      steps: [
        ...message.steps,
        {
          step: event.data.step,
          tool: event.data.tool,
          sourceDb: event.data.source_db,
          summary: event.data.summary,
          status: "running",
          hits: [],
        },
      ],
    }));
  }
  if (event.event === "rate_wait") {
    return updateLastAssistant(state, (message) => {
      const steps = message.steps.slice();
      const index = lastMatchingIndex(steps, (step) => step.sourceDb === event.data.source_db && step.status === "running");
      const current = steps[index];
      if (index >= 0 && current) {
        steps[index] = { ...current, status: "waiting", retryAfter: event.data.retry_after };
      }
      return { ...message, steps };
    });
  }
  if (event.event === "tool_result") {
    return updateLastAssistant(state, (message) => {
      const steps = message.steps.map((step) => step.step === event.data.step ? {
        ...step,
        status: event.data.status === "source_error"
          ? "error" as const
          : event.data.status === "rate_limited" ? "rate" as const : "done" as const,
        count: event.data.count,
        hits: event.data.top_hits,
        retryAfter: undefined,
      } : step);
      return { ...message, steps };
    });
  }
  if (event.event === "citation") {
    return updateLastAssistant(state, (message) => ({
      ...message,
      citations: [
        ...message.citations.filter((citation) => citation.marker !== event.data.marker),
        event.data,
      ].sort((left, right) => left.marker - right.marker),
    }));
  }
  if (event.event === "done") {
    const next = updateLastAssistant(state, (message) => ({
      ...message,
      id: event.data.message_id,
      status: event.data.stop_reason === "interrupted" ? "interrupted" : "complete",
      streaming: false,
      thinking: false,
    }));
    return { ...next, streaming: false };
  }
  if (event.event === "error") {
    const next = updateLastAssistant(state, (message) => ({
      ...message,
      status: "error",
      streaming: false,
      thinking: false,
      error: event.data.message,
    }));
    return { ...next, streaming: false };
  }
  return state;
};

function isDecision(value: unknown): value is NormalizedDecision {
  return Boolean(value && typeof value === "object" && "source_db" in value && "doc_key" in value);
}

function storedCitations(message: StoredMessage): CitationPayload[] {
  if (!Array.isArray(message.citations)) return [];
  return message.citations.flatMap((item, index) => {
    if (item && typeof item === "object" && "decision" in item && isDecision(item.decision)) {
      const marker = typeof item.marker === "number" ? item.marker : index + 1;
      return [{ marker, decision: item.decision }];
    }
    if (isDecision(item)) {
      const marker = "marker" in item && typeof item.marker === "number" ? item.marker : index + 1;
      return [{ marker, decision: item }];
    }
    return [];
  });
}

function storedSteps(message: StoredMessage): ToolStep[] {
  if (!Array.isArray(message.tool_calls)) return [];
  return message.tool_calls.map((step, index) => ({
    step: typeof step.step === "number" ? step.step : index + 1,
    tool: step.tool || "araç",
    sourceDb: step.source_db || "bedesten",
    summary: step.summary || `${step.tool || "Araç"} çalıştırıldı`,
    status: step.status === "rate_limited"
      ? "rate"
      : step.status === "source_error" || step.status === "error" || step.status === "interrupted" ? "error" : "done",
    count: step.count,
    retryAfter: step.retry_after,
    hits: Array.isArray(step.top_hits) ? step.top_hits : [],
  }));
}

function fromStoredMessage(message: StoredMessage): ChatMessage {
  if (message.role === "user") {
    return { localId: message.id, id: message.id, role: "user", content: message.content, status: message.status };
  }
  return {
    localId: message.id,
    id: message.id,
    role: "assistant",
    content: message.content,
    status: message.status,
    steps: storedSteps(message),
    citations: storedCitations(message),
    thinking: false,
    streaming: false,
    error: message.status === "error" ? "Bu yanıt tamamlanamadı." : undefined,
  };
}

function AnswerText({ message }: { message: AssistantMessage }) {
  const byMarker = new Map(message.citations.map((citation) => [citation.marker, citation]));
  const paragraphs = message.content ? message.content.split(/\n{2,}/) : [];
  return (
    <div className="assistant-answer">
      {paragraphs.map((paragraph, paragraphIndex) => (
        <p key={`${message.localId}-p-${paragraphIndex}`}>
          {paragraph.split(/(\[\d+\])/g).map((segment, segmentIndex) => {
            const match = /^\[(\d+)\]$/.exec(segment);
            if (!match) return <span key={segmentIndex}>{segment}</span>;
            const marker = Number(match[1]);
            return <CitationChip key={segmentIndex} marker={marker} citation={byMarker.get(marker)} />;
          })}
        </p>
      ))}
      {message.streaming && message.content && <span className="streaming-caret" />}
    </div>
  );
}

const starters = [
  {
    title: "Yargıtay · kişisel veri",
    subtitle: "2024'te kişisel veri ihlali kararları",
    prompt: "2024'te Yargıtay'ın kişisel veri ihlali (KVKK) hakkında verdiği kararları özetler misin?",
  },
  {
    title: "KVKK · para cezaları",
    subtitle: "Veri güvenliği ihlali idari cezaları",
    prompt: "KVKK'nın veri güvenliği ihlali nedeniyle verdiği idari para cezası kararları neler?",
  },
  {
    title: "Rekabet · birleşme",
    subtitle: "Son devralma/birleşme kararları",
    prompt: "Rekabet Kurulu'nun son dönemdeki birleşme ve devralma kararları neler?",
  },
  {
    title: "AYM · ifade özgürlüğü",
    subtitle: "Bireysel başvuru içtihadı",
    prompt: "Anayasa Mahkemesi'nin ifade özgürlüğüne ilişkin bireysel başvuru içtihadı nasıl?",
  },
];

function exportAnswer(message: AssistantMessage) {
  const citations = message.citations.map((citation) => `[${citation.marker}] ${citation.decision.title}`).join("\n");
  const blob = new Blob([`${message.content}\n\nKaynaklar\n${citations}`], { type: "text/plain;charset=utf-8" });
  const url = URL.createObjectURL(blob);
  const anchor = document.createElement("a");
  anchor.href = url;
  anchor.download = "yargi-asistan-yanit.txt";
  document.body.appendChild(anchor);
  anchor.click();
  anchor.remove();
  URL.revokeObjectURL(url);
}

function Thinking() {
  return (
    <div className="thinking">
      <span className="thinking-dots"><span /><span /><span /></span>
      Yanıt hazırlanıyor
    </div>
  );
}

export default function ChatView() {
  const {
    conversationId,
    openConversation,
    refreshConversations,
    chatSeed,
    consumeChatSeed,
  } = useApp();
  const [state, dispatch] = useReducer(chatReducer, initialState);
  const [input, setInput] = useState("");
  const [loading, setLoading] = useState(false);
  const [copiedId, setCopiedId] = useState<string | null>(null);
  const controllerRef = useRef<AbortController | null>(null);
  const streamingRef = useRef(false);
  const scrollRef = useRef<HTMLDivElement | null>(null);
  const textareaRef = useRef<HTMLTextAreaElement | null>(null);
  const seedRef = useRef<string | null>(null);

  useEffect(() => {
    if (!conversationId) {
      if (!streamingRef.current) dispatch({ type: "reset" });
      return;
    }
    if (streamingRef.current) return;
    const controller = new AbortController();
    setLoading(true);
    apiFetch<ConversationDetail>(`/api/conversations/${conversationId}`, { signal: controller.signal })
      .then((conversation) => dispatch({ type: "load", messages: conversation.messages.map(fromStoredMessage) }))
      .catch((error) => {
        if (!(error instanceof DOMException && error.name === "AbortError")) {
          dispatch({ type: "fail", message: error instanceof Error ? error.message : "Sohbet yüklenemedi." });
        }
      })
      .finally(() => setLoading(false));
    return () => controller.abort();
  }, [conversationId]);

  useLayoutEffect(() => {
    const textarea = textareaRef.current;
    if (!textarea) return;
    textarea.style.height = "auto";
    textarea.style.height = `${Math.min(textarea.scrollHeight, 150)}px`;
  }, [input]);

  useEffect(() => {
    const scroll = scrollRef.current;
    if (scroll) scroll.scrollTop = scroll.scrollHeight;
  }, [state.messages]);

  const sendMessage = useCallback(async (rawContent: string) => {
    const content = rawContent.trim();
    if (!content || streamingRef.current) return;
    streamingRef.current = true;
    dispatch({ type: "begin", content });
    setInput("");
    let activeConversationId = conversationId;
    try {
      if (!activeConversationId) {
        const created = await apiJson<{ id: string }>("/api/conversations", "POST", {});
        activeConversationId = created.id;
        openConversation(created.id);
      }
      const controller = new AbortController();
      controllerRef.current = controller;
      await streamSse(
        `/api/conversations/${activeConversationId}/messages`,
        { content },
        {
          signal: controller.signal,
          onEvent: (event) => dispatch({ type: "event", event }),
        },
      );
    } catch (error) {
      if (error instanceof DOMException && error.name === "AbortError") {
        dispatch({ type: "interrupt" });
      } else {
        dispatch({ type: "fail", message: error instanceof Error ? error.message : "Yanıt alınamadı." });
      }
    } finally {
      streamingRef.current = false;
      controllerRef.current = null;
      await refreshConversations().catch(() => undefined);
      window.setTimeout(() => { void refreshConversations().catch(() => undefined); }, 2_500);
    }
  }, [conversationId, openConversation, refreshConversations]);

  useEffect(() => {
    if (!chatSeed || seedRef.current === chatSeed) return;
    seedRef.current = chatSeed;
    const seed = consumeChatSeed();
    if (seed) void sendMessage(seed);
  }, [chatSeed, consumeChatSeed, sendMessage]);

  const stop = () => {
    if (conversationId) {
      void apiJson<void>(`/api/conversations/${conversationId}/stop`, "POST").catch(() => undefined);
    }
    controllerRef.current?.abort();
    dispatch({ type: "interrupt" });
    streamingRef.current = false;
  };

  const onComposerKeyDown = (event: KeyboardEvent<HTMLTextAreaElement>) => {
    if (event.key === "Enter" && !event.shiftKey) {
      event.preventDefault();
      void sendMessage(input);
    }
  };

  const copyAnswer = async (message: AssistantMessage) => {
    try {
      await copyText(message.content);
      setCopiedId(message.localId);
      window.setTimeout(() => setCopiedId(null), 1_600);
    } catch {
      setCopiedId(null);
    }
  };

  return (
    <div className="chat-view">
      <div className="chat-scroll" ref={scrollRef} data-chat-scroll="1">
        <div className="chat-column">
          {loading ? (
            <div className="loading-state"><span className="spinner spinner--large" />Sohbet yükleniyor…</div>
          ) : state.messages.length === 0 ? (
            <div className="chat-empty">
              <div className="brand-mark brand-mark--soft"><Icon name="scales" size={27} /></div>
              <h1 className="chat-empty-title">Ne araştıralım?</h1>
              <p className="chat-empty-copy">Doğal dille sorun; asistan ilgili veritabanlarında arama yapıp kararları kaynak göstererek özetlesin.</p>
              <div className="starter-grid">
                {starters.map((starter) => (
                  <button type="button" className="starter-card" key={starter.title} onClick={() => void sendMessage(starter.prompt)}>
                    <span className="starter-title">{starter.title}</span>
                    <span className="starter-subtitle">{starter.subtitle}</span>
                  </button>
                ))}
              </div>
            </div>
          ) : state.messages.map((message) => (
            <div className="message" key={message.localId}>
              {message.role === "user" ? (
                <div className="user-message"><div className="user-bubble">{message.content}</div></div>
              ) : (
                <div className="assistant-message">
                  <div className="assistant-avatar"><Icon name="scales" size={16} /></div>
                  <div className="assistant-content">
                    {message.steps.map((step) => <ToolStepCard step={step} key={`${message.localId}-${step.step}`} />)}
                    {message.thinking && <Thinking />}
                    {(message.content || message.streaming) && <AnswerText message={message} />}
                    {message.error && <div className="chat-error" role="alert">{message.error}</div>}
                    {message.citations.length > 0 && (
                      <div className="citations">
                        <div className="citations-label">Kaynaklar</div>
                        <div className="citation-list">
                          {message.citations.map((citation) => (
                            <CitationCard citation={citation} key={`${message.localId}-${citation.marker}`} />
                          ))}
                        </div>
                      </div>
                    )}
                    {!message.streaming && message.content && (
                      <div className="message-actions">
                        <button type="button" className="button button--quiet button--compact" onClick={() => void copyAnswer(message)}>
                          <Icon name={copiedId === message.localId ? "check" : "copy"} size={15} />
                          {copiedId === message.localId ? "Kopyalandı" : "Kopyala"}
                        </button>
                        <button type="button" className="button button--quiet button--compact" onClick={() => exportAnswer(message)}>
                          <Icon name="download" size={15} />Dışa aktar
                        </button>
                      </div>
                    )}
                  </div>
                </div>
              )}
            </div>
          ))}
          {state.messages.length > 0 && (
            <div className="disclaimer">Yapay zekâ hukuki tavsiye vermez; kararları asıl kaynağından doğrulayın.</div>
          )}
        </div>
      </div>

      <div className="composer-wrap">
        <div className="composer-inner">
          <div className="composer">
            <textarea
              ref={textareaRef}
              rows={1}
              value={input}
              onChange={(event) => setInput(event.target.value)}
              onKeyDown={onComposerKeyDown}
              disabled={state.streaming}
              placeholder="Hukuki bir soru sorun…  (ör. 2024'te Yargıtay'ın kişisel veri ihlali kararları)"
              aria-label="Hukuki soru"
            />
            {state.streaming ? (
              <button type="button" className="send-button stop" onClick={stop} aria-label="Yanıtı durdur" title="Durdur">
                <Icon name="stop" size={17} />
              </button>
            ) : (
              <button type="button" className="send-button" onClick={() => void sendMessage(input)} disabled={!input.trim()} aria-label="Gönder">
                <Icon name="arrow-right" size={18} />
              </button>
            )}
          </div>
          <div className="composer-hint">Enter ile gönder · Shift+Enter yeni satır</div>
        </div>
      </div>
    </div>
  );
}
