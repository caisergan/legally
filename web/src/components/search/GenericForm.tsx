import { Icon } from "../Icon";
import type { SourceMeta } from "../../lib/types";
import type { SearchDraft } from "./types";

interface Option {
  value: string;
  label: string;
}

const fallbacks: Record<string, Option[]> = {
  anayasa: [
    { value: "bireysel_basvuru", label: "Bireysel başvuru" },
    { value: "norm_denetimi", label: "Norm denetimi" },
  ],
  kik: [
    { value: "uyusmazlik", label: "Uyuşmazlık" },
    { value: "duzenleyici", label: "Düzenleyici" },
    { value: "mahkeme", label: "Mahkeme" },
  ],
  sayistay: [
    { value: "genel_kurul", label: "Genel kurul" },
    { value: "temyiz_kurulu", label: "Temyiz kurulu" },
    { value: "daire", label: "Daire" },
  ],
};

function normalizeOptions(value: unknown, fallback: Option[] = []): Option[] {
  if (!Array.isArray(value)) return fallback;
  const rows = value.flatMap((option) => {
    if (typeof option === "string") return [{ value: option, label: option }];
    if (!option || typeof option !== "object") return [];
    const row = option as Record<string, unknown>;
    const itemValue = row.value ?? row.id ?? row.key ?? row.label;
    const label = row.label ?? row.name ?? itemValue;
    if (typeof itemValue !== "string" || typeof label !== "string") return [];
    return [{ value: itemValue, label }];
  });
  return rows.length ? rows : fallback;
}

interface GenericFormProps {
  source: SourceMeta;
  draft: SearchDraft;
  onChange: (draft: SearchDraft) => void;
}

export function GenericForm({ source, draft, onChange }: GenericFormProps) {
  const patch = (changes: Partial<SearchDraft>) => onChange({ ...draft, ...changes, page: 1 });
  const patchExtra = (changes: Record<string, unknown>) => patch({ extra: { ...draft.extra, ...changes } });
  const decisionTypeOptions = normalizeOptions(source.decision_types, fallbacks[source.id]);
  const selectedDecisionType = typeof draft.extra.decision_type === "string"
    ? draft.extra.decision_type
    : decisionTypeOptions[0]?.value;
  const competitionOptions = normalizeOptions(source.karar_turu_options, [
    { value: "ALL", label: "Tüm karar türleri" },
    { value: "Birleşme ve Devralma", label: "Birleşme ve Devralma" },
    { value: "Rekabet İhlali", label: "Rekabet İhlali" },
    { value: "Menfi Tespit ve Muafiyet", label: "Menfi Tespit ve Muafiyet" },
    { value: "Özelleştirme", label: "Özelleştirme" },
    { value: "Diğer", label: "Diğer" },
  ]);

  return (
    <>
      <div className="schema-badge"><Icon name="database" size={13} />Form, aracın canlı JSON şemasından üretildi</div>

      {decisionTypeOptions.length > 0 && (
        <div className="form-extra">
          <div className="field-label" style={{ marginBottom: 9 }}>Karar türü</div>
          <div className="court-chips">
            {decisionTypeOptions.map((option) => (
              <button
                type="button"
                className={`court-chip ${selectedDecisionType === option.value ? "active" : ""}`}
                key={option.value}
                onClick={() => patchExtra({ decision_type: option.value })}
              >
                {option.label}
              </button>
            ))}
          </div>
        </div>
      )}

      {source.id === "rekabet" && (
        <label className="field form-extra">
          <span className="field-label">Karar türü</span>
          <select
            className="select"
            value={typeof draft.extra.KararTuru === "string" ? draft.extra.KararTuru : "ALL"}
            onChange={(event) => patchExtra({ KararTuru: event.target.value })}
          >
            {competitionOptions.map((option) => <option value={option.value} key={option.value}>{option.label}</option>)}
          </select>
        </label>
      )}

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
      </div>

      {(source.id === "btk" || source.id === "gib") && (
        <div className="field-grid">
          {source.id === "btk" ? (
            <label className="field">
              <span className="field-label">Karar numarası <span className="field-hint">· isteğe bağlı</span></span>
              <input
                className="input input--mono"
                value={typeof draft.extra.decision_no === "string" ? draft.extra.decision_no : ""}
                onChange={(event) => patchExtra({ decision_no: event.target.value || undefined })}
                placeholder="2024/DK-…"
              />
            </label>
          ) : (
            <>
              <label className="field">
                <span className="field-label">Kanun no <span className="field-hint">· isteğe bağlı</span></span>
                <input
                  className="input input--mono"
                  value={typeof draft.extra.kanunNo === "string" ? draft.extra.kanunNo : ""}
                  onChange={(event) => patchExtra({ kanunNo: event.target.value || undefined })}
                  placeholder="3065"
                />
              </label>
              <label className="field">
                <span className="field-label">Özelge no <span className="field-hint">· isteğe bağlı</span></span>
                <input
                  className="input input--mono"
                  value={typeof draft.extra.ozelgeNo === "string" ? draft.extra.ozelgeNo : ""}
                  onChange={(event) => patchExtra({ ozelgeNo: event.target.value || undefined })}
                  placeholder="Özelge numarası"
                />
              </label>
            </>
          )}
        </div>
      )}

      {source.id === "sigorta" && (
        <div className="inline-notice inline-notice--amber form-extra">
          <Icon name="database" size={15} />
          <span>Sigorta sonuçları kararın yayımlandığı hakem dergisi sayısını açar; belge panelinde ilgili karar başlığını sayfa içinde bulabilirsiniz.</span>
        </div>
      )}

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
