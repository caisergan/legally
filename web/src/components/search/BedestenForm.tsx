import { Icon } from "../Icon";
import type { SourceMeta } from "../../lib/types";
import type { SearchDraft } from "./types";

interface Option {
  value: string;
  label: string;
  group?: string;
}

const fallbackCourts: Option[] = [
  { value: "YARGITAYKARARI", label: "Yargıtay" },
  { value: "DANISTAYKARAR", label: "Danıştay" },
  { value: "YERELHUKUK", label: "Yerel Mahkeme" },
  { value: "ISTINAFHUKUK", label: "İstinaf (BAM)" },
  { value: "KYB", label: "Kanun Yararına Bozma" },
];

const operators = ['"tam ifade"', "+gerekli", "-hariç", "VE", "VEYA", "DEĞİL"];

function normalizeOption(option: unknown): Option | null {
  if (typeof option === "string") return { value: option, label: option };
  if (!option || typeof option !== "object") return null;
  const row = option as Record<string, unknown>;
  const value = row.value ?? row.id ?? row.key ?? row.code ?? row.label;
  const label = row.label ?? row.name ?? row.value ?? value;
  if (typeof value !== "string" || typeof label !== "string") return null;
  return { value, label, group: typeof row.group === "string" ? row.group : undefined };
}

function normalizeOptions(options: unknown, fallback: Option[] = []): Option[] {
  if (!Array.isArray(options)) return fallback;
  const normalized = options.map(normalizeOption).filter((item): item is Option => Boolean(item));
  return normalized.length ? normalized : fallback;
}

interface BedestenFormProps {
  source: SourceMeta;
  draft: SearchDraft;
  onChange: (draft: SearchDraft) => void;
}

export function BedestenForm({ source, draft, onChange }: BedestenFormProps) {
  const courts = normalizeOptions(source.court_types, fallbackCourts);
  const chambers = normalizeOptions(source.birim_options);
  const selectedCourts = Array.isArray(draft.extra.court_types)
    ? draft.extra.court_types.filter((value): value is string => typeof value === "string")
    : ["YARGITAYKARARI"];
  const chamber = typeof draft.extra.birimAdi === "string" ? draft.extra.birimAdi : "ALL";

  const patch = (changes: Partial<SearchDraft>) => onChange({ ...draft, ...changes, page: 1 });
  const patchExtra = (changes: Record<string, unknown>) => patch({ extra: { ...draft.extra, ...changes } });

  const toggleCourt = (value: string) => {
    const next = selectedCourts.includes(value)
      ? selectedCourts.filter((court) => court !== value)
      : [...selectedCourts, value];
    patchExtra({ court_types: next });
  };

  const insertOperator = (operator: string) => {
    const separator = draft.phrase && !draft.phrase.endsWith(" ") ? " " : "";
    patch({ phrase: `${draft.phrase}${separator}${operator} ` });
  };

  return (
    <>
      <div className="form-section">
        <div className="field-label" style={{ marginBottom: 9 }}>Mahkeme türü</div>
        <div className="court-chips">
          {courts.map((court) => (
            <button
              type="button"
              className={`court-chip ${selectedCourts.includes(court.value) ? "active" : ""}`}
              key={court.value}
              onClick={() => toggleCourt(court.value)}
              aria-pressed={selectedCourts.includes(court.value)}
            >
              {court.label}
            </button>
          ))}
        </div>
      </div>

      <div className="field-grid field-grid--single">
        <label className="field">
          <span className="field-label">Daire / Kurul <span className="field-hint">· {chambers.length || 81} birim</span></span>
          <span className="search-input-wrap">
            <Icon name="search" size={15} className="muted" />
            <input
              value={chamber === "ALL" ? "" : chamber}
              onChange={(event) => patchExtra({ birimAdi: event.target.value || "ALL" })}
              placeholder="Daire ara… (ör. Yargıtay 9. Hukuk Dairesi)"
              list="bedesten-chambers"
            />
          </span>
          <datalist id="bedesten-chambers">
            {chambers.map((option) => <option value={option.value} key={`${option.group || ""}-${option.value}`}>{option.label}</option>)}
          </datalist>
        </label>
      </div>

      <div className="form-section">
        <label className="field">
          <span className="field-label">Aranan ifade</span>
          <input
            className="input"
            value={draft.phrase}
            onChange={(event) => patch({ phrase: event.target.value })}
            placeholder="Anahtar kelime veya ifade"
          />
        </label>
        <div className="operator-chips">
          {operators.map((operator) => (
            <button type="button" className="operator-chip" onClick={() => insertOperator(operator)} key={operator}>{operator}</button>
          ))}
          <span className="operator-note">joker/regex desteklenmez</span>
        </div>
      </div>

      <div className="field-grid">
        <label className="field">
          <span className="field-label">Başlangıç tarihi</span>
          <input
            className="input input--mono"
            value={draft.dateFrom}
            onChange={(event) => patch({ dateFrom: event.target.value })}
            placeholder="GG.AA.YYYY"
            inputMode="numeric"
          />
        </label>
        <label className="field">
          <span className="field-label">Bitiş tarihi</span>
          <input
            className="input input--mono"
            value={draft.dateTo}
            onChange={(event) => patch({ dateTo: event.target.value })}
            placeholder="GG.AA.YYYY"
            inputMode="numeric"
          />
        </label>
      </div>
    </>
  );
}
