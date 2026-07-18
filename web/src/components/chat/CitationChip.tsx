import type { CitationPayload } from "../../lib/types";
import { useApp } from "../../state/app";

export function CitationChip({ citation, marker }: { citation?: CitationPayload; marker: number }) {
  const { openDocument } = useApp();
  return (
    <button
      type="button"
      className="citation-chip"
      title={citation ? "Kaynağı aç" : "Kaynak henüz yüklenmedi"}
      disabled={!citation}
      onClick={() => citation && openDocument(citation.decision)}
    >
      [{marker}]
    </button>
  );
}
