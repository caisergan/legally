# Yargı Asistan — Start-to-End Technical Implementation Plan

**Project root:** `~/Desktop/personal-projects/legally`
**Goal:** A unified legal research web platform for Turkish lawyers. Users log in, ask legal
research questions in a chat backed by an LLM agent that calls the **yargi-mcp** MCP server's
tools, run structured searches directly against 12 Turkish legal databases, read decisions in a
document viewer, and keep all history/bookmarks in **SQLite**.

This plan is self-contained: an implementor following it end-to-end, together with the two
ground-truth reference files below, can reproduce the exact system.

---

## 0. Ground-truth references (read these first)

| File | What it is |
|---|---|
| `design-reference/Yargi Asistan.dc.html` | **The visual + interaction spec.** A fully working single-file design prototype of the entire app (all 6 surfaces, light/dark themes, demo state machine). Every color, spacing, radius, font size, and TR copy string in the UI must match this file. |
| `../yargi-mcp/` | The MCP server this app drives. Python ≥3.11, FastMCP. Entry: `mcp_server_main.py` (`app = FastMCP(...)`, `create_app()` factory). |
| §2 of this document | Verified inventory of all active MCP tools with exact names/params/response shapes (extracted from the codebase 2026-07-18). |

**Critical facts about yargi-mcp (verified against source, NOT its stale CLAUDE.md/README):**
- 28 tools always active + 1 conditional (`search_bedesten_semantic` registers only when
  `EMBEDDING_PROVIDER=local` or `OPENROUTER_API_KEY` is set at startup).
- **No authentication whatsoever** in the current code — Clerk/OAuth/Redis sections of its docs
  are dead. Our backend is the auth layer; never expose the MCP endpoint publicly.
- Transports: stdio, or Streamable HTTP at `/mcp/` (trailing slash). No SSE endpoint exists.
- Best integration: **in-process** — `fastmcp.Client(mcp_server_main.app)` (in-memory transport).
  Do NOT call `tool.fn(**args)` directly (some tools take an injected `ctx: Context`).
- Tool results arrive as JSON (Pydantic `.model_dump()`); via `fastmcp.Client` use
  `result.data` if present else `json.loads(result.content[0].text)`.
- Bedesten/Emsal clients enforce a token bucket (~1 req / 3.5 s, per process) and return
  in-band `{error: "rate_limit_exceeded", status_code: 429, retry_after: N}` payloads inside a
  successful call. Other modules return `error_message`/`error_code` fields or raise. One
  classifier must fold all four conventions (see §5.4).
- Key-gated sources: KVKK falls back to a rate-limited free Brave token if `BRAVE_API_TOKEN`
  unset; BDDK + Sigorta Tahkim **require** `TAVILY_API_KEY` (report as gated when absent).
- Env vars actually read by yargi-mcp: `ALLOWED_ORIGINS`, `TAVILY_API_KEY`, `BRAVE_API_TOKEN`,
  `BEDESTEN_RATE_*`, `EMSAL_RATE_*`, `EMBEDDING_PROVIDER`, `OPENROUTER_API_KEY`,
  `OPENROUTER_EMBEDDING_MODEL`, `LOCAL_EMBEDDING_*`, `EMBEDDING_PROMPT_STYLE`. Nothing else.

---

## 1. Architecture

```
┌──────────────────────────────────────────────────────────────┐
│  web/  — React SPA (Vite + TypeScript, no CSS framework —    │
│  design tokens as CSS custom properties, per design file)    │
│  Chat · Structured search · Document panel · History ·       │
│  Bookmarks · Settings · Login/Register                       │
└──────────────▲───────────────────────────────────────────────┘
               │ same-origin REST + SSE (fetch-streamed)
┌──────────────┴───────────────────────────────────────────────┐
│  server/ — FastAPI app (Python ≥3.11, uv-managed)            │
│  • Session auth (argon2id + HttpOnly cookie, sha256-at-rest) │
│  • Chat orchestrator (Anthropic tool-use loop → SSE)         │
│  • Search proxy + normalization adapters (12 databases)      │
│  • Document cache-through · history · bookmarks · quotas     │
│  • SQLite (SQLAlchemy 2 async + aiosqlite, WAL)              │
└──────────────▲───────────────────────────────────────────────┘
               │ in-process fastmcp.Client (embedded mode)
┌──────────────┴───────────────────────────────────────────────┐
│  ../yargi-mcp — FastMCP server, 28(+1) tools, 12 databases   │
└──────────────────────────────────────────────────────────────┘
```

Decisions (deliberate, don't revisit):
- **Single deployable unit**: FastAPI serves `web/dist` statically in production; Vite dev
  server proxies `/api` in development. No CORS in prod.
- **Single uvicorn worker**: SQLite writes serialize anyway; the Bedesten/Emsal token buckets
  are per-process (a second worker would double pressure on government APIs).
- **Embedded MCP bridge by default** (`MCP_MODE=embedded`), with an `http` fallback mode
  pointing at `YARGI_MCP_URL` for when the server runs separately.
- **SSE via streamed POST response** (fetch + ReadableStream on the client). v1 does not
  implement detached generation/reconnect; the assistant message is persisted server-side
  when the stream ends (including on client disconnect — generation runs to completion in a
  background task; see §7.3).
- No Alembic in v1: `Base.metadata.create_all` on startup. Schema changes during v1 are
  applied by deleting the dev DB.

### Repository layout (target state)

```
legally/
├── docs/IMPLEMENTATION_PLAN.md        # this file
├── design-reference/Yargi Asistan.dc.html
├── server/
│   ├── pyproject.toml                 # uv project; yargi-mcp as path dep ../../yargi-mcp
│   ├── .env                           # ANTHROPIC_API_KEY etc. (gitignored)
│   ├── data/yargi_asistan.db          # SQLite (gitignored)
│   └── app/
│       ├── __init__.py
│       ├── main.py                    # app factory, lifespan, static mount
│       ├── config.py                  # ✅ created — pydantic-settings, all env vars
│       ├── db.py                      # ✅ created — async engine, PRAGMAs, get_db, init_db
│       ├── models.py                  # ✅ created — full schema (§4)
│       ├── security.py                # ✅ created — argon2, tokens, get_current_user
│       ├── mcp_bridge.py              # ✅ created — McpBridge, classify_result
│       ├── adapters.py                # NormalizedDecision/DocRef + 12 adapters (§5)
│       ├── quotas.py                  # per-user daily accounting/enforcement
│       ├── chat.py                    # Anthropic tool loop + SSE event generator (§7)
│       └── api/
│           ├── __init__.py            # api_router aggregating the routers below
│           ├── auth.py                # §6.1
│           ├── meta.py                # §6.2
│           ├── search.py              # §6.3
│           ├── documents.py           # §6.4
│           ├── conversations.py       # §6.5 (includes chat SSE endpoint)
│           ├── history.py             # §6.6
│           ├── bookmarks.py           # §6.7
│           └── account.py             # §6.8
└── web/
    ├── package.json                   # Vite + React 18 + TypeScript
    ├── vite.config.ts                 # dev proxy /api → 127.0.0.1:8600
    ├── index.html                     # IBM Plex Google-Fonts links, tr lang
    └── src/
        ├── main.tsx
        ├── App.tsx                    # auth gate + shell
        ├── styles/tokens.css          # §3 tokens verbatim
        ├── styles/app.css             # shared component classes
        ├── lib/api.ts                 # typed fetch client
        ├── lib/sse.ts                 # POST-stream SSE reader
        ├── lib/types.ts               # mirrors backend Pydantic response models
        ├── state/auth.tsx             # user context (me/login/logout)
        ├── state/app.tsx              # view router, theme, doc panel state
        ├── components/Sidebar.tsx
        ├── components/Header.tsx
        ├── components/DocPanel.tsx
        ├── views/AuthView.tsx         # login/register
        ├── views/ChatView.tsx
        ├── views/SearchView.tsx
        ├── views/HistoryView.tsx
        ├── views/BookmarksView.tsx
        └── views/SettingsView.tsx
```

`✅ created` = already written; implementor should read them and keep their contracts.

---

## 2. MCP tool inventory (ground truth — use these exact names/params)

Hidden from both chat and UI: `search`, `fetch` (ChatGPT deep-research duplicates),
`check_government_servers_health` (used only by `/api/meta/health`).

| # | Tool | Key params (required bold) | Returns (essentials) |
|---|---|---|---|
| 1 | `search_bedesten_unified` | **`phrase`** (supports `"exact"`, `+must`, `-not`, AND/OR/NOT; no wildcards), `court_types: ["YARGITAYKARARI","DANISTAYKARAR","YERELHUKUK","ISTINAFHUKUK","KYB"]` (default first two), `pageNumber` (1-based; page size fixed 10), `birimAdi` (enum: `ALL`, `H1`–`H23`, `C1`–`C23`, `HGK`,`CGK`,`BGK`,`HBK`,`CBK`, `D1`–`D17`, `DBGK`,`IDDK`,`VDDK`,`IBK`,`IIK`,`DBK`), `kararTarihiStart`/`kararTarihiEnd` (`YYYY-MM-DD` auto-ISO) | `{decisions:[{documentId, itemType, birimAdi, esasNo, kararNo, kararTarihi,…}], total_records, requested_page, page_size}` — or in-band 429 payload |
| 2 | `get_bedesten_document_markdown` | **`documentId`** | `{documentId, markdown_content, source_url, mime_type}` full text, NOT chunked |
| 3 | `search_emsal_detailed_decisions` | `keyword`, `start_date`/`end_date` (`DD.MM.YYYY`), esas/karar year+seq filters, `page_number` (size fixed 10) | `{decisions:[{id, daire, esasNo, kararNo, kararTarihi, arananKelime, document_url}], total_records}` |
| 4 | `get_emsal_document_markdown` | **`id`** | `{id, markdown_content, source_url}` full text |
| 5 | `search_uyusmazlik_decisions` | **`icerik`**, `search_scope: All\|EsasNo\|KararNo`, `page_number` | `{decisions:[{esas_sayisi, karar_sayisi, karar_tarihi, document_url}], total_records_found}` (no titles — synthesize) |
| 6 | `get_uyusmazlik_document_markdown_from_url` | **`document_url`** | `{source_url, markdown_content}` full text |
| 7 | `search_anayasa_unified` | **`decision_type: norm_denetimi\|bireysel_basvuru`**, `keywords: List[str]`, `page_to_fetch` (1-100), `results_per_page` (default 10) | **JSON string** — parse! `{decision_type, decisions:[dict per type], total_records_found, retrieved_page_number}` |
| 8 | `get_anayasa_document_unified` | **`document_url`**, `page_number` | JSON string: `{decision_type, source_url, document_data, markdown_chunk, current_page, total_pages, is_paginated}` — **chunked 5 000 chars** |
| 9 | `search_kik_v2_decisions` | `decision_type: uyusmazlik\|duzenleyici\|mahkeme` (default uyusmazlik), `karar_metni`, `karar_no`, `basvuran`, `idare_adi`, `baslangic_tarihi`/`bitis_tarihi` (`YYYY-MM-DD`) | `{decisions:[{kararNo, kararTarihi, basvuran, idareAdi, basvuruKonusu, gundemMaddesiId, decision_type}], total_records, page, error_code?, error_message?}` |
| 10 | `get_kik_v2_document_markdown` | **`gundemMaddesiId`** | `{document_id, kararNo, markdown_content, source_url, error_message?}` full text |
| 11 | `search_rekabet_kurumu_decisions` | `PdfText` (supports `"exact"`), `KararTuru: ALL\|Birleşme ve Devralma\|Diğer\|Menfi Tespit ve Muafiyet\|Özelleştirme\|Rekabet İhlali`, `page` | `{decisions:[{publication_date, decision_number, decision_date, title, decision_url, karar_id,…}], total_records_found, retrieved_page_number, total_pages}` — treat `karar_id == "UNKNOWN_KARAR_ID"` as an unopenable row |
| 12 | `get_rekabet_kurumu_document` | **`karar_id`** (GUID), `page_number` | `{pdf_url, title_on_landing_page, markdown_chunk, current_page, total_pages, is_paginated, error_message?}` — **chunked** |
| 13 | `search_sayistay_unified` | **`decision_type: genel_kurul\|temyiz_kurulu\|daire`**; shared: `start`/`length` (**0-based** DataTables), `karar_tarih_baslangic`/`_bitis` (`DD.MM.YYYY`), `kamu_idaresi_turu` (8-enum+ALL), `ilam_no`, `web_karar_konusu` (7-enum+ALL); genel_kurul: `karar_no`,`karar_ek`,`karar_tamami`; temyiz: `ilam_dairesi` (ALL,1–8), `yili`,`dosya_no`,`temyiz_tutanak_no`,`temyiz_karar`; daire: `yargilama_dairesi` (ALL,1–8), `hesap_yili`,`web_karar_metni` | `{decision_type, decisions:[dict per type], total_records, total_filtered}` |
| 14 | `get_sayistay_document_unified` | **`decision_id`**, **`decision_type`** | `{markdown_content, source_url, error_message?}` full text |
| 15 | `search_kvkk_decisions` | **`keywords`** (`+must -not "exact"`), `page` (1-50, size fixed 10) | `{decisions:[{title, url, description, decision_id, publication_date, decision_number}], total_results}` — **empty may mask a network error** (silent-empty), caveat in UI |
| 16 | `get_kvkk_document_markdown` | **`decision_url`** (must start `https://www.kvkk.gov.tr/`), `page_number` | `{source_url, title, decision_date, decision_number, markdown_chunk, current_page, total_pages, is_paginated}` — **chunked** |
| 17 | `search_bddk_decisions` | **`keywords`**, `page` (size fixed 10) — needs `TAVILY_API_KEY` | `{decisions:[{title, document_id, content}], total_results}` |
| 18 | `get_bddk_document_markdown` | **`document_id`**, `page_number` | `{document_id, markdown_content, page_number, total_pages, error?}` — **chunked** |
| 19 | `search_btk_decisions` | `keywords`, `decision_no`, `decision_date`/`publication_date` (`YYYY-MM-DD`), `page`, `pageSize` (1-50) | `{decisions:[{id, title, decision_no, decision_date, pdf_url,…}], total_results, total_pages}` |
| 20 | `get_btk_document_markdown` | **`pdf_url`** (from search result — NOT id), `page_number` | `{source_url, markdown_chunk, current_page, total_pages, is_paginated}` — **chunked** |
| 21 | `search_gib_ozelge` | `keywords`, `ozelgeNo`, `kanunNo`, `ozelgeStartDate`/`EndDate` (`YYYY-MM-DD`), `page`, `pageSize` (1-50) | `{ozelgeler:[{id, ozelgeNo, ozelgeTarih, title, kanunNo, kanunTitle, siteLink}], total_results, total_pages, current_page}` — note key `ozelgeler`, not `decisions` |
| 22 | `get_gib_ozelge_document_markdown` | **`ozelge_id: int`**, `page_number` | `{title, markdown_chunk, current_page, total_pages, is_paginated}` — **chunked** |
| 23 | `search_sigorta_tahkim_decisions` | **`keywords`**, `page` — needs `TAVILY_API_KEY` | `{decisions:[{title, document_id, content, url}], total_results}` |
| 24 | `get_sigorta_tahkim_document_markdown` | **`issue_number`** (journal 1–64), `page_number` | `{markdown_content, page_number, total_pages, source_url}` — **chunked** |
| 25 | `search_within_sigorta_tahkim_issue` | **`issue_number`**, **`keyword`**, `max_results` (1-25) | `{matches:[{decision_header, relevance_score, excerpt}], total_decisions, matching_decisions}` |
| 26 | `search_bedesten_semantic` *(conditional)* | **`initial_keyword`**, **`query`** (NL sentence), `court_types`, `top_k` (1-50) | `{results:[{document_id, title, similarity_score, preview, source_url}], stats}` — expensive (~100 doc fetches); 120 s timeout |
| 27 | `check_government_servers_health` | — | `{overall_status, servers:{yargitay, bedesten}}` — only covers Yargıtay portal + Bedesten; label honestly in UI |

---

## 3. Design system (extract of the .dc.html — implement verbatim)

### 3.1 Fonts
Google Fonts: `IBM Plex Sans` (400/500/600/700 + italic 400), `IBM Plex Serif`
(400/500/600 + italic 400), `IBM Plex Mono` (400/500/600).
`--sans` body/UI · `--serif` headings + document body text · `--mono` references/dates/badges.

### 3.2 Tokens (CSS custom properties on `:root` / `[data-theme="dark"]`)

```css
:root{
  --sans:'IBM Plex Sans',ui-sans-serif,system-ui,-apple-system,sans-serif;
  --serif:'IBM Plex Serif',ui-serif,Georgia,serif;
  --mono:'IBM Plex Mono',ui-monospace,SFMono-Regular,monospace;
  --canvas:#F4EFE6; --surface:#FCFAF5; --surface-2:#EFE9DC; --surface-3:#E7DFCE;
  --border:#E1D9C8; --border-strong:#CFC4AE;
  --ink:#211C15; --ink-2:#5B5344; --ink-3:#8B8271;
  --primary:#8A2A33; --primary-strong:#701F27; --primary-ink:#FCFAF5;
  --primary-soft:#F2E3E0; --primary-border:#E3C8C5;
  --gold:#9A6D22; --gold-soft:#F0E5CD;
  --green:#3F7A55; --green-soft:#E2EEE3;
  --amber:#9C6A16; --amber-soft:#F5E8C9;
  --danger:#B23A34; --danger-soft:#F5DFDB;
  --shadow-sm:0 1px 2px rgba(45,32,12,.06),0 1px 1px rgba(45,32,12,.04);
  --shadow-md:0 8px 26px -10px rgba(45,32,12,.20);
  --shadow-lg:0 24px 64px -22px rgba(45,32,12,.30);
}
[data-theme="dark"]{
  --canvas:#191612; --surface:#211D17; --surface-2:#282219; --surface-3:#302A1F;
  --border:#39321F; --border-strong:#4B4230;
  --ink:#EEE7D8; --ink-2:#B4AB98; --ink-3:#847B69;
  --primary:#D07A7F; --primary-strong:#B85A60; --primary-ink:#1B1712;
  --primary-soft:#39241F; --primary-border:#5C3A37;
  --gold:#D3A755; --gold-soft:#342B1A;
  --green:#7DB78D; --green-soft:#22302A;
  --amber:#D2A150; --amber-soft:#342B19;
  --danger:#E08A84; --danger-soft:#38221E;
  --shadow-sm:0 1px 2px rgba(0,0,0,.30);
  --shadow-md:0 10px 30px -12px rgba(0,0,0,.55);
  --shadow-lg:0 28px 70px -24px rgba(0,0,0,.65);
}
```

Keyframes: `om-caret` (blink), `om-rise` (message entrance, .35s translateY(8px)),
`om-pulse` (health dot), `om-spin` (tool spinner), `om-dot` (thinking dots).
Scrollbars: 11px, thumb `--border-strong`, radius 20, `background-clip:content-box`.

### 3.3 Database identity colors (used everywhere: dots, cards, doc panel)

| id | name | sub | color | gated |
|---|---|---|---|---|
| bedesten | Bedesten | Yargıtay · Danıştay | `#8A2A33` | no |
| anayasa | Anayasa Mahkemesi | AYM | `#3B4B8C` | no |
| emsal | Emsal | UYAP | `#2E6E6A` | no |
| kik | Kamu İhale Kurulu | KİK | `#8A5A1E` | no |
| rekabet | Rekabet Kurumu | RK | `#3F7A55` | no |
| sayistay | Sayıştay | — | `#556072` | no |
| kvkk | KVKK | — | `#6B3FA0` | no |
| bddk | BDDK | — | `#2A5B8A` | when no TAVILY_API_KEY |
| btk | BTK | — | `#1E7A8A` | no |
| gib | Gelir İdaresi | Özelge | `#8A6A2A` | no |
| uyusmazlik | Uyuşmazlık Mahkemesi | — | `#A03D5E` | no |
| sigorta | Sigorta Tahkim | — | `#4A6E2E` | when no TAVILY_API_KEY |

Card descriptions (TR, verbatim from design): Bedesten "Yargıtay, Danıştay, yerel ve istinaf
mahkemeleri ile Kanun Yararına Bozma kararları."; Anayasa "Norm denetimi ve bireysel başvuru
kararları."; Emsal "İlk derece ve bölge adliye mahkemesi emsal kararları."; KİK "İhale
uyuşmazlığı, düzenleyici ve mahkeme kararları."; Rekabet "Birleşme/devralma, ihlal ve muafiyet
kararları."; Sayıştay "Genel kurul, temyiz ve daire kararları."; KVKK "Kişisel verilerin
korunması kurul kararları."; BDDK "Bankacılık düzenleme ve denetleme kararları."; BTK "Bilgi
teknolojileri ve iletişim kurumu kararları."; GİB "Vergi özelgeleri ve muktezalar.";
Uyuşmazlık "Görev ve hüküm uyuşmazlığı kararları."; Sigorta "Sigorta tahkim komisyonu hakem
kararları."

### 3.4 Layout metrics (from the design file)
- Sidebar: 264px fixed, `--surface`, right border; mobile (≤900px): fixed overlay,
  translateX slide, backdrop `rgba(20,14,4,.42)`.
- Header: 56px, `--surface`, bottom border. Contains hamburger (mobile only), view title
  (14px/600), health chip (pill, dot + text; text hidden on mobile), theme toggle (36px sq).
- Chat column max-width 760px; search 980px; history 900px; bookmarks 820px; settings 720px.
- Doc panel: right aside `clamp(380px,40vw,520px)`, full-screen overlay on mobile.
- Radii: buttons 9–11px, cards 12–14px, composer 16px, chips/pills 20px.
- Brand icon: scales-of-justice SVG path
  `M12 3v18M7 21h10M12 6l-7 2 2.5 5a3 3 0 0 1-5 0L12 6zm0 0l7 2-2.5 5a3 3 0 0 0 5 0L12 6z`
  on 34px rounded-9px `--primary` square; app name serif 16.5px/600 "Yargı Asistan",
  tagline 10.5px `--ink-3` "Hukuki araştırma çalışma alanı".

### 3.5 Screen specs
Implement each screen 1:1 from the design file (read the relevant `sc-if` block before
building; all copy is Turkish):

1. **Sidebar**: "Yeni sohbet" primary button; nav Sohbet/Arama/Geçmiş/Kayıtlı (active =
   `--surface-3` bg); "SOHBETLER" section label; conversation list grouped by day
   (Bugün/Dün/Bu hafta/Daha eski — active item `--primary-soft` bg + 600 weight); footer
   profile row (initials avatar, name, plan label, gear → Ayarlar).
2. **Chat empty state**: centered 54px scales icon tile (`--primary-soft` bg,
   `--primary-border`), serif h1 26px "Ne araştıralım?", sub 14px, 2×2 starter grid
   (four canned prompts, title 13px/600 + sub 11.5px `--ink-3`).
3. **Chat messages**: user bubble right-aligned max 82%, `--surface`, radius
   `16px 16px 4px 16px`; assistant row: 30px brand tile + content column.
   **Tool step cards**: border card, 8px db-color dot, db label 12px/600, tool name in mono
   chip (`--surface-2`), right status: spinner (running) / amber countdown pill "{n} sn"
   (rate-wait) / green pill "{n} sonuç" (done); below: 12px description line, then up to 3
   top-hit mini-rows (`--surface-2`, court + mono ref + date, click → doc panel).
   **Thinking**: three pulsing dots + "Yanıt hazırlanıyor". **Answer**: 14.5px/1.62 paragraphs;
   inline citation buttons `[n]` (mono 10.5px, `--primary-soft` bg, radius 5) → open doc.
   **Streaming caret**: 8×16px `--primary` blinking block.
   **Kaynaklar**: uppercase 10.5px label + citation cards (marker square 22px, db dot +
   court 13px/600, mono ref · date, optional snippet 12.5px, chevron) → doc panel.
   Finalized actions: ghost buttons "Kopyala", "Dışa aktar".
   Disclaimer under thread: "Yapay zekâ hukuki tavsiye vermez; kararları asıl kaynağından
   doğrulayın." (11px, centered).
4. **Composer**: sticky bottom, gradient fade into canvas; `--surface` card,
   `--border-strong`, radius 16, shadow-md; auto-growing textarea (max-height 150px),
   placeholder "Hukuki bir soru sorun…  (ör. 2024'te Yargıtay'ın kişisel veri ihlali
   kararları)"; 38px send button (`--primary`, arrow icon); hint line "Enter ile gönder ·
   Shift+Enter yeni satır". While streaming, send becomes a stop button (POST /stop).
5. **Search picker**: serif h1 "Yapılandırılmış arama" + sub; responsive card grid
   `minmax(280px,1fr)`; each card: 10px color square, name 14px/600, mono sub-chip, desc
   12.5px; gated cards at 60% opacity, lock icon + "Sunucuda yapılandırılmamış".
6. **Search form** (db selected): back link "Tüm veritabanları"; header with 13px color
   square + serif name + desc. **Bedesten form**: court-type toggle chips (Yargıtay,
   Danıştay, Yerel Mahkeme, İstinaf (BAM), Kanun Yararına Bozma — active: `--primary-soft`
   bg/`--primary` border+text), chamber search input ("Daire / Kurul · 81 birim"), phrase
   input + operator chips (`"tam ifade"` `+gerekli` `-hariç` `VE` `VEYA` `DEĞİL` + note
   "joker/regex desteklenmez"), date range (GG.AA.YYYY mono inputs). **Other dbs**: generic
   form (badge "Form, aracın canlı JSON şemasından üretildi", phrase + date range + any
   db-specific selects per §5.3 — e.g. Anayasa norm/bireysel toggle, KİK/Sayıştay type
   segments, Rekabet KararTuru dropdown, Sigorta issue flow). Primary "Ara" button + ghost
   "Temizle".
7. **Search results**: count label "1–{n} / {total} sonuç"; "Sohbette devam et" soft-primary
   button (forks results into a new chat); bordered list — rows: db dot, court 13.5px/600,
   konu/snippet 12px `--ink-3`, mono ref right-aligned, mono date (82px col), chevron;
   footer: "Metadata-only sonuçlar · özet için karara girin" + "Sonraki sayfa" button.
8. **History**: serif h1 "Geçmiş" + sub "Yerel olarak saklanan aramalar, sohbetler ve
   görüntülenen kararlar…"; segmented tabs Aramalar/Sohbetler/Görüntülenen
   (`--surface-2` track, active `--surface` + shadow); right search input "Geçmişte ara…".
   Searches tab: card rows «query» + db name, "{time} · anlık görüntü {snap}", status pill
   (ok=green "{n} sonuç" / empty=neutral "sonuç yok" / rate=amber "hız sınırı"), buttons
   "Yeniden" + "Anlık görüntü". Chats tab: icon tile + title + relative time + 1-line
   preview. Docs tab: bordered list like search results + relative time.
9. **Bookmarks**: serif h1 "Kayıtlı kararlar"; tag filter pills; cards: db dot + court +
   mono ref·date + filled bookmark icon; note in left-bordered blockquote 13px; mono
   `#tag` chips.
10. **Settings**: stacked cards, each with uppercase 12px section label — Profil (avatar,
    name, email, Düzenle), Aktif oturumlar (dot green=this device "Bu cihaz" pill, others
    "Oturumu kapat"), Dil (Türkçe/English segment) + Tema (Açık/Koyu segment) side-by-side,
    Model ve günlük kullanım (radio cards: Claude Sonnet 5 "Dengeli · varsayılan" default /
    Claude Opus "En yetenekli · daha yavaş" / Claude Haiku "En hızlı · kısa görevler";
    usage meters: "Dil modeli jetonu" x/300.000, "Araç çağrısı" x/300 — 7px bars, color
    primary→amber>50%→danger>80%), Veri denetimi (KVKK copy; "Tüm verilerimi indir",
    "Geçmişi sil", danger "Hesabı sil").
11. **Doc panel**: 56px header (db dot, court name, close X); body: gold cache badge
    "önbellekten · {dd.mm.yyyy}" when served from cache; serif h2 title 21px; mono meta row
    Esas/Karar/Tarih separated, bottom border; action row: Kaydet (toggles to Kaydedildi,
    filled icon, primary-soft) / Atıf kopyala / Kaynak (external link); document body serif
    14.5px/1.72, `###`-style section heads rendered as uppercase sans 12px `--ink-3`;
    chunked docs: footer pager "Önceki sayfa | Sayfa {n} / {m} | Sonraki sayfa" (next =
    primary).
12. **Auth screens** (not in the design file — extend its language): centered card on
    `--canvas`, brand mark + serif title, email + password inputs (same input style as
    search forms), primary submit, link to switch login↔register. Errors inline in
    `--danger`. Login: "Giriş yap"; Register: "Hesap oluştur" (display name optional).
13. **Health chip states** (header): ok = green pulsing dot "Bedesten · Yargıtay çalışıyor";
    degraded = amber "…yavaş yanıt veriyor"; down = danger "…erişilemiyor". Data from
    `/api/meta/health`, refreshed every 60 s. Label honestly covers only Bedesten/Yargıtay.

---

## 4. Data model (already implemented in `server/app/models.py` — keep as-is)

Tables: `users`, `sessions` (token sha256 PK, sliding expiry), `conversations`,
`messages` (tool_calls_json + citations_json summaries, never raw payloads),
`searches` + `search_results_snapshot` (first 50 normalized rows per search),
`document_cache` (PK source_db+doc_key+chunk_page, shared across users),
`document_views`, `bookmarks` (UNIQUE user+source_db+doc_key, note + tags_json),
`usage_daily` (PK user+day: llm tokens, tool_calls).

PRAGMAs (set in `db.py`): WAL, foreign_keys ON, busy_timeout 5000, synchronous NORMAL.

**doc_key canonicalization** (the invariant everything hangs on): `doc_key =
base64url(json.dumps(DocRef.args, sort_keys=True, ensure_ascii=False)).rstrip("=")`.
Only adapters construct DocRefs, so the same decision always yields the same key —
this powers the cache PK, bookmark uniqueness, and `/doc/{source_db}/{doc_key}` routes.

---

## 5. Adapter layer (`server/app/adapters.py`)

### 5.1 Canonical models (Pydantic)

```python
class DocRef(BaseModel):
    tool: str          # exact document tool name
    args: dict         # exactly the args that tool needs
    chunked: bool      # True → tool paginates in 5,000-char pages

class NormalizedDecision(BaseModel):
    source_db: str
    doc_key: str                  # derived from doc_ref.args (§4)
    doc_ref: DocRef
    title: str                    # synthesized when source has none
    court: str | None
    esas_no: str | None
    karar_no: str | None
    decision_date: str | None     # ISO YYYY-MM-DD when parseable
    decision_date_raw: str | None
    snippet: str | None
    source_url: str | None
    extra: dict = {}

class PageInfo(BaseModel):
    page: int
    page_size: int
    total: int | None             # None → UI shows "Sonraki sayfa" without count
    has_more: bool

class SearchOutcome(BaseModel):
    status: str                   # ok | empty | rate_limited | source_error
    results: list[NormalizedDecision] = []
    page_info: PageInfo | None = None
    error: str | None = None
    retry_after: int | None = None
```

### 5.2 Adapter protocol

```python
class Adapter:
    id: str                    # UI database id (§3.3)
    name: str
    search_tool: str
    def build_search_args(self, p: SearchParams) -> dict: ...
    def normalize(self, payload: Any, page: int) -> SearchOutcome: ...
    def doc_ref_from_row(self, raw_row: dict) -> DocRef: ...
```

`SearchParams` (one shared request model for `/api/search/{db}`):
`{phrase: str = "", date_from: str|None, date_to: str|None, page: int = 1, extra: dict = {}}`
— `extra` carries db-specific fields (court_types, birimAdi, decision_type, KararTuru, …).

### 5.3 Per-database mapping (implement exactly)

| id | search args from params | DocRef | date input format | title synthesis / snippet |
|---|---|---|---|---|
| bedesten | `phrase`; `court_types` from `extra.court_types` (default `["YARGITAYKARARI","DANISTAYKARAR"]`); `pageNumber=page`; `birimAdi` from `extra` (default ALL); dates → `YYYY-MM-DD` | `get_bedesten_document_markdown` `{documentId}` chunked=False | UI `GG.AA.YYYY` → convert | no snippet; title = `"{birimAdi} · E. {esasNo} K. {kararNo}"` |
| anayasa | `decision_type` from `extra` (default `bireysel_basvuru`); `keywords=[phrase]`; `page_to_fetch=page` | `get_anayasa_document_unified` `{document_url}` chunked=True | — | branch row fields on decision_type; keep outcome summary as snippet when present |
| emsal | `keyword=phrase`; `start_date`/`end_date` `DD.MM.YYYY`; `page_number=page` | `get_emsal_document_markdown` `{id}` chunked=False | pass-through `GG.AA.YYYY` | title = `"{daire} · E. {esasNo} K. {kararNo}"` |
| uyusmazlik | `icerik=phrase`; `page_number=page` | `get_uyusmazlik_document_markdown_from_url` `{document_url}` chunked=False | — | title = `"Uyuşmazlık Mahkemesi · E. {esas_sayisi} K. {karar_sayisi}"` |
| kik | `decision_type` from `extra` (default uyusmazlik); `karar_metni=phrase`; dates `YYYY-MM-DD` | `get_kik_v2_document_markdown` `{gundemMaddesiId}` chunked=False | convert | snippet = `basvuruKonusu`; check in-band `error_code` |
| rekabet | `PdfText=phrase`; `KararTuru` from `extra` (default ALL); `page=page` | `get_rekabet_kurumu_document` `{karar_id}` chunked=True | — | title from `title`; skip/flag rows with `karar_id == "UNKNOWN_KARAR_ID"` |
| sayistay | `decision_type` from `extra` (default genel_kurul); `start=(page-1)*10`, `length=10`; type-specific fields pass through `extra` | `get_sayistay_document_unified` `{decision_id, decision_type}` chunked=False | `DD.MM.YYYY` | snippet = `karar_ozeti`/type equivalents; branch fields per type |
| kvkk | `keywords=phrase`; `page=page` | `get_kvkk_document_markdown` `{decision_url}` chunked=True | — | snippet = `description`; **empty-status message must carry the "kaynak geçici olarak erişilemiyor olabilir" caveat** |
| bddk | `keywords=phrase`; `page=page` | `get_bddk_document_markdown` `{document_id}` chunked=True | — | snippet = `content` |
| btk | `keywords=phrase`; `page=page`; `pageSize=10`; optional `decision_no` etc. from `extra` | `get_btk_document_markdown` `{pdf_url}` chunked=True | `YYYY-MM-DD` | title from `title` |
| gib | `keywords=phrase`; `page=page`; `pageSize=10`; optional `kanunNo`/`ozelgeNo` from `extra`; dates `YYYY-MM-DD` | `get_gib_ozelge_document_markdown` `{ozelge_id:int}` chunked=True | convert | results under key **`ozelgeler`** |
| sigorta | `keywords=phrase`; `page=page` | `get_sigorta_tahkim_document_markdown` `{issue_number}` chunked=True | — | snippet = `content`; doc opens the whole journal issue (two-step UX noted in UI) |

Shared normalization rules:
- **Date parser**: accepts ISO 8601 (with/without time), `DD/MM/YYYY`, `DD.MM.YYYY`;
  outputs ISO date; on failure store `decision_date_raw` only.
- **Pagination**: fold `total_records` / `total_results` / `total_records_found` /
  `total`+`start` variants into `PageInfo`; when the source gives no reliable total →
  `total=None`, `has_more = len(rows) == page_size`.
- Registry validated at startup: every adapter's `search_tool` and doc tool must exist in
  `bridge.tool_names()`; missing → mark that db unavailable in `/api/meta/sources`
  (log loudly, don't crash).
- Gating: bddk + sigorta available only if `TAVILY_API_KEY` set (check `os.environ` at boot).

### 5.4 Error classification
Already implemented in `mcp_bridge.classify_result` — reuse it in every adapter/route:
in-band `error=="rate_limit_exceeded"`/`status_code==429` → `rate_limited` (+retry_after);
`error`/`error_message`/`error_code` keys → `source_error`; `markdown_content` starting
with `"ERROR ("` → source_error/rate_limited; exceptions from `call_tool` → `source_error`;
`TimeoutError` → `source_error` ("kaynak zaman aşımına uğradı").

---

## 6. REST API contract (all under `/api`, JSON, session cookie)

Error envelope everywhere: HTTP status + `{detail: str}` (FastAPI default), except search
which returns 200 with `SearchOutcome.status` for source-level failures (a failed upstream
search is not an HTTP error).

### 6.1 `auth.py`
| Route | Behavior |
|---|---|
| `POST /api/auth/register` `{email, password, display_name?}` | 403 if `registration_mode=closed`; validate email format + password ≥ 8 chars; first user gets `role=admin`; creates session, sets `ya_session` HttpOnly cookie (SameSite=Lax, Secure in prod, max-age = 30d); returns `UserOut` |
| `POST /api/auth/login` `{email, password}` | argon2 verify (generic 401 "E-posta veya şifre hatalı"); update `last_login_at`; set cookie; returns `UserOut` |
| `POST /api/auth/logout` | delete session row, clear cookie |
| `GET /api/auth/me` | `UserOut {id, email, display_name, role, locale, theme, preferred_model, created_at}` |
| `GET /api/auth/sessions` | list `{token_prefix, user_agent, created_at, last_seen_at, current: bool}` |
| `DELETE /api/auth/sessions/{token_prefix}` | revoke (cannot revoke through with foreign user) |
| `PATCH /api/auth/me` `{display_name?, locale?, theme?, preferred_model?, password?, current_password?}` | profile updates; password change requires current_password |

### 6.2 `meta.py`
- `GET /api/meta/sources` → for each §3.3 db: `{id, name, sub, desc, color, available,
  gated_reason?, decision_types?: [...], court_types?: [...], birim_options?: [...],
  karar_turu_options?: [...]}` — the UI builds forms from this. Include Bedesten's five
  court types with TR labels and the birimAdi option groups (Yargıtay Hukuk H1–H23,
  Yargıtay Ceza C1–C23, Kurullar, Danıştay D1–D17, Danıştay kurulları).
- `GET /api/meta/health` → proxied `check_government_servers_health`, cached in-process
  60 s → `{status: ok|degraded|down, detail}` (map overall_status; degraded when one of two
  healthy).
- `GET /api/meta/usage` → today's `usage_daily` row + quota limits (for settings meters).

### 6.3 `search.py`
- `POST /api/search/{source_db}` body `SearchParams` → runs adapter → on `ok`/`empty`
  persist `searches` row + snapshot (≤50 rows) → returns
  `{search_id, status, results, page_info, error?, retry_after?}`.
  `origin="manual"`. 404 for unknown db; 409 `"bu kaynak sunucuda yapılandırılmamış"` for
  gated dbs.
- `POST /api/search/{search_id}/rerun` → load `params_json`, re-execute as new search,
  return same shape.
- `GET /api/search/{search_id}/snapshot` → stored rows + the original search meta
  (no upstream calls).

### 6.4 `documents.py`
- `GET /api/documents/{source_db}/{doc_key}?page=1` → cache-through:
  1. lookup `document_cache` (fresh if `fetched_at` < TTL 30d) → serve with `cached: true`;
  2. else decode doc_key → DocRef args, call the adapter's doc tool (with `page_number`
     when chunked), classify errors, upsert cache row, record `document_views` (once per
     user/doc/day), return
     `{source_db, doc_key, title, markdown, page, total_pages, chunked, cached,
       fetched_at, source_url, meta: {court?, esas_no?, karar_no?, decision_date?}}`.
  - Meta comes from the querystring-optional `?meta=` blob the frontend passes from the
    NormalizedDecision it clicked (server stores it in the view row + returns it), since
    several doc tools don't return court metadata.
- `POST /api/documents/{source_db}/{doc_key}/refresh` → bypass cache, refetch page 1.

### 6.5 `conversations.py`
- `GET /api/conversations` → `[{id, title, updated_at, archived}]` newest-first,
  grouped client-side.
- `POST /api/conversations` `{}` → create empty conv (title null) → `{id}`.
- `GET /api/conversations/{id}` → conv + full messages
  `[{id, role, content, status, tool_calls, citations, created_at}]` (JSON fields parsed).
- `PATCH /api/conversations/{id}` `{title?, archived?}` · `DELETE` → cascade.
- `POST /api/conversations/{id}/messages` `{content: str}` → **SSE streamed response**
  (`text/event-stream`, §7.2). Quota-check before start (429 + reset time when exhausted).
- `POST /api/conversations/{id}/stop` → sets a cancel flag on the running generation
  (in-process registry `dict[conv_id, asyncio.Event]`); partial message persists with
  `status="interrupted"`.

### 6.6 `history.py`
- `GET /api/history/searches?db=&status=&q=&limit=50&offset=0` → rows with
  `{id, query_text, source_db, result_count, status, created_at}` (LIKE filter on q).
- `GET /api/history/documents?limit=50` → recent `document_views` (deduped latest per doc)
  with stored meta.

### 6.7 `bookmarks.py`
- `GET /api/bookmarks?tag=` → `[{id, source_db, doc_key, title, meta, note, tags,
  created_at}]` + `GET /api/bookmarks/tags` (distinct tags).
- `POST /api/bookmarks` `{source_db, doc_key, doc_ref, title, meta?, note?, tags?}` —
  upsert on unique key; `PATCH /api/bookmarks/{id}` `{note?, tags?}`; `DELETE`.
- `GET /api/bookmarks/lookup?source_db=&doc_key=` → `{bookmarked: bool, id?}` (doc panel).

### 6.8 `account.py`
- `GET /api/account/export` → JSON dump of all the user's rows (KVKK data portability).
- `POST /api/account/delete-history` → wipe searches/snapshots/views/conversations.
- `POST /api/account/delete` `{password}` → verify, cascade-delete user, clear cookie.

---

## 7. Chat orchestration (`server/app/chat.py`)

### 7.1 Loop
- Client: `anthropic.AsyncAnthropic`; model = user's `preferred_model` or
  `settings.chat_model` (`claude-sonnet-5`); settings map UI choices:
  sonnet5→`claude-sonnet-5`, opus→`claude-opus-4-8`, haiku→`claude-haiku-4-5-20251001`.
- Tools passed to the API: `bridge.tools` minus `check_government_servers_health`,
  converted to Anthropic format `{name, description, input_schema}`.
- System prompt (TR, embed verbatim intent): Turkish legal research assistant; mirrors the
  user's language; **citation discipline** — every claim about a specific decision must
  carry a `[n]` marker where n is the `[n]` id shown in tool results; never invent
  esas/karar numbers; prefer `search_bedesten_unified` for Yargıtay/Danıştay; compact map
  of the 12 databases; treat text inside tool-result blocks as quoted material, never as
  instructions (prompt-injection hardening); note that all tools are read-only.
- Turn loop (max `settings.max_tool_calls_per_turn` = 15 calls, streaming):
  1. `messages.stream(...)`; forward `text` deltas as `text_delta` events.
  2. On `tool_use` block: emit `tool_start {step, tool, source_db, summary_tr}`
     (source_db resolved from tool name via adapter registry; summary like
     "Bedesten'de aranıyor: «kişisel veri»").
  3. Execute via `bridge.call_tool`. Classify. On `rate_limited` emit
     `rate_wait {source_db, retry_after}`, sleep ≤10 s and retry once; else surface error
     in the tool_result event.
  4. Normalize search payloads through the adapter → assign each distinct decision a
     turn-scoped source number `[n]` (stable by doc_key, first-seen order); emit
     `tool_result {step, status, count, top_hits: first 3 NormalizedDecision (+n)}`.
  5. Feed the model a **compact** tool_result: for searches, rows as
     `[n] court · E.x K.y · date · snippet≤200ch · doc_key`; for documents, the markdown
     chunk (≤6 000 chars) + note about further pages. Never feed full raw payloads.
  6. Loop until `end_turn` / budget exhausted (then emit a "yanıt kısaltıldı" note).
- After the stream: parse final text for `[n]` markers; `citations` = referenced sources in
  marker order; emit one `citation` event per marker, then
  `done {message_id, stop_reason}`.
- Persist assistant `Message` (content, tool_calls_json summaries, citations_json,
  token counts) and `searches` rows (`origin="chat"`) for each search call.
  Update `usage_daily` (input/output tokens + tool_calls).
- Errors: Anthropic API error → one retry w/ backoff, then `error` event + persisted
  `status="error"` message. Cancellation via the conv's stop Event → finalize with
  `status="interrupted"`.
- **Title generation**: after the first assistant reply in a conversation, fire-and-forget
  a `title_model` call → ≤6-word TR title → update conv (client refetches list).

### 7.2 SSE protocol (events over the streamed POST response)

```
event: text_delta   data: {"delta": "..."}
event: tool_start   data: {"step": 1, "tool": "search_bedesten_unified",
                           "source_db": "bedesten", "summary": "Bedesten'de aranıyor: «…»"}
event: rate_wait    data: {"source_db": "bedesten", "retry_after": 4}
event: tool_result  data: {"step": 1, "status": "ok", "count": 12,
                           "top_hits": [NormalizedDecision+{n}] }
event: citation     data: {"marker": 1, "decision": NormalizedDecision}
event: usage        data: {"input_tokens": 1200, "output_tokens": 640}
event: done         data: {"message_id": "…", "stop_reason": "end_turn",
                           "conversation_title": "…"?}
event: error        data: {"message": "…"}
```
Each SSE frame: `event: <name>\ndata: <json>\n\n`. Heartbeat comment `: ping\n\n` every
15 s while a tool call is running.

### 7.3 Disconnect behavior
Generation runs in the request handler; if the client disconnects mid-stream, wrap the
generator so the loop **continues to completion** in a shielded task and persists the
message (v1 approximation of the plan's detached generation). Frontend refetches the
conversation on reopen.

---

## 8. Quotas (`server/app/quotas.py`)
- `check_and_touch(user_id, *, need_tool_call=False, tokens=0)` → raises 429 with
  `{detail, reset_at}` when today's row exceeds `daily_llm_token_quota` (300k) or
  `daily_tool_call_quota` (300). Called before each chat turn and each manual search;
  incremented during the turn. UPSERT on `(user_id, day)` (UTC day).

---

## 9. Frontend build phases

Scaffold: `npm create vite@latest web -- --template react-ts`; no UI library; plain CSS
with the §3 tokens (`styles/tokens.css` + `styles/app.css` component classes). Dev proxy:
`server: {proxy: {"/api": "http://127.0.0.1:8600"}}`. `index.html`: `lang="tr"`, IBM Plex
font links, `<title>Yargı Asistan</title>`.

State: React context (`auth`, `app`) + local component state; data fetching with plain
`fetch` wrappers (`lib/api.ts`, credentials: same-origin, JSON, error → thrown `ApiError`).
No router lib — `app.view ∈ {chat, search, history, bookmarks, settings}` mirrors the
design's state machine; auth gate renders `AuthView` when `/api/auth/me` 401s.

Theme: `data-theme` attribute on `<html>`; persisted to the user profile (PATCH me) and
localStorage for pre-login.

SSE client (`lib/sse.ts`): `fetch(url, {method: "POST", body})` → `res.body.getReader()`
→ TextDecoder → split on `\n\n` → parse `event:`/`data:` lines → typed callback map.
AbortController for the stop button (also POSTs `/stop`).

Component tasks (each maps 1:1 to a §3.5 spec item):
1. `tokens.css` + `app.css` (buttons, inputs, cards, chips, segments, pills, scrollbars,
   keyframes).
2. `Sidebar` + `Header` + responsive shell (mobile breakpoint 900px, overlay + backdrop).
3. `AuthView`.
4. `ChatView`: message list (empty state, starters), `ToolStepCard`, `CitationChip`,
   `CitationCard`, streaming reducer consuming SSE events into a message draft, composer
   (auto-grow, Enter/Shift+Enter, stop button), disclaimer.
5. `SearchView`: picker grid (from `/api/meta/sources`), `BedestenForm` + `GenericForm`
   (+ per-db extras), results list, pagination, "Sohbette devam et" (creates conv, sends
   canned prompt referencing the search).
6. `DocPanel`: fetch + chunk pager, bookmark toggle (lookup/upsert/delete), copy citation
   (per-court TR template: `"{court}, E. {esas}, K. {karar}, T. {dd.mm.yyyy}"`), source
   link, cache badge, refresh.
7. `HistoryView` (3 tabs, rerun → SearchView with results, snapshot → modal/inline list).
8. `BookmarksView` (tag filter, note display, open in DocPanel).
9. `SettingsView` (profile edit, sessions list/revoke, lang/theme segments, model radios,
   usage meters from `/api/meta/usage`, data controls wired to §6.8).

---

## 10. Phases & task checklist

### Phase 0 — Workspace ✅ (done)
- [x] `server/pyproject.toml` (uv, yargi-mcp path dep), config/db/models/security/mcp_bridge.

### Phase 1 — Backend foundation
- [ ] `adapters.py` per §5 (all 12 + registry + date parser + pagination folding).
- [ ] `quotas.py` per §8.
- [ ] `api/auth.py`, `api/meta.py` per §6.1–6.2.
- [ ] `main.py`: app factory; lifespan = `init_db()` + `bridge.start()/stop()`; mount
      `api_router`; UTF-8 JSON default response class (`ensure_ascii=False`); serve
      `web/dist` with SPA fallback when the directory exists.
- [ ] `uv sync` then boot check: `uv run uvicorn app.main:app --port 8600`;
      `GET /api/meta/sources` lists 12 dbs with availability.

### Phase 2 — Search & documents
- [ ] `api/search.py`, `api/documents.py`, `api/history.py`, `api/bookmarks.py`,
      `api/account.py`.
- [ ] Manual smoke: register → search bedesten «kişisel veri» → open a document (cache
      MISS then HIT) → bookmark it → history rows exist.

### Phase 3 — Chat
- [ ] `chat.py` orchestrator + SSE per §7; `conversations.py` routes.
- [ ] Smoke with a real `ANTHROPIC_API_KEY`: one question produces tool_start/tool_result/
      text_delta/citation/done and a persisted assistant message + title.

### Phase 4 — Frontend
- [ ] Tasks 1–9 of §9 in order (shell first, then chat, then the rest).
- [ ] `npm run build` clean; FastAPI serves the SPA.

### Phase 5 — Verification & polish
- [ ] End-to-end drive (§11) in a real browser, light + dark, desktop + 900px mobile.
- [ ] `.env.example` for server (`ANTHROPIC_API_KEY`, `SESSION_SECRET`, optional
      `TAVILY_API_KEY`/`BRAVE_API_TOKEN`, `MCP_MODE`, quotas) and a root `README.md`
      (setup: `uv sync`, `npm i && npm run build`, run command, dev mode instructions).
- [ ] `.gitignore` (data/, .env, dist/, node_modules/, __pycache__/).

### Acceptance criteria (Phase 5 gate)
1. Fresh clone + `uv sync` + `npm run build` + one uvicorn command = working app.
2. Register/login/logout; session survives restart; wrong password → generic TR error.
3. Chat: a KVKK/Yargıtay question streams visible tool steps with live status, renders
   `[n]` citations that open the doc panel; stop button interrupts and persists partial;
   conversation title auto-generates; thread reload shows identical content from DB.
4. Structured search: Bedesten form with court chips + dates returns rows; pagination
   works; rate-limit shows the amber "hız sınırı" treatment, not a crash; gated dbs
   (BDDK/Sigorta without TAVILY key) render locked.
5. Document panel: chunked doc (KVKK/Rekabet) pages through; cache badge on second open;
   bookmark toggle + citation copy work.
6. History tabs populated by real activity; rerun works; snapshot renders without
   upstream calls.
7. Settings: theme + language persist; usage meters move after a chat; export downloads
   JSON; delete-history empties History.
8. UI is pixel-faithful to the design file in both themes (spot-check: sidebar, chat
   step cards, search cards, doc panel header, settings meters).

---

## 11. Manual E2E script (run after Phase 5)
1. `cd server && uv run uvicorn app.main:app --port 8600` (with `.env` containing
   `ANTHROPIC_API_KEY`).
2. Browser → `http://127.0.0.1:8600` → register `test@example.com`.
3. Chat: "2024'te Yargıtay'ın kişisel veri ihlali kararlarını özetler misin?" → observe
   step cards → click a citation → doc panel → Kaydet.
4. Arama → Bedesten → phrase «kişisel veri», Yargıtay only, 01.01.2024–31.12.2024 → Ara
   → open row → Sonraki sayfa.
5. Geçmiş → all three tabs → Yeniden on a search.
6. Kayıtlı → bookmark present with note editing.
7. Ayarlar → Koyu theme → reload (persisted) → usage meters non-zero → Tüm verilerimi
   indir.
8. Resize < 900px → sidebar overlay + doc panel full-screen.
