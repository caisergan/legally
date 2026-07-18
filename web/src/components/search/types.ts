export interface SearchDraft {
  phrase: string;
  dateFrom: string;
  dateTo: string;
  page: number;
  extra: Record<string, unknown>;
}

export interface SearchFormProps {
  sourceId: string;
  draft: SearchDraft;
  onChange: (draft: SearchDraft) => void;
}
