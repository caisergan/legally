import React from "react";
import ReactDOM from "react-dom/client";
import App from "./App";
import { AuthProvider } from "./state/auth";
import "./styles/tokens.css";
import "./styles/app.css";

const preLoginTheme = localStorage.getItem("ya_theme");
if (preLoginTheme === "light" || preLoginTheme === "dark") {
  document.documentElement.dataset.theme = preLoginTheme;
} else if (window.matchMedia("(prefers-color-scheme: dark)").matches) {
  document.documentElement.dataset.theme = "dark";
} else {
  document.documentElement.dataset.theme = "light";
}

ReactDOM.createRoot(document.getElementById("root")!).render(
  <React.StrictMode>
    <AuthProvider><App /></AuthProvider>
  </React.StrictMode>,
);
