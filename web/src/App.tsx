import { lazy, Suspense } from "react";
import { Header } from "./components/Header";
import { Sidebar } from "./components/Sidebar";
import { Icon } from "./components/Icon";
import { useAuth } from "./state/auth";
import { AppProvider, useApp } from "./state/app";
import { AuthView } from "./views/AuthView";

const ChatView = lazy(() => import("./views/ChatView"));
const SearchView = lazy(() => import("./views/SearchView"));
const HistoryView = lazy(() => import("./views/HistoryView"));
const BookmarksView = lazy(() => import("./views/BookmarksView"));
const SigningView = lazy(() => import("./views/SigningView"));
const SettingsView = lazy(() => import("./views/SettingsView"));
const DocPanel = lazy(() => import("./components/DocPanel"));

function LoadingScreen({ compact = false }: { compact?: boolean }) {
  return (
    <div className="loading-state" style={compact ? undefined : { minHeight: "100vh" }}>
      <div className="brand-mark brand-mark--soft"><Icon name="scales" size={27} /></div>
      <span className="spinner spinner--large" />
      <span>Yargı Asistan hazırlanıyor…</span>
    </div>
  );
}

function ActiveView() {
  const { view } = useApp();
  switch (view) {
    case "chat": return <ChatView />;
    case "search": return <SearchView />;
    case "history": return <HistoryView />;
    case "bookmarks": return <BookmarksView />;
    case "signing": return <SigningView />;
    case "settings": return <SettingsView />;
  }
}

function Workspace() {
  const { documentTarget } = useApp();
  return (
    <div className="app-shell">
      <Sidebar />
      <main className="app-main">
        <Header />
        <div className="app-content">
          <div className="view-router">
            <Suspense fallback={<LoadingScreen compact />}><ActiveView /></Suspense>
          </div>
          {documentTarget && (
            <Suspense fallback={null}><DocPanel /></Suspense>
          )}
        </div>
      </main>
    </div>
  );
}

export default function App() {
  const { user, loading } = useAuth();
  if (loading) return <LoadingScreen />;
  if (!user) return <AuthView />;
  return <AppProvider><Workspace /></AppProvider>;
}
