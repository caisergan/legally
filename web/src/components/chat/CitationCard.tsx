import type { CSSProperties } from "react";
import { decisionRef, displayDate, sourceColor } from "../../lib/format";
import type { CitationPayload } from "../../lib/types";
import { useApp } from "../../state/app";
import { Icon } from "../Icon";

export function CitationCard({ citation }: { citation: CitationPayload }) {
  const { openDocument } = useApp();
  const decision = citation.decision;
  return (
    <button type="button" className="citation-card" onClick={() => openDocument(decision)}>
      <span className="citation-marker">{citation.marker}</span>
      <span className="citation-body">
        <span className="citation-title-row">
          <span className="db-dot db-dot--sm" style={{ "--db-color": sourceColor(decision.source_db) } as CSSProperties} />
          <span className="citation-court">{decision.court || decision.title}</span>
        </span>
        <span className="citation-ref">{decisionRef(decision)} · {displayDate(decision.decision_date || decision.decision_date_raw)}</span>
        {decision.snippet && <span className="citation-snippet">{decision.snippet}</span>}
      </span>
      <Icon name="chevron-right" size={16} className="muted" />
    </button>
  );
}
