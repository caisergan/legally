# Word → UYAP UDF Conversion — Technical Integration Document

**Status:** research complete, ready for planning · **Date:** 2026-07-19
**Scope:** adding "convert a Word (.docx) document to UYAP UDF format" as a feature of Yargı Asistan (FastAPI backend + React frontend).
**Companion docs:** `docs/EIMZA_INTEGRATION_PLAN.md` (e-imza; signed-UDF touches it in Phase 4).

---

## 1. Why this feature

UDF ("UYAP Doküman Formatı") is the mandatory document format of UYAP, Turkey's national judiciary informatics system. Lawyers draft in Word but must file in UDF via UYAP; the official UYAP Doküman Editörü (a Java Swing app) imports Word poorly (tables shift, formatting breaks), which created a cottage industry of paid converters (udfcevir.com at ₺1499/yr, udfceviripro.com, e-udf.com, …). There is **no PyPI package, no LibreOffice filter, and no maintained drop-in library** for UDF — every product in this space hand-rolls the format. For a legal-research workspace whose users end every workflow by producing a filing, Word→UDF is a high-value, low-competition capability — and it composes directly with our e-imza work (draft → convert → sign → file).

## 2. Competitive reference: udfcevir.com (analyzed 2026-07-19)

Key facts (from fetching the site, its JS bundle, its public API, and string-dumping the actual desktop binary):

- It is **not** a web converter. The site is a Vite/React SPA (Auth0 login, PayTR payments, Netlify Functions backend at `https://udfcevir.com/.netlify/functions`) that sells a **downloadable Go desktop app** (Fyne GUI, module `udf-converter-go`) which converts entirely offline. Only conversion *metadata* (`source_format`, `target_format`, `device_fingerprint`) is uploaded, for license enforcement.
- **Scope:** Word→UDF only (`.doc` and `.docx`); no UDF→Word, no PDF. Batch conversion supported. 5 free conversions, then ₺1499/yr, 3 devices, device-fingerprint licensing.
- **Their fidelity investment** (visible in binary symbols) shows where the hard problems are: a fully custom OOXML parser (styles, numbering, headers/footers, `.rels`, SDT content controls, footnotes, hyperlinks); a from-scratch **EMF/WMF rasterizer** for legacy vector images in old `.doc` files; a page-layout engine with table-splitting across page breaks; and **heuristic wet-signature-image detection** (scanned imza stamps get higher-quality/larger embedding than ordinary images) — a genuinely lawyer-shaped feature.
- Their marketing angle is privacy: "online converters upload your files to their servers." **Our counter-positioning:** conversion happens inside the user's existing authenticated workspace on our own server (no third party), and the feature is bundled with research + drafting + e-imza rather than sold standalone.
- Their ToS disclaims all conversion-accuracy liability — worth mirroring in our own terms.
- Ecosystem note: the author of the main open-source toolkit (saidsurucu/UDF-Toolkit — same author as **yargi-mcp**, which we already use) states in that repo's README that his new paid app is at udfcevir.com. This matters for licensing (§5).

## 3. UDF format specification (reverse-engineered; no official schema exists)

Everything below is corroborated by at least two independent sources: real `content.xml` dumps (format_id 1.7, from 2013–15 forum threads), the actively-maintained UDF-Toolkit writer/reader (format_id 1.8, interoperates with current UYAP Editör 5.4.x), and literal XML format strings extracted from the udfcevir.com binary. The Adalet Bakanlığı has **never published a DTD/XSD**; the only "spec of record" is the UYAP Editör itself.

### 3.1 Container

- A `.udf` file is a plain **ZIP archive (DEFLATE)** containing, in the unsigned case, **exactly one entry: `content.xml`** (UTF-8). No mimetype entry, no META-INF.
- Signed UDFs (per the udfpdf project's documentation) add `documentproperties.xml` (verification code, serial number) and `sign.sgn` (PKCS#7/X.509, TÜBİTAK chain). *Not yet verified against a real signed sample — see §9.*
- Images and background images are **base64 inline in XML attributes**, never separate zip entries.

### 3.2 The offset model (the core mechanic)

`content.xml` mirrors Java Swing's `StyledDocument` run model (the official editor is Swing; style entries literally contain serialized `javax.swing.plaf.FontUIResource[...]` strings):

- **All document text — header, body, footer, table cells — lives in one CDATA blob** inside `<content><![CDATA[...]]></content>`.
- Structural elements carry **no text of their own**; they reference character ranges of that blob via `startOffset` + `length`.
- Offsets are **character counts (not bytes)** — `ğ ş ı ö ü ç` each count as 1. This is the single most consequential correctness rule; any byte-based counting misaligns everything after the first Turkish character.
- Paragraph separator in the blob: `\n` (U+000A). **Caution — the separator convention is not actually pinned down** (§9.1): real UYAP output contains `\n` between paragraphs, but working third-party writers disagree on whether the separator is covered by the preceding run's `length`, referenced by its own tiny run, or omittable entirely — UDF-Toolkit emits *no* separators at all between body paragraphs and its files still load, because the editor rebuilds structure from `<paragraph>` elements, not from the pool. Provisional writer rule until the Phase 0 lab settles it: emit `\n` between paragraphs, excluded from run ranges.
- Placeholder characters, each paired with a `length="1"` element:
  - **U+FFFC** (object replacement) ↔ `<image>`
  - **U+0009** (tab) ↔ `<tab>`
  - **U+200B** (zero-width space) — placeholder for a visually empty paragraph; `length="0"` runs are not accepted by the editor.
- A literal `]]>` in user text must split the CDATA: `text.replace("]]>", "]]]]><![CDATA[>")`.
- Writer hygiene: normalize `\r\n`/`\r` → `\n`; strip or map XML-1.0-invalid control characters before pooling (C0 controls other than `\t`/`\n` are illegal even inside CDATA — e.g. U+000B from pasted content; map to `\n` or drop with a warning); XML-escape every attribute value (font family names can contain `&` or `"`).

### 3.3 Document skeleton

```xml
<?xml version="1.0" encoding="UTF-8" ?>
<template format_id="1.8">
<content><![CDATA[Sayın Hakimliğinize
Davacı taraf aşağıdaki gerekçelerle...]]></content>
<properties>
  <pageFormat mediaSizeName="1" leftMargin="42.52" rightMargin="28.35"
              topMargin="14.17" bottomMargin="14.17" paperOrientation="1"
              headerFOffset="20.0" footerFOffset="20.0" />
</properties>
<elements resolver="hvl-default">
  <paragraph Alignment="1" LeftIndent="0.0" RightIndent="0.0">
    <content startOffset="0" length="19" family="Times New Roman" size="12" bold="true" />
  </paragraph>
  <paragraph Alignment="3" LeftIndent="0.0" RightIndent="0.0">
    <content startOffset="20" length="38" family="Times New Roman" size="12" />
  </paragraph>
</elements>
<styles>
  <style name="default" description="Geçerli" family="Dialog" size="12" bold="false" italic="false"
         foreground="-13421773"
         FONT_ATTRIBUTE_KEY="javax.swing.plaf.FontUIResource[family=Dialog,name=Dialog,style=plain,size=12]" />
  <style name="hvl-default" family="Times New Roman" size="12" description="Gövde" />
</styles>
</template>
```

Offset walkthrough: positions 0–18 = `Sayın Hakimliğinize` (19 chars — Turkish letters count as 1), position 19 = the `\n` separator (left unreferenced under the provisional rule in §3.2), positions 20–57 = `Davacı taraf aşağıdaki gerekçelerle...` (38 chars).

Section order: `content`, `properties`, `elements`, `styles`. Confirmed `format_id` values in the wild: `"1.7"` and `"1.8"` — **emit `"1.8"`**. Default body style is always named `hvl-default`, Times New Roman 12pt.

### 3.4 Element & attribute reference

All length units are **points (pt)** unless noted. A4 = 595.28 × 841.89 pt.

**`<pageFormat>`** — `mediaSizeName` (`"1"` = A4), `leftMargin`/`rightMargin`/`topMargin`/`bottomMargin` (pt, per-document, not fixed), `paperOrientation` (`"1"` = portrait, confirmed; the **landscape value is unverified** — the reverse-engineered spec says `0`, the community UDF→docx reader checks for `2`; Phase 0 lab item — until then always emit `"1"` and warn on landscape sections), `headerFOffset`/`footerFOffset` (always `20.0` in observed output).

**`<bgImage>`** (optional, in `<properties>`) — `bgImageSource`, `bgImageData` (base64), `bgImageBottomMargin`, `bgImageUpMargin`, **`bgImageRigtMargin`** (sic — genuine typo in the format, must be reproduced verbatim; both the reverse-engineered docs and the udfcevir binary preserve it), `bgImageLeftMargin`.

**`<paragraph>`** — `Alignment` (0=left, 1=center, 2=right, 3=justify; map OOXML `both` **and** `distribute` → 3), `LeftIndent`/`RightIndent` (pt; present on every real paragraph — always emit, `0.0` when unset; OOXML `w:ind` values are twips → ÷20), `FirstLineIndent` (pt; hanging indents (`w:hanging`) presumably map to a negative value — unverified, lab item), `LineSpacing` (**multiplier minus one**: single = `0.0`/omitted, 1.15× = `0.15`, 1.5× = `0.5`, 2× = `1.0`; from OOXML auto line rule: `w:line/240 − 1`; `exact`/`atLeast` rules have no clean equivalent — approximate and warn), `SpaceAbove`/`SpaceBelow` (pt), `TabSet` (`"pos:align:leader,..."`; align 0=left/1=center/2=right/3=decimal, leader 0=none/1=dot/2=dash), `resolver` (style name — seen both here and on `<elements>`; emit on `<elements>` like current 1.8 output), list state: `Numbered`/`NumberType`/`Bulleted`/`BulletType`/`ListId`/`ListLevel`.

List enums: `NUMBER_TYPE_{NUMBER,CHAR_SMALL,CHAR_BIG,ROMAN_SMALL,ROMAN_BIG}_{DOT,PARENTHESIS}` (→ `1.` `1)` `a.` `a)` `A.` `A)` `i.` `i)` `I.` `I)`); `BULLET_TYPE_{ELLIPSE,RECTANGLE,RECTANGLE_D,ARROW,DIAMOND,DIAMOND_2,TRIANGLE}`.

**`<content>` (run)** — `startOffset`, `length`, `family`, `size` (pt), `bold`/`italic`/`underline` (`"true"`/`"false"`), `foreground`/`background` (**signed 32-bit Java ARGB int**: `0xFF000000|(R<<16)|(G<<8)|B` reinterpreted as signed — black `-16777216`, white `-1`, red `-65536`), `spellError` (seen in the wild; ignore/omit). No attribute names are confirmed for **strikethrough** or **superscript/subscript** (UdfSharp's mapping table implies they exist) — until the Phase 0 lab discovers them, convert those runs plain with a warning. Word's many underline variants (double, dotted, …) all collapse to boolean `underline="true"`.

**`<image>`** — `imageData` (base64 JPEG/PNG), `startOffset` (position of U+FFFC), `length="1"`, `width`/`height` (pt). OOXML EMU→pt: `pt = EMU × 72 / 914400`.

**`<tab>`** — `startOffset`, `length="1"`, `family`, `size`. **`<space>`** — extra inter-run space marker referencing a space char (seen in real UYAP output between runs).

**`<table>`** — `tableName`, `columnCount`, `columnSpans` (comma-separated widths — **semantics unpinned**: the reverse-engineered spec says absolute pt, but UDF-Toolkit emits proportional values summing to 300 and still loads; provisional: absolute pt from OOXML `gridCol` twips ÷ 20, lab-verify), `border` (`"borderCell"` observed; also `"borderNone"`; full enum not pinned down).
**`<row>`** — `rowName`, `rowType` (`"dataRow"`/`"headerRow"`), `border`, row height (pt; **attribute name inconsistent across sources** — `height` in the spec, `height_min` in the community reader; lab item).
**`<cell>`** — `colspan`, `align` (`"top"`/`"vcenter"`/`"bottom"`), `fillColor` (ARGB int), `border`, `borderStyle` (`"borderStyle-solid"|-dotted|-dashed|-double"`), `borderWidth` (pt), `borderColor` (ARGB int), `borderSpec` (bitmask 1=top 2=right 4=bottom 8=left; 15=all). Cells contain `<paragraph>` children. Default cell padding ≈ 5.4 pt.

**`<header>` / `<footer>`** — contain `<paragraph>`/`<image>` like the body; `<footer>` carries page-number attributes: `pageNumber-spec` (opaque code, e.g. `"BSP32_40"` — grammar undecoded), `pageNumber-fontFace`, `pageNumber-fontSize`, `pageNumber-fontBold/Italic`, `pageNumber-color`.

**`<page-break>`** — wraps the `<paragraph>` that starts the new page.

**`<field>`** — merge fields in UYAP-produced documents ("DAVACI", "VEKİLİ"): `fieldName`, optional `startOffset`/`length`, plus run formatting attrs. We only need to *read* these (Phase 4), never emit them.

**`<styles>`** — must contain at least the `default` (Dialog 12, `FONT_ATTRIBUTE_KEY` with the literal Swing `FontUIResource` string) and `hvl-default` (Times New Roman 12) entries shown in §3.3.

### 3.5 Known conventions from real UYAP output

- Every non-blank paragraph's text ends with `\n`; UYAP inserts a blank paragraph between consecutive non-blank paragraphs (observed in genuine editor output — mirror this in the writer's normalize pass).
- OOXML `w:sz` is **half-points** → divide by 2 for UDF `size`.
- Word `PAGE` fields in footers should map to the `pageNumber-*` footer attributes, not literal text.

## 4. Existing open-source building blocks

| Project | Lang | License | Capability | Use for us |
|---|---|---|---|---|
| saidsurucu/UDF-Toolkit | Python | **none (all rights reserved)** | full docx↔udf↔pdf/md; best-documented (`Docs.md`) | **Reference only — do not vendor code, and do not treat its output as a fidelity benchmark.** Its docx→udf drops headers/footers, merged cells, underline/color; its font family & size extraction is outright broken (`findtext` on attribute-carrying elements — everything comes out Times New Roman 10pt); list enums are misspelled (`PARANTHESE`/`TRE`); image sizes are px (÷9525) where the format wants pt. That its files still open only proves the editor is lenient. The valuable parts are `Docs.md` and the reader (`udf_to_docx.py`) |
| johanfaberrr/uyap-udf-mcp (`udf.py`) | Python | **MIT** | small correct UDF writer (offsets, CDATA escaping, normalize pass, footer placeholder) | **Port/vendor as our writer core** (~250 lines; text/paragraph/runs only — we extend) |
| gokselb/udfpdf | TypeScript | **Apache-2.0** | UDF **reader** → docx/html/md/pdf; CI + tests; documents signed-container entries | Reference (and vendorable) for a future UDF→X read side |
| murataskin/udf-cli | TypeScript | **MIT** | html/md↔udf round-trip; clean `cdata-builder`/`serializer` split | Algorithm reference for HTML/Markdown→UDF (our editor/AI-draft path) |
| gulecfatih/UdfSharp | C# | proprietary source-available | HTML→UDF; **most complete attribute-mapping table** (README) | Completeness checklist only — do not port code |
| akapar/GoogleDocs-UYAP-Donusturucu | Apps Script | none | Google Docs→UDF; general char-level style-run splitting | Algorithm reference for non-docx sources |
| flathub/tr.gov.uyap.Editor | — | packages proprietary editor | official UYAP Editör 5.4.x (OpenJDK 11) | **Install as the ground-truth validation oracle** |

Confirmed dead ends: nothing on PyPI, no LibreOffice filter, no VS Code extension, several toy/dead viewers (mistih/UdfParser, mertcelen/udf-okuyucu), one "open source" project publishing no code and describing the format wrongly as RTF-in-XML (ox3adie1/udf-to-pdf — its claims are contradicted by every primary source).

## 5. Licensing constraints (important)

- **UDF-Toolkit has no LICENSE file** → default copyright. We may learn from it, but must not copy code or closely derive from `Docs.md` text. Its author now sells udfcevir.com — assume permission for commercial reuse is unavailable. Our format knowledge is safe because it is independently corroborated (real 1.7-era forum dumps, the MIT/Apache projects, and the udfcevir binary's own format strings), and file formats themselves are not copyrightable — but **the implementation must be written by us**, using MIT/Apache code (uyap-udf-mcp, udf-cli, udfpdf) as the only vendored/ported sources, each with license attribution retained.
- Do **not** commit the scratchpad research snapshots of UDF-Toolkit files into the repo.

## 6. Proposed architecture

### 6.1 Placement

In-process in the FastAPI backend — no new service, no subprocess, no LibreOffice/Java dependency. Conversion of a typical dilekçe (tens of pages) is CPU-milliseconds work; run it in a thread via `asyncio.to_thread` to keep the event loop free.

```
server/app/udf/
  __init__.py
  model.py          # intermediate document model: Document, Paragraph, Run, Table, Cell, ImageRef, HeaderFooter
  docx_reader.py    # python-docx/lxml → model (incl. section headers/footers, numbering, gridSpan/vMerge)
  writer.py         # model → content.xml string → zipped .udf   (core ported from uyap-udf-mcp, MIT)
  colors.py         # hex/theme color → signed ARGB int
  units.py          # EMU/half-point/twip → pt
server/app/api/udf.py   # router, registered in api/__init__.py like the others
```

The intermediate model is the key design decision: it decouples input formats from the writer, so the same writer later serves **docx→udf**, **markdown/HTML→udf** (AI-drafted petitions from our chat — a differentiator no competitor has), and eventually the read side maps udf→model→docx.

New backend dependencies: `python-docx` and `Pillow` (lxml arrives with python-docx). Port caveat for the MIT writer core: take `uyap-udf-mcp`'s offset/CDATA/escape *mechanics*, but treat its `_normalize` rules as hypotheses, not law — e.g. its "insert a blank paragraph between consecutive non-blank paragraphs" rule is derived from a single sample and, applied blindly, would visually double-space every converted petition. Phase 0 decides which normalize rules are real.

### 6.2 Pipeline (docx → udf)

1. **Validate upload**: extension/MIME `.docx` only (no legacy `.doc` in MVP — that's what LibreOffice would be for, and udfcevir's EMF/WMF renderer shows how expensive `.doc` fidelity is); size cap (e.g. 20 MB); reject encrypted files.
2. **Parse** with `python-docx` walking the lxml body (`w:p`, `w:tbl` order preserved). Sub-rules that decide real-world correctness:
   - **Effective run properties** must be resolved through the full OOXML chain — docDefaults → style `basedOn` chain → paragraph style → character style → direct formatting — including **toggle-property XOR semantics** (bold in style *and* in run = not bold), theme fonts (`w:asciiTheme` via `theme1.xml`) and theme colors. python-docx exposes the raw tree but resolves none of this; **it is the single biggest chunk of reader work** — budget it accordingly.
   - **Tracked changes:** include `w:ins` content (runs nested inside `w:ins` wrappers are *missed* by naive direct-child iteration — iterate depth-aware), exclude `w:del`/`w:delText`, strip comment ranges. Getting this wrong silently files text the lawyer deleted — unacceptable in a legal filing tool; dedicated test fixture required.
   - Soft line breaks (`w:br`) — UDF representation unknown (lab item); until then map to a paragraph break with a warning. Hyperlinks (`w:hyperlink`) — flatten to styled text (blue + underline) with a warning. Text boxes/shapes/SmartArt — skip with a prominent warning (letterheads love text boxes). Multi-section documents — UDF has one `pageFormat`; first section wins, warn when later sections differ.
   - Lists via `w:numPr` + `numbering.xml`; merged cells via `w:gridSpan`/`w:vMerge`; images extracted from *embedded* relationships only, re-encoded via Pillow (keep JPEG as JPEG, PNG as PNG, convert the rest), EMU→pt for dimensions.
3. **Build model**, mapping OOXML → UDF semantics (alignment enum, half-points→pt, hex colors→signed ARGB, `PAGE` field in footer→`pageNumber-*` attrs, unsupported constructs recorded as warnings).
4. **Write UDF**: single-pass offset accumulator over the model producing the CDATA pool and elements simultaneously; normalize pass (trailing `\n`, blank-paragraph insertion, U+200B for empty paragraphs, CDATA `]]>` split); emit skeleton with `format_id="1.8"`, `resolver="hvl-default"`, mandatory `default`+`hvl-default` styles; zip as `content.xml` (DEFLATE).
5. **Respond** with the `.udf` bytes plus a machine-readable list of fidelity warnings ("3 dipnot desteklenmiyor, metne dönüştürüldü" etc.).

### 6.3 API design

```
POST /api/udf/convert          multipart: file=<docx>
  → 200: JSON { filename, udf_base64, warnings: [{code, message_tr}], stats }
  ?dry_run=1 → same JSON without udf_base64
```

- Response shape: JSON envelope with base64 payload (a dilekçe is small; +33% base64 is irrelevant) so **warnings and file travel together** — an octet-stream response would have nowhere to put the warnings, and warnings are a first-class part of this product. The SPA decodes to a Blob for download; sanitize the output filename (stem + `.udf`).
- Auth: existing session (`get_current_user`), same pattern as `documents.py`.
- Quotas: derive the daily limit by `count(*)` on `UdfConversion` for the UTC day (429 + `reset_at`, same shape as `quotas.py`) — a new table auto-creates via `init_db`'s `create_all`, whereas adding a column to `UsageDaily` would need a migration for zero benefit.
- Concurrency: run conversion in `asyncio.to_thread` behind a small global semaphore (2–3) plus one-at-a-time per user, so a 50-file batch can't starve the event loop or other users. Enforce the upload cap manually (check `Content-Length` and count bytes while reading — Starlette does not limit multipart size by itself).
- Persistence: a `UdfConversion` row (user, filename, sizes, warning count, duration, created_at) for history/analytics — **never store file content** (matches our privacy posture and udfcevir's positioning; documents are lawyer work-product).
- Errors in Turkish, matching house style ("Dosya .docx formatında olmalı", "Dosya çok büyük", "Belge dönüştürülemedi").
- Frontend: a "UDF'ye Dönüştür" upload card (drag-drop, multi-file batch client-side by looping the endpoint), warning list rendering, and later a "convert this AI draft to UDF" action in chat.

### 6.4 Security

- `.docx` is attacker-controlled ZIP+XML: enforce decompressed-size and entry-count limits (zip-bomb); **reject any part whose XML contains `<!DOCTYPE`** (legitimate Word files never carry DTDs — this forecloses entity-expansion attacks regardless of parser defaults, rather than trusting lxml settings); cap image dimensions/count before Pillow decoding (decompression-bomb: set `Image.MAX_IMAGE_PIXELS`), strip all macros/embedded OLE silently (they can't exist in UDF anyway).
- **Zero network I/O in the converter:** linked (non-embedded) images (`r:link`) and any external relationship targets are skipped with a warning, never fetched.
- Output is generated purely from the model — no pass-through of raw XML from the input, so no injection into `content.xml` beyond CDATA escaping.
- Process uploads in memory or scratch tmp; delete immediately after response.

## 7. Phased scope

| Phase | Delivers | Notes |
|---|---|---|
| **0 — Format lab (~1–2 days)** | Install the official UYAP Editör (macOS build exists); author minimal documents in it exercising each unknown, save, unzip, diff the XML. Targets: newline/separator convention, landscape `paperOrientation` value, `columnSpans` semantics, row-height attribute name, non-integer font sizes, strikethrough/superscript attribute names, soft-line-break representation, hanging indents, empty-cell filler, `pageNumber-spec` values, border enums, image pt sizing, which `_normalize` rules are real. Output: settled writer conventions + the seed of the golden corpus | Highest-leverage step in the whole plan — converts ~a dozen open questions into facts *before* any converter code is written |
| **1 — MVP** | Text, paragraphs (alignment, indents, spacing), runs (bold/italic/underline, size, family, colors), tabs, empty-paragraph handling, page format/margins, **simple tables** (widths, borders, fill; merged cells approximated via colspan with a warning), tracked-changes-safe text extraction, `.udf` output + warnings; API + quota + minimal UI | Tables cannot wait: the standard dilekçe caption block (DAVACI/DAVALI/VEKİLİ/KONU) *is* a table — without them most real petitions convert wrongly. Validated in UYAP Editör before release |
| **2 — Structure** | Full merged cells (`gridSpan`/`vMerge`), inline images (JPEG/PNG kept, rest converted; pt sizing), numbered/bulleted lists (full enum mapping), page breaks | This is where udfcevir wins on fidelity today |
| **3 — Chrome** | Headers/footers incl. `pageNumber-*` mapping from Word `PAGE` fields, background image (letterhead), batch UX, markdown/AI-draft→UDF path reusing the writer | Markdown path is our unique differentiator and is cheap once the writer exists — pull earlier if desired |
| **4 — Round-trip & signing** | UDF→model reader (reference: Apache-2.0 udfpdf + real UYAP samples incl. `<field>`), UDF→docx/pdf; signed-UDF (`documentproperties.xml` + `sign.sgn`) investigation joined with the signerd/e-imza track | Requires obtaining real signed .udf samples first |

Explicitly out of scope: legacy `.doc` input, EMF/WMF rasterization, footnotes-as-footnotes (convert to inline/endnote text with a warning), text boxes/shapes/SmartArt (skip + prominent warning), `.usf` templates, `<data>`/`<field>` emission.

## 8. Validation & testing strategy

- **Ground-truth oracle:** install official UYAP Editör (5.4.x; Flathub `tr.gov.uyap.Editor` or Windows build) and manually open every golden output. "Opens cleanly and renders identically" is the only real acceptance test — there is no schema to validate against.
- **Golden corpus:** build ~15 representative docx fixtures (dilekçe with tables, Turkish-diacritic-heavy text, images, lists, headers/footers, edge cases: empty paragraphs, `]]>` in text, tracked changes + comments, soft line breaks, multi-section, 50-page document) with committed expected `content.xml` outputs; byte-diff in CI.
- **Property tests:** for arbitrary model instances, assert (a) every element's `[startOffset, startOffset+length)` slice of the CDATA equals the model text, (b) ranges are non-overlapping and ordered, (c) offsets count characters not bytes (fuzz with Turkish/emoji text).
- **Independent parser in CI:** run Apache-2.0 `udfpdf` (npm) over generated files and compare its extracted text against the model text — an automated second opinion from a codebase we didn't write, without needing the proprietary editor in CI.
- **Never benchmark against UDF-Toolkit's emitted XML** — its attribute output is demonstrably wrong (§4); agreement with it proves nothing, disagreement proves nothing.
- **Round-trip test (Phase 4+):** docx→udf→model equals docx→model.
- **Canary check:** generated `bgImage` output must contain the literal misspelled `bgImageRigtMargin`; a "fixed" spelling means someone broke compatibility.
- **Real-world beta:** convert actual user petitions and open them in real UYAP before claiming fidelity; mirror udfcevir's ToS accuracy disclaimer in ours.

## 9. Open questions & risks

**Resolved by the Phase 0 format lab** (each = author a probe document in the official editor, save, unzip, diff — see §7):

1. **Newline/separator convention** — the most consequential unknown. Evidence conflicts three ways: real UYAP output has `\n` between paragraphs; uyap-udf-mcp's real-sample-derived writer puts the `\n` *inside* the paragraph's run range; UDF-Toolkit emits no separators at all and its files still load. The editor demonstrably rebuilds structure from `<paragraph>` elements and tolerates unreferenced pool characters — but we should emit exactly what the editor itself emits. Provisional (until lab): `\n` between paragraphs, excluded from run ranges.
2. Landscape `paperOrientation` value (`0` per docs vs `2` per the community reader).
3. `columnSpans` semantics (absolute pt vs proportional) and the row-height attribute name (`height` vs `height_min`).
4. Non-integer font sizes (does the editor accept `size="11.5"` from `w:sz="23"`?).
5. Attribute names for strikethrough and superscript/subscript, and the representation of soft line breaks (`w:br`) and hanging indents.
6. Empty-cell filler (space vs U+200B) and which uyap-udf-mcp `_normalize` rules reflect the editor vs one sample.
7. **`pageNumber-spec` grammar** (e.g. `BSP32_40`) — create footers with each numbering style, diff.
8. **Border/`borderStyle` enums** — same method.
9. **EMU→pt for images** — formula is `EMU × 72 / 914400`; UDF-Toolkit's ÷9525 yields 96-dpi px (~33% oversize), explaining the discrepancy. Confirm by measuring a known-size image in the editor.
10. **`resolver` placement** (on `<elements>` in 1.8 output vs on `<paragraph>` in 1.7 dumps) — we emit 1.8-style; confirm acceptance.

**Standing risks (not lab-resolvable):**

11. **Signed UDF internals unverified** — `sign.sgn`/`documentproperties.xml` claims come from one Apache-2.0 project's docs; need a real signed `.udf` (from a lawyer contact or UYAP itself) before designing Phase 4.
12. **Format drift:** UYAP Editör updates could change accepted `format_id`/conventions; re-run the format-lab probes and golden corpus against each editor release.
13. **Fidelity long tail:** OOXML effective-style resolution (docDefaults/basedOn/toggle-XOR/themes) is the largest and least glamorous part of the reader — under-budgeting it is the likeliest schedule risk.
14. **Legal:** conversion-accuracy disclaimer in ToS (mirror udfcevir's); KVKK text must state we process but never retain document content.

## 10. Sources

**Format (primary):** saidsurucu/UDF-Toolkit (code + Docs.md — reference only, no license); real `content.xml` dumps: delphiturkiye.com forum t=31215, forum.donanimhaber.com 76852703; johanfaberrr/uyap-udf-mcp (MIT); udfcevir.com desktop binary format strings (independent corroboration).
**Ecosystem:** gokselb/udfpdf (Apache-2.0), murataskin/udf-cli (MIT), gulecfatih/UdfSharp (proprietary, mapping table), akapar/GoogleDocs-UYAP-Donusturucu, talhaorak/uyap-dilekce-kit (MIT), flathub/tr.gov.uyap.Editor, uyap.gov.tr/Uyap-Editor-Yardim.
**Competitor:** udfcevir.com (site, `/.netlify/functions/version` API, Windows binary string analysis, KVKK/ToS pages). Operator: HF Yaratıcı Proje Geliştirme ve Danışmanlık Ltd.
**Known-bad source (do not use):** ox3adie1/udf-to-pdf dev.to posts — describe the format as RTF-in-XML with a `metadata.xml`, contradicted by all primary sources.
