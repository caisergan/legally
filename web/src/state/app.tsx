import {
  createContext,
  type PropsWithChildren,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useState,
} from "react";
import { apiFetch } from "../lib/api";
import type {
  ConversationSummary,
  DocumentTarget,
  SearchParams,
  SearchResponse,
  SourceMeta,
} from "../lib/types";
import { useAuth } from "./auth";

export type AppView = "chat" | "search" | "history" | "bookmarks" | "signing" | "settings";
export type Theme = "light" | "dark";

export interface SearchHandoff {
  sourceDb: string;
  params?: SearchParams;
  response: SearchResponse;
}

interface AppContextValue {
  view: AppView;
  setView: (view: AppView) => void;
  theme: Theme;
  setTheme: (theme: Theme, persist?: boolean) => void;
  isMobile: boolean;
  sidebarOpen: boolean;
  setSidebarOpen: (open: boolean) => void;
  toggleSidebar: () => void;
  sources: SourceMeta[];
  sourcesLoading: boolean;
  refreshSources: () => Promise<void>;
  conversations: ConversationSummary[];
  conversationsLoading: boolean;
  refreshConversations: () => Promise<void>;
  conversationId: string | null;
  openConversation: (id: string) => void;
  beginNewChat: () => void;
  documentTarget: DocumentTarget | null;
  openDocument: (target: DocumentTarget) => void;
  closeDocument: () => void;
  chatSeed: string | null;
  sendToChat: (prompt: string) => void;
  consumeChatSeed: () => string | null;
  searchHandoff: SearchHandoff | null;
  setSearchHandoff: (handoff: SearchHandoff | null) => void;
}

const AppContext = createContext<AppContextValue | null>(null);

function storedTheme(): Theme {
  const value = localStorage.getItem("ya_theme");
  if (value === "light" || value === "dark") return value;
  return window.matchMedia("(prefers-color-scheme: dark)").matches ? "dark" : "light";
}

export function AppProvider({ children }: PropsWithChildren) {
  const { user, updateProfile } = useAuth();
  const [viewState, setViewState] = useState<AppView>("chat");
  const [themeState, setThemeState] = useState<Theme>(
    user?.theme === "dark" || user?.theme === "light" ? user.theme : storedTheme(),
  );
  const [isMobile, setIsMobile] = useState(() => window.matchMedia("(max-width: 900px)").matches);
  const [sidebarOpen, setSidebarOpen] = useState(() => !window.matchMedia("(max-width: 900px)").matches);
  const [sources, setSources] = useState<SourceMeta[]>([]);
  const [sourcesLoading, setSourcesLoading] = useState(true);
  const [conversations, setConversations] = useState<ConversationSummary[]>([]);
  const [conversationsLoading, setConversationsLoading] = useState(true);
  const [conversationId, setConversationId] = useState<string | null>(null);
  const [documentTarget, setDocumentTarget] = useState<DocumentTarget | null>(null);
  const [chatSeed, setChatSeed] = useState<string | null>(null);
  const [searchHandoff, setSearchHandoff] = useState<SearchHandoff | null>(null);

  useEffect(() => {
    const media = window.matchMedia("(max-width: 900px)");
    const apply = () => {
      setIsMobile(media.matches);
      setSidebarOpen(!media.matches);
    };
    apply();
    media.addEventListener("change", apply);
    return () => media.removeEventListener("change", apply);
  }, []);

  useEffect(() => {
    document.documentElement.dataset.theme = themeState;
    localStorage.setItem("ya_theme", themeState);
    const themeColor = document.querySelector<HTMLMetaElement>('meta[name="theme-color"]');
    if (themeColor) themeColor.content = themeState === "dark" ? "#191612" : "#F4EFE6";
  }, [themeState]);

  const refreshSources = useCallback(async () => {
    setSourcesLoading(true);
    try {
      const response = await apiFetch<SourceMeta[] | { sources: SourceMeta[] }>("/api/meta/sources");
      setSources(Array.isArray(response) ? response : response.sources);
    } finally {
      setSourcesLoading(false);
    }
  }, []);

  const refreshConversations = useCallback(async () => {
    setConversationsLoading(true);
    try {
      const response = await apiFetch<ConversationSummary[] | { conversations: ConversationSummary[] }>("/api/conversations");
      const rows = Array.isArray(response) ? response : response.conversations;
      setConversations(rows.filter((conversation) => !conversation.archived));
    } finally {
      setConversationsLoading(false);
    }
  }, []);

  useEffect(() => {
    refreshSources().catch(() => setSources([]));
    refreshConversations().catch(() => setConversations([]));
  }, [refreshConversations, refreshSources]);

  const setView = useCallback((next: AppView) => {
    setViewState(next);
    if (isMobile) setSidebarOpen(false);
  }, [isMobile]);

  const setTheme = useCallback((next: Theme, persist = true) => {
    setThemeState(next);
    if (persist && user) updateProfile({ theme: next }).catch(() => undefined);
  }, [updateProfile, user]);

  const toggleSidebar = useCallback(() => setSidebarOpen((open) => !open), []);

  const openConversation = useCallback((id: string) => {
    setConversationId(id);
    setChatSeed(null);
    setView("chat");
  }, [setView]);

  const beginNewChat = useCallback(() => {
    setConversationId(null);
    setChatSeed(null);
    setView("chat");
  }, [setView]);

  const openDocument = useCallback((target: DocumentTarget) => setDocumentTarget(target), []);
  const closeDocument = useCallback(() => setDocumentTarget(null), []);

  const sendToChat = useCallback((prompt: string) => {
    setConversationId(null);
    setChatSeed(prompt);
    setView("chat");
  }, [setView]);

  const consumeChatSeed = useCallback(() => {
    const value = chatSeed;
    setChatSeed(null);
    return value;
  }, [chatSeed]);

  const value = useMemo<AppContextValue>(() => ({
    view: viewState,
    setView,
    theme: themeState,
    setTheme,
    isMobile,
    sidebarOpen,
    setSidebarOpen,
    toggleSidebar,
    sources,
    sourcesLoading,
    refreshSources,
    conversations,
    conversationsLoading,
    refreshConversations,
    conversationId,
    openConversation,
    beginNewChat,
    documentTarget,
    openDocument,
    closeDocument,
    chatSeed,
    sendToChat,
    consumeChatSeed,
    searchHandoff,
    setSearchHandoff,
  }), [
    beginNewChat,
    chatSeed,
    closeDocument,
    consumeChatSeed,
    conversationId,
    conversations,
    conversationsLoading,
    documentTarget,
    isMobile,
    openConversation,
    openDocument,
    refreshConversations,
    refreshSources,
    searchHandoff,
    sendToChat,
    setTheme,
    setView,
    sidebarOpen,
    sources,
    sourcesLoading,
    themeState,
    toggleSidebar,
    viewState,
  ]);

  return <AppContext.Provider value={value}>{children}</AppContext.Provider>;
}

export function useApp(): AppContextValue {
  const value = useContext(AppContext);
  if (!value) throw new Error("useApp, AppProvider içinde kullanılmalıdır.");
  return value;
}
