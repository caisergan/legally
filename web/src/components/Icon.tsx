import type { SVGProps } from "react";

export type IconName =
  | "arrow-left"
  | "arrow-right"
  | "bookmark"
  | "chat"
  | "check"
  | "chevron-right"
  | "clock"
  | "close"
  | "copy"
  | "database"
  | "download"
  | "external"
  | "history"
  | "lock"
  | "menu"
  | "moon"
  | "pen"
  | "plus"
  | "refresh"
  | "scales"
  | "search"
  | "settings"
  | "stop"
  | "sun"
  | "trash";

interface IconProps extends Omit<SVGProps<SVGSVGElement>, "name"> {
  name: IconName;
  size?: number;
  filled?: boolean;
}

export function Icon({ name, size = 18, filled = false, ...props }: IconProps) {
  const common = {
    width: size,
    height: size,
    viewBox: "0 0 24 24",
    fill: filled ? "currentColor" : "none",
    stroke: "currentColor",
    strokeWidth: 1.8,
    strokeLinecap: "round" as const,
    strokeLinejoin: "round" as const,
    "aria-hidden": true,
    ...props,
  };

  switch (name) {
    case "scales":
      return <svg {...common}><path d="M12 3v18M7 21h10M12 6l-7 2 2.5 5a3 3 0 0 1-5 0L12 6zm0 0l7 2-2.5 5a3 3 0 0 0 5 0L12 6z" /></svg>;
    case "plus":
      return <svg {...common}><path d="M12 5v14M5 12h14" /></svg>;
    case "pen":
      return <svg {...common}><path d="M12 20h9M16.5 3.5a2.12 2.12 0 0 1 3 3L7 19l-4 1 1-4z" /></svg>;
    case "chat":
      return <svg {...common}><path d="M21 15a2 2 0 0 1-2 2H7l-4 4V5a2 2 0 0 1 2-2h14a2 2 0 0 1 2 2z" /></svg>;
    case "search":
      return <svg {...common}><circle cx="11" cy="11" r="7" /><path d="m21 21-4.3-4.3" /></svg>;
    case "history":
      return <svg {...common}><path d="M3 3v6h6M3.5 9a9 9 0 1 0 2-4.5L3 9" /><path d="M12 8v4l3 2" /></svg>;
    case "bookmark":
      return <svg {...common}><path d="M19 21l-7-5-7 5V5a2 2 0 0 1 2-2h10a2 2 0 0 1 2 2z" /></svg>;
    case "settings":
      return <svg {...common}><circle cx="12" cy="12" r="3" /><path d="M19.4 15a1.6 1.6 0 0 0 .3 1.8l.1.1a2 2 0 1 1-2.8 2.8l-.1-.1a1.6 1.6 0 0 0-1.8-.3 1.6 1.6 0 0 0-1 1.5V21a2 2 0 1 1-4 0v-.1a1.6 1.6 0 0 0-1-1.5 1.6 1.6 0 0 0-1.8.3l-.1.1a2 2 0 1 1-2.8-2.8l.1-.1a1.6 1.6 0 0 0 .3-1.8 1.6 1.6 0 0 0-1.5-1H3a2 2 0 1 1 0-4h.1a1.6 1.6 0 0 0 1.5-1 1.6 1.6 0 0 0-.3-1.8l-.1-.1a2 2 0 1 1 2.8-2.8l.1.1a1.6 1.6 0 0 0 1.8.3H9a1.6 1.6 0 0 0 1-1.5V3a2 2 0 1 1 4 0v.1a1.6 1.6 0 0 0 1 1.5 1.6 1.6 0 0 0 1.8-.3l.1-.1a2 2 0 1 1 2.8 2.8l-.1.1a1.6 1.6 0 0 0-.3 1.8V9a1.6 1.6 0 0 0 1.5 1H21a2 2 0 1 1 0 4h-.1a1.6 1.6 0 0 0-1.5 1z" /></svg>;
    case "menu":
      return <svg {...common}><path d="M3 6h18M3 12h18M3 18h18" /></svg>;
    case "moon":
      return <svg {...common}><path d="M21 12.8A9 9 0 1 1 11.2 3a7 7 0 0 0 9.8 9.8z" /></svg>;
    case "sun":
      return <svg {...common}><circle cx="12" cy="12" r="4.5" /><path d="M12 2v2M12 20v2M4.9 4.9l1.4 1.4M17.7 17.7l1.4 1.4M2 12h2M20 12h2M4.9 19.1l1.4-1.4M17.7 6.3l1.4-1.4" /></svg>;
    case "arrow-right":
      return <svg {...common}><path d="M5 12h14M13 6l6 6-6 6" /></svg>;
    case "arrow-left":
      return <svg {...common}><path d="M19 12H5m6 6-6-6 6-6" /></svg>;
    case "chevron-right":
      return <svg {...common}><path d="m9 18 6-6-6-6" /></svg>;
    case "close":
      return <svg {...common}><path d="M18 6 6 18M6 6l12 12" /></svg>;
    case "copy":
      return <svg {...common}><rect x="9" y="9" width="12" height="12" rx="2" /><path d="M5 15V5a2 2 0 0 1 2-2h8" /></svg>;
    case "download":
      return <svg {...common}><path d="M12 3v13m0 0 4-4m-4 4-4-4M5 21h14" /></svg>;
    case "external":
      return <svg {...common}><path d="M15 3h6v6M10 14 21 3M18 13v6a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V8a2 2 0 0 1 2-2h6" /></svg>;
    case "refresh":
      return <svg {...common}><path d="M4 4v6h6M20 20v-6h-6" /><path d="M4 10a8 8 0 0 1 14-3M20 14a8 8 0 0 1-14 3" /></svg>;
    case "lock":
      return <svg {...common}><rect x="5" y="11" width="14" height="10" rx="2" /><path d="M8 11V7a4 4 0 0 1 8 0v4" /></svg>;
    case "trash":
      return <svg {...common}><path d="M3 6h18M8 6V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2m2 0v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6" /></svg>;
    case "stop":
      return <svg {...common}><rect x="7" y="7" width="10" height="10" rx="1.5" fill="currentColor" stroke="none" /></svg>;
    case "check":
      return <svg {...common}><path d="m5 12 4 4L19 6" /></svg>;
    case "clock":
      return <svg {...common}><circle cx="12" cy="12" r="9" /><path d="M12 8v4l2.5 2.5" /></svg>;
    case "database":
      return <svg {...common}><ellipse cx="12" cy="5" rx="8" ry="3" /><path d="M4 5v6c0 1.7 3.6 3 8 3s8-1.3 8-3V5M4 11v6c0 1.7 3.6 3 8 3s8-1.3 8-3v-6" /></svg>;
  }
}
