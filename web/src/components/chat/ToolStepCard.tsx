import { type CSSProperties, useEffect, useState } from "react";
import { decisionRef, decisionToTarget, displayDate, shortDecisionRef, sourceColor, sourceName } from "../../lib/format";
import type { ToolHit } from "../../lib/types";
import { useApp } from "../../state/app";
import { Icon } from "../Icon";

export type ToolStepStatus = "running" | "waiting" | "done" | "rate" | "error";

export interface ToolStep {
  step: number;
  tool: string;
  sourceDb: string;
  summary: string;
  status: ToolStepStatus;
  retryAfter?: number;
  count?: number;
  hits: ToolHit[];
}

export function ToolStepCard({ step }: { step: ToolStep }) {
  const { openDocument } = useApp();
  const [countdown, setCountdown] = useState(step.retryAfter ?? 0);

  useEffect(() => {
    setCountdown(step.retryAfter ?? 0);
    if (step.status !== "waiting" || !step.retryAfter) return;
    const startedAt = Date.now();
    const timer = window.setInterval(() => {
      const elapsed = Math.floor((Date.now() - startedAt) / 1000);
      setCountdown(Math.max(0, (step.retryAfter ?? 0) - elapsed));
    }, 250);
    return () => window.clearInterval(timer);
  }, [step.retryAfter, step.status]);

  const status = step.status === "running" ? <span className="spinner" aria-label="Aranıyor" />
    : step.status === "waiting" ? (
      <span className="status-pill status-pill--amber"><Icon name="clock" size={12} />{countdown} sn</span>
    ) : step.status === "rate" ? (
      <span className="status-pill status-pill--amber"><Icon name="clock" size={12} />hız sınırı</span>
    ) : step.status === "error" ? (
      <span className="status-pill status-pill--danger">kaynak hatası</span>
    ) : (
      <span className="status-pill status-pill--green">{step.count ?? 0} sonuç</span>
    );

  return (
    <div className="tool-step">
      <div className="tool-step-head">
        <span className="db-dot db-dot--sm" style={{ "--db-color": sourceColor(step.sourceDb) } as CSSProperties} />
        <span className="tool-step-db">{sourceName(step.sourceDb)}</span>
        <span className="tool-name" title={step.tool}>{step.tool}</span>
        <span className="tool-spacer" />
        {status}
      </div>
      <div className="tool-summary">{step.summary}</div>
      {step.hits.length > 0 && (
        <div className="tool-hits">
          {step.hits.slice(0, 3).map((hit) => (
            <button
              type="button"
              className="tool-hit"
              key={`${hit.doc_key}-${hit.n}`}
              onClick={() => openDocument(decisionToTarget(hit))}
              title={`${hit.court || hit.title} · ${decisionRef(hit)}`}
            >
              <span className="db-dot db-dot--sm" style={{ "--db-color": sourceColor(hit.source_db) } as CSSProperties} />
              <span className="tool-hit-court">{hit.court || hit.title}</span>
              <span className="tool-hit-ref">{shortDecisionRef(hit)}</span>
              <span className="tool-hit-date">{displayDate(hit.decision_date || hit.decision_date_raw)}</span>
            </button>
          ))}
        </div>
      )}
    </div>
  );
}
