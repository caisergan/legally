from __future__ import annotations

import base64
import json
import logging
import math
import os
import re
from collections.abc import Callable
from datetime import date, datetime
from typing import Any, Literal

from pydantic import BaseModel, ConfigDict, Field

from .mcp_bridge import classify_result

logger = logging.getLogger("yargi_asistan.adapters")


class CanonicalModel(BaseModel):
    model_config = ConfigDict(extra="forbid")


class DocRef(CanonicalModel):
    tool: str
    args: dict[str, Any]
    chunked: bool


class NormalizedDecision(CanonicalModel):
    source_db: str
    doc_key: str
    doc_ref: DocRef
    title: str
    court: str | None
    esas_no: str | None
    karar_no: str | None
    decision_date: str | None
    decision_date_raw: str | None
    snippet: str | None
    source_url: str | None
    extra: dict[str, Any] = Field(default_factory=dict)


class PageInfo(CanonicalModel):
    page: int
    page_size: int
    total: int | None
    has_more: bool


class SearchParams(CanonicalModel):
    phrase: str = ""
    date_from: str | None = None
    date_to: str | None = None
    page: int = Field(default=1, ge=1)
    extra: dict[str, Any] = Field(default_factory=dict)


class SearchOutcome(CanonicalModel):
    status: Literal["ok", "empty", "rate_limited", "source_error"]
    results: list[NormalizedDecision] = Field(default_factory=list)
    page_info: PageInfo | None = None
    error: str | None = None
    retry_after: int | None = None


def make_doc_key(args: dict) -> str:
    raw = json.dumps(args, sort_keys=True, ensure_ascii=False).encode("utf-8")
    return base64.urlsafe_b64encode(raw).decode("ascii").rstrip("=")


def decode_doc_key(doc_key: str) -> dict:
    pad = "=" * (-len(doc_key) % 4)
    return json.loads(base64.urlsafe_b64decode(doc_key + pad).decode("utf-8"))


def parse_date(value: str | date | datetime | None) -> str | None:
    if value is None:
        return None
    if isinstance(value, datetime):
        return value.date().isoformat()
    if isinstance(value, date):
        return value.isoformat()

    raw = str(value).strip()
    if not raw:
        return None

    try:
        return datetime.fromisoformat(raw.replace("Z", "+00:00")).date().isoformat()
    except ValueError:
        pass

    for date_format in ("%d/%m/%Y", "%d.%m.%Y"):
        try:
            return datetime.strptime(raw, date_format).date().isoformat()
        except ValueError:
            continue
    return None


def _text(value: Any) -> str | None:
    if value is None:
        return None
    normalized = str(value).strip()
    return normalized or None


def _as_dict(value: Any) -> dict[str, Any]:
    if isinstance(value, BaseModel):
        return value.model_dump(mode="json")
    if isinstance(value, dict):
        return value
    raise TypeError("kaynak yanıtı nesne değil")


def _classify(payload: Any) -> tuple[str, str | None, int | None]:
    try:
        status, error, retry_after = classify_result(payload)
        # KİK uses the string sentinel "0" for a successful upstream response.
        # The shared classifier intentionally treats other truthy error codes as errors.
        if (
            status == "source_error"
            and isinstance(payload, dict)
            and str(payload.get("error_code", "")).strip() == "0"
            and not payload.get("error")
            and not payload.get("error_message")
        ):
            without_success_sentinel = dict(payload)
            without_success_sentinel.pop("error_code", None)
            return classify_result(without_success_sentinel)
        return status, error, retry_after
    except (TypeError, ValueError):
        if isinstance(payload, dict) and (
            payload.get("error") == "rate_limit_exceeded"
            or payload.get("status_code") == 429
        ):
            retry_after = payload.get("retry_after")
            try:
                parsed_retry_after = math.ceil(float(retry_after))
            except (TypeError, ValueError):
                parsed_retry_after = None
            return "rate_limited", _text(payload.get("message")), parsed_retry_after
        return "source_error", "kaynak hatası", None


def _normalizable(payload: Any) -> tuple[dict[str, Any] | None, SearchOutcome | None]:
    status, error, retry_after = _classify(payload)
    if status != "ok":
        return None, SearchOutcome(
            status=status,
            error=error or "kaynak hatası",
            retry_after=retry_after,
        )

    try:
        parsed = json.loads(payload) if isinstance(payload, str) else payload
        data = _as_dict(parsed)
    except (json.JSONDecodeError, TypeError, ValueError):
        return None, SearchOutcome(status="source_error", error="kaynak hatası")

    status, error, retry_after = _classify(data)
    if status != "ok":
        return None, SearchOutcome(
            status=status,
            error=error or "kaynak hatası",
            retry_after=retry_after,
        )
    return data, None


def outcome_from_exception(error: BaseException) -> SearchOutcome:
    if isinstance(error, TimeoutError):
        return SearchOutcome(
            status="source_error", error="kaynak zaman aşımına uğradı"
        )
    return SearchOutcome(status="source_error", error="kaynak hatası")


def _page_info(
    payload: dict[str, Any], page: int, returned_rows: int, default_page_size: int = 10
) -> PageInfo:
    page_size = default_page_size
    for key in ("page_size", "pageSize", "length", "results_per_page"):
        value = payload.get(key)
        if value is not None:
            try:
                page_size = max(1, int(value))
            except (TypeError, ValueError):
                pass
            break

    total: int | None = None
    for key in ("total_records", "total_results", "total_records_found", "total"):
        value = payload.get(key)
        if value is not None:
            try:
                total = max(0, int(value))
            except (TypeError, ValueError):
                total = None
            break

    start_value = payload.get("start", (page - 1) * page_size)
    try:
        start = max(0, int(start_value))
    except (TypeError, ValueError):
        start = (page - 1) * page_size
    has_more = start + returned_rows < total if total is not None else returned_rows == page_size
    return PageInfo(page=page, page_size=page_size, total=total, has_more=has_more)


def _decision(
    source_db: str,
    doc_ref: DocRef,
    *,
    title: str,
    court: str | None = None,
    esas_no: str | None = None,
    karar_no: str | None = None,
    date_value: Any = None,
    snippet: str | None = None,
    source_url: str | None = None,
    extra: dict[str, Any] | None = None,
) -> NormalizedDecision:
    raw_date = _text(date_value)
    normalized_date = parse_date(raw_date)
    return NormalizedDecision(
        source_db=source_db,
        doc_key=make_doc_key(doc_ref.args),
        doc_ref=doc_ref,
        title=title,
        court=court,
        esas_no=esas_no,
        karar_no=karar_no,
        decision_date=normalized_date,
        decision_date_raw=raw_date if raw_date and normalized_date is None else None,
        snippet=snippet,
        source_url=source_url,
        extra=extra or {},
    )


def _format_date(value: str | None, *, dotted: bool) -> str | None:
    if not value:
        return None
    parsed = parse_date(value)
    if parsed is None:
        return value
    if dotted:
        return datetime.strptime(parsed, "%Y-%m-%d").strftime("%d.%m.%Y")
    return parsed


def _copy_extra(
    target: dict[str, Any], extra: dict[str, Any], allowed_keys: tuple[str, ...]
) -> None:
    for key in allowed_keys:
        if key in extra and extra[key] not in (None, ""):
            target[key] = extra[key]


def _finish(
    adapter_id: str,
    payload: dict[str, Any],
    rows: Any,
    page: int,
    normalizer: Callable[[dict[str, Any]], NormalizedDecision],
    *,
    empty_error: str | None = None,
    pagination_row_count: int | None = None,
    allow_filtered_empty: bool = False,
) -> SearchOutcome:
    if not isinstance(rows, list):
        return SearchOutcome(status="source_error", error="kaynak hatası")

    results: list[NormalizedDecision] = []
    for raw_row in rows:
        try:
            results.append(normalizer(_as_dict(raw_row)))
        except (KeyError, TypeError, ValueError) as error:
            logger.warning("%s satırı normalize edilemedi: %s", adapter_id, error)

    returned_rows = len(rows) if pagination_row_count is None else pagination_row_count
    page_info = _page_info(payload, page, returned_rows)
    if not results:
        if rows and not allow_filtered_empty:
            return SearchOutcome(status="source_error", error="kaynak hatası")
        return SearchOutcome(status="empty", page_info=page_info, error=empty_error)
    return SearchOutcome(status="ok", results=results, page_info=page_info)


class Adapter:
    id: str
    name: str
    search_tool: str
    doc_tool: str
    doc_chunked: bool

    def build_search_args(self, p: SearchParams) -> dict[str, Any]:
        raise NotImplementedError

    def normalize(self, payload: Any, page: int) -> SearchOutcome:
        raise NotImplementedError

    def doc_ref_from_row(self, row: dict[str, Any]) -> DocRef:
        raise NotImplementedError

    def _normalize_rows(
        self,
        payload: Any,
        page: int,
        normalizer: Callable[[dict[str, Any]], NormalizedDecision],
        *,
        rows_key: str = "decisions",
        empty_error: str | None = None,
    ) -> SearchOutcome:
        data, error_outcome = _normalizable(payload)
        if error_outcome is not None:
            return error_outcome
        if data is None:
            return SearchOutcome(status="source_error", error="kaynak hatası")
        return _finish(
            self.id,
            data,
            data.get(rows_key, []),
            page,
            normalizer,
            empty_error=empty_error,
        )


class BedestenAdapter(Adapter):
    id = "bedesten"
    name = "Bedesten"
    search_tool = "search_bedesten_unified"
    doc_tool = "get_bedesten_document_markdown"
    doc_chunked = False

    def build_search_args(self, p: SearchParams) -> dict[str, Any]:
        args: dict[str, Any] = {
            "phrase": p.phrase,
            "court_types": p.extra.get(
                "court_types", ["YARGITAYKARARI", "DANISTAYKARAR"]
            ),
            "pageNumber": p.page,
            "birimAdi": p.extra.get("birimAdi", "ALL"),
        }
        if date_from := _format_date(p.date_from, dotted=False):
            args["kararTarihiStart"] = date_from
        if date_to := _format_date(p.date_to, dotted=False):
            args["kararTarihiEnd"] = date_to
        return args

    def doc_ref_from_row(self, row: dict[str, Any]) -> DocRef:
        document_id = _text(row.get("documentId"))
        if not document_id:
            raise ValueError("documentId eksik")
        return DocRef(
            tool=self.doc_tool,
            args={"documentId": document_id},
            chunked=self.doc_chunked,
        )

    def _normalize_row(self, row: dict[str, Any]) -> NormalizedDecision:
        doc_ref = self.doc_ref_from_row(row)
        birim = _text(row.get("birimAdi")) or "Bedesten"
        esas_no = _text(row.get("esasNo"))
        karar_no = _text(row.get("kararNo"))
        item_type = row.get("itemType")
        item_meta = item_type if isinstance(item_type, dict) else {}
        court = birim or _text(item_meta.get("description")) or _text(item_meta.get("name"))
        return _decision(
            self.id,
            doc_ref,
            title=f"{birim} · E. {esas_no or '-'} K. {karar_no or '-'}",
            court=court,
            esas_no=esas_no,
            karar_no=karar_no,
            date_value=row.get("kararTarihi") or row.get("kararTarihiStr"),
            source_url=_text(row.get("source_url")),
            extra={"item_type": item_meta.get("name") or item_type},
        )

    def normalize(self, payload: Any, page: int) -> SearchOutcome:
        return self._normalize_rows(payload, page, self._normalize_row)


class AnayasaAdapter(Adapter):
    id = "anayasa"
    name = "Anayasa Mahkemesi"
    search_tool = "search_anayasa_unified"
    doc_tool = "get_anayasa_document_unified"
    doc_chunked = True

    def build_search_args(self, p: SearchParams) -> dict[str, Any]:
        return {
            "decision_type": p.extra.get("decision_type", "bireysel_basvuru"),
            "keywords": [p.phrase],
            "page_to_fetch": p.page,
            "results_per_page": 10,
        }

    def doc_ref_from_row(self, row: dict[str, Any]) -> DocRef:
        document_url = _text(row.get("decision_page_url"))
        if not document_url:
            raise ValueError("decision_page_url eksik")
        return DocRef(
            tool=self.doc_tool,
            args={"document_url": document_url},
            chunked=self.doc_chunked,
        )

    @staticmethod
    def _case_numbers(reference: str | None) -> tuple[str | None, str | None]:
        if not reference:
            return None, None
        esas_match = re.search(r"\bE\.?\s*([0-9]{4}/[0-9]+)", reference, re.IGNORECASE)
        karar_match = re.search(r"\bK\.?\s*([0-9]{4}/[0-9]+)", reference, re.IGNORECASE)
        return (
            esas_match.group(1) if esas_match else None,
            karar_match.group(1) if karar_match else None,
        )

    def _normalize_row(
        self, row: dict[str, Any], decision_type: str
    ) -> NormalizedDecision:
        doc_ref = self.doc_ref_from_row(row)
        reference = _text(row.get("decision_reference_no"))
        if decision_type == "norm_denetimi":
            esas_no, karar_no = self._case_numbers(reference)
            return _decision(
                self.id,
                doc_ref,
                title=f"Anayasa Mahkemesi · {reference or 'Norm Denetimi Kararı'}",
                court="Anayasa Mahkemesi",
                esas_no=esas_no,
                karar_no=karar_no,
                date_value=row.get("decision_date_summary"),
                snippet=_text(row.get("decision_outcome_summary")),
                source_url=_text(row.get("decision_page_url")),
                extra={"decision_type": decision_type},
            )

        title = _text(row.get("title")) or f"Anayasa Mahkemesi · {reference or 'Bireysel Başvuru'}"
        return _decision(
            self.id,
            doc_ref,
            title=title,
            court=_text(row.get("decision_making_body")) or "Anayasa Mahkemesi",
            esas_no=reference,
            date_value=row.get("decision_date_summary"),
            snippet=_text(row.get("application_subject_summary")),
            source_url=_text(row.get("decision_page_url")),
            extra={
                "decision_type": decision_type,
                "decision_type_summary": row.get("decision_type_summary"),
            },
        )

    def normalize(self, payload: Any, page: int) -> SearchOutcome:
        data, error_outcome = _normalizable(payload)
        if error_outcome is not None:
            return error_outcome
        if data is None:
            return SearchOutcome(status="source_error", error="kaynak hatası")
        decision_type = _text(data.get("decision_type")) or "bireysel_basvuru"
        return _finish(
            self.id,
            data,
            data.get("decisions", []),
            page,
            lambda row: self._normalize_row(row, decision_type),
        )


class EmsalAdapter(Adapter):
    id = "emsal"
    name = "Emsal"
    search_tool = "search_emsal_detailed_decisions"
    doc_tool = "get_emsal_document_markdown"
    doc_chunked = False

    def build_search_args(self, p: SearchParams) -> dict[str, Any]:
        args: dict[str, Any] = {"keyword": p.phrase, "page_number": p.page}
        if date_from := _format_date(p.date_from, dotted=True):
            args["start_date"] = date_from
        if date_to := _format_date(p.date_to, dotted=True):
            args["end_date"] = date_to
        _copy_extra(
            args,
            p.extra,
            (
                "selected_bam_civil_court",
                "selected_civil_court",
                "selected_regional_civil_chambers",
                "case_year_esas",
                "case_start_seq_esas",
                "case_end_seq_esas",
                "decision_year_karar",
                "decision_start_seq_karar",
                "decision_end_seq_karar",
                "sort_criteria",
                "sort_direction",
            ),
        )
        return args

    def doc_ref_from_row(self, row: dict[str, Any]) -> DocRef:
        document_id = _text(row.get("id"))
        if not document_id:
            raise ValueError("id eksik")
        return DocRef(tool=self.doc_tool, args={"id": document_id}, chunked=False)

    def _normalize_row(self, row: dict[str, Any]) -> NormalizedDecision:
        doc_ref = self.doc_ref_from_row(row)
        court = _text(row.get("daire")) or "Emsal"
        esas_no = _text(row.get("esasNo"))
        karar_no = _text(row.get("kararNo"))
        return _decision(
            self.id,
            doc_ref,
            title=f"{court} · E. {esas_no or '-'} K. {karar_no or '-'}",
            court=court,
            esas_no=esas_no,
            karar_no=karar_no,
            date_value=row.get("kararTarihi"),
            snippet=_text(row.get("arananKelime")),
            source_url=_text(row.get("document_url")),
        )

    def normalize(self, payload: Any, page: int) -> SearchOutcome:
        return self._normalize_rows(payload, page, self._normalize_row)


class UyusmazlikAdapter(Adapter):
    id = "uyusmazlik"
    name = "Uyuşmazlık Mahkemesi"
    search_tool = "search_uyusmazlik_decisions"
    doc_tool = "get_uyusmazlik_document_markdown_from_url"
    doc_chunked = False

    def build_search_args(self, p: SearchParams) -> dict[str, Any]:
        return {
            "icerik": p.phrase,
            "search_scope": p.extra.get("search_scope", "All"),
            "case_sensitive": bool(p.extra.get("case_sensitive", False)),
            "page_number": p.page,
        }

    def doc_ref_from_row(self, row: dict[str, Any]) -> DocRef:
        document_url = _text(row.get("document_url"))
        if not document_url:
            raise ValueError("document_url eksik")
        return DocRef(
            tool=self.doc_tool,
            args={"document_url": document_url},
            chunked=False,
        )

    def _normalize_row(self, row: dict[str, Any]) -> NormalizedDecision:
        doc_ref = self.doc_ref_from_row(row)
        esas_no = _text(row.get("esas_sayisi"))
        karar_no = _text(row.get("karar_sayisi"))
        return _decision(
            self.id,
            doc_ref,
            title=f"Uyuşmazlık Mahkemesi · E. {esas_no or '-'} K. {karar_no or '-'}",
            court="Uyuşmazlık Mahkemesi",
            esas_no=esas_no,
            karar_no=karar_no,
            date_value=row.get("karar_tarihi"),
            source_url=_text(row.get("document_url")),
        )

    def normalize(self, payload: Any, page: int) -> SearchOutcome:
        return self._normalize_rows(payload, page, self._normalize_row)


class KikAdapter(Adapter):
    id = "kik"
    name = "Kamu İhale Kurulu"
    search_tool = "search_kik_v2_decisions"
    doc_tool = "get_kik_v2_document_markdown"
    doc_chunked = False

    def build_search_args(self, p: SearchParams) -> dict[str, Any]:
        args: dict[str, Any] = {
            "decision_type": p.extra.get("decision_type", "uyusmazlik"),
            "karar_metni": p.phrase,
        }
        _copy_extra(args, p.extra, ("karar_no", "basvuran", "idare_adi"))
        if date_from := _format_date(p.date_from, dotted=False):
            args["baslangic_tarihi"] = date_from
        if date_to := _format_date(p.date_to, dotted=False):
            args["bitis_tarihi"] = date_to
        return args

    def doc_ref_from_row(self, row: dict[str, Any]) -> DocRef:
        document_id = _text(row.get("gundemMaddesiId"))
        if not document_id:
            raise ValueError("gundemMaddesiId eksik")
        return DocRef(
            tool=self.doc_tool,
            args={"gundemMaddesiId": document_id},
            chunked=False,
        )

    def _normalize_row(self, row: dict[str, Any]) -> NormalizedDecision:
        doc_ref = self.doc_ref_from_row(row)
        karar_no = _text(row.get("kararNo"))
        return _decision(
            self.id,
            doc_ref,
            title=f"Kamu İhale Kurulu · {karar_no or 'Karar'}",
            court="Kamu İhale Kurulu",
            karar_no=karar_no,
            date_value=row.get("kararTarihi"),
            snippet=_text(row.get("basvuruKonusu")),
            extra={
                "decision_type": row.get("decision_type"),
                "basvuran": row.get("basvuran"),
                "idare_adi": row.get("idareAdi"),
            },
        )

    def normalize(self, payload: Any, page: int) -> SearchOutcome:
        return self._normalize_rows(payload, page, self._normalize_row)


class RekabetAdapter(Adapter):
    id = "rekabet"
    name = "Rekabet Kurumu"
    search_tool = "search_rekabet_kurumu_decisions"
    doc_tool = "get_rekabet_kurumu_document"
    doc_chunked = True

    def build_search_args(self, p: SearchParams) -> dict[str, Any]:
        args: dict[str, Any] = {
            "PdfText": p.phrase,
            "KararTuru": p.extra.get("KararTuru", "ALL"),
            "page": p.page,
        }
        _copy_extra(
            args,
            p.extra,
            ("sayfaAdi", "YayinlanmaTarihi", "KararSayisi", "KararTarihi"),
        )
        return args

    def doc_ref_from_row(self, row: dict[str, Any]) -> DocRef:
        karar_id = _text(row.get("karar_id"))
        if not karar_id or karar_id == "UNKNOWN_KARAR_ID":
            raise ValueError("karar_id geçersiz")
        return DocRef(
            tool=self.doc_tool,
            args={"karar_id": karar_id},
            chunked=True,
        )

    def _normalize_row(self, row: dict[str, Any]) -> NormalizedDecision:
        doc_ref = self.doc_ref_from_row(row)
        karar_no = _text(row.get("decision_number"))
        title = _text(row.get("title")) or f"Rekabet Kurumu · {karar_no or 'Karar'}"
        return _decision(
            self.id,
            doc_ref,
            title=title,
            court="Rekabet Kurumu",
            karar_no=karar_no,
            date_value=row.get("decision_date"),
            snippet=_text(row.get("decision_type_text")),
            source_url=_text(row.get("decision_url")),
            extra={"publication_date": row.get("publication_date")},
        )

    def normalize(self, payload: Any, page: int) -> SearchOutcome:
        data, error_outcome = _normalizable(payload)
        if error_outcome is not None:
            return error_outcome
        if data is None:
            return SearchOutcome(status="source_error", error="kaynak hatası")
        rows = data.get("decisions", [])
        if isinstance(rows, list):
            pagination_row_count = len(rows)
            openable_rows: list[Any] = []
            filtered_unknown_count = 0
            for row in rows:
                try:
                    if _as_dict(row).get("karar_id") == "UNKNOWN_KARAR_ID":
                        filtered_unknown_count += 1
                    else:
                        openable_rows.append(row)
                except TypeError:
                    openable_rows.append(row)
            rows = openable_rows
            return _finish(
                self.id,
                data,
                rows,
                page,
                self._normalize_row,
                pagination_row_count=pagination_row_count,
                allow_filtered_empty=filtered_unknown_count == pagination_row_count,
            )
        return _finish(self.id, data, rows, page, self._normalize_row)


class SayistayAdapter(Adapter):
    id = "sayistay"
    name = "Sayıştay"
    search_tool = "search_sayistay_unified"
    doc_tool = "get_sayistay_document_unified"
    doc_chunked = False

    _TYPE_TEXT_KEYS = {
        "genel_kurul": "karar_tamami",
        "temyiz_kurulu": "temyiz_karar",
        "daire": "web_karar_metni",
    }

    def build_search_args(self, p: SearchParams) -> dict[str, Any]:
        decision_type = p.extra.get("decision_type", "genel_kurul")
        args: dict[str, Any] = {
            "decision_type": decision_type,
            "start": (p.page - 1) * 10,
            "length": 10,
            self._TYPE_TEXT_KEYS.get(decision_type, "karar_tamami"): p.phrase,
        }
        if date_from := _format_date(p.date_from, dotted=True):
            args["karar_tarih_baslangic"] = date_from
        if date_to := _format_date(p.date_to, dotted=True):
            args["karar_tarih_bitis"] = date_to
        _copy_extra(
            args,
            p.extra,
            (
                "kamu_idaresi_turu",
                "ilam_no",
                "web_karar_konusu",
                "karar_no",
                "karar_ek",
                "karar_tamami",
                "ilam_dairesi",
                "yili",
                "dosya_no",
                "temyiz_tutanak_no",
                "temyiz_karar",
                "yargilama_dairesi",
                "hesap_yili",
                "web_karar_metni",
            ),
        )
        return args

    def doc_ref_from_row(self, row: dict[str, Any]) -> DocRef:
        decision_id = _text(row.get("id"))
        decision_type = _text(row.get("_decision_type") or row.get("decision_type"))
        if decision_type is None:
            if "karar_ozeti" in row:
                decision_type = "genel_kurul"
            elif "temyiz_karar" in row:
                decision_type = "temyiz_kurulu"
            elif "web_karar_metni" in row:
                decision_type = "daire"
        if not decision_id or not decision_type:
            raise ValueError("decision_id veya decision_type eksik")
        return DocRef(
            tool=self.doc_tool,
            args={"decision_id": decision_id, "decision_type": decision_type},
            chunked=False,
        )

    def _normalize_row(
        self, source_row: dict[str, Any], decision_type: str
    ) -> NormalizedDecision:
        row = {**source_row, "_decision_type": decision_type}
        doc_ref = self.doc_ref_from_row(row)
        if decision_type == "genel_kurul":
            karar_no = _text(row.get("karar_no"))
            return _decision(
                self.id,
                doc_ref,
                title=f"Sayıştay Genel Kurulu · {karar_no or row.get('id')}",
                court="Sayıştay Genel Kurulu",
                karar_no=karar_no,
                date_value=row.get("karar_tarih"),
                snippet=_text(row.get("karar_ozeti")),
                extra={"decision_type": decision_type},
            )
        if decision_type == "temyiz_kurulu":
            chamber = _text(row.get("ilam_dairesi"))
            return _decision(
                self.id,
                doc_ref,
                title=f"Sayıştay Temyiz Kurulu · {row.get('id')}",
                court=f"Sayıştay {chamber}. Daire" if chamber else "Sayıştay Temyiz Kurulu",
                date_value=row.get("temyiz_tutanak_tarihi"),
                snippet=_text(row.get("temyiz_karar")),
                extra={"decision_type": decision_type},
            )

        chamber = _text(row.get("yargilama_dairesi"))
        karar_no = _text(row.get("karar_no"))
        return _decision(
            self.id,
            doc_ref,
            title=f"Sayıştay {chamber + '. Daire' if chamber else 'Daire'} · {karar_no or row.get('id')}",
            court=f"Sayıştay {chamber}. Daire" if chamber else "Sayıştay",
            esas_no=_text(row.get("ilam_no")),
            karar_no=karar_no,
            date_value=row.get("karar_tarih"),
            snippet=_text(row.get("web_karar_metni")),
            extra={"decision_type": decision_type},
        )

    def normalize(self, payload: Any, page: int) -> SearchOutcome:
        data, error_outcome = _normalizable(payload)
        if error_outcome is not None:
            return error_outcome
        if data is None:
            return SearchOutcome(status="source_error", error="kaynak hatası")
        decision_type = _text(data.get("decision_type")) or "genel_kurul"
        return _finish(
            self.id,
            data,
            data.get("decisions", []),
            page,
            lambda row: self._normalize_row(row, decision_type),
        )


class KvkkAdapter(Adapter):
    id = "kvkk"
    name = "KVKK"
    search_tool = "search_kvkk_decisions"
    doc_tool = "get_kvkk_document_markdown"
    doc_chunked = True

    def build_search_args(self, p: SearchParams) -> dict[str, Any]:
        return {"keywords": p.phrase, "page": p.page}

    def doc_ref_from_row(self, row: dict[str, Any]) -> DocRef:
        decision_url = _text(row.get("url"))
        if not decision_url:
            raise ValueError("url eksik")
        return DocRef(
            tool=self.doc_tool,
            args={"decision_url": decision_url},
            chunked=True,
        )

    def _normalize_row(self, row: dict[str, Any]) -> NormalizedDecision:
        doc_ref = self.doc_ref_from_row(row)
        karar_no = _text(row.get("decision_number"))
        return _decision(
            self.id,
            doc_ref,
            title=_text(row.get("title")) or f"KVKK · {karar_no or 'Karar'}",
            court="Kişisel Verileri Koruma Kurulu",
            karar_no=karar_no,
            date_value=row.get("publication_date"),
            snippet=_text(row.get("description")),
            source_url=_text(row.get("url")),
            extra={"decision_id": row.get("decision_id")},
        )

    def normalize(self, payload: Any, page: int) -> SearchOutcome:
        return self._normalize_rows(
            payload,
            page,
            self._normalize_row,
            empty_error="Sonuç bulunamadı — kaynak geçici olarak erişilemiyor olabilir.",
        )


class BddkAdapter(Adapter):
    id = "bddk"
    name = "BDDK"
    search_tool = "search_bddk_decisions"
    doc_tool = "get_bddk_document_markdown"
    doc_chunked = True

    def build_search_args(self, p: SearchParams) -> dict[str, Any]:
        return {"keywords": p.phrase, "page": p.page}

    def doc_ref_from_row(self, row: dict[str, Any]) -> DocRef:
        document_id = _text(row.get("document_id"))
        if not document_id:
            raise ValueError("document_id eksik")
        return DocRef(
            tool=self.doc_tool,
            args={"document_id": document_id},
            chunked=True,
        )

    def _normalize_row(self, row: dict[str, Any]) -> NormalizedDecision:
        doc_ref = self.doc_ref_from_row(row)
        return _decision(
            self.id,
            doc_ref,
            title=_text(row.get("title")) or "BDDK Kararı",
            court="Bankacılık Düzenleme ve Denetleme Kurumu",
            snippet=_text(row.get("content")),
        )

    def normalize(self, payload: Any, page: int) -> SearchOutcome:
        return self._normalize_rows(payload, page, self._normalize_row)


class BtkAdapter(Adapter):
    id = "btk"
    name = "BTK"
    search_tool = "search_btk_decisions"
    doc_tool = "get_btk_document_markdown"
    doc_chunked = True

    def build_search_args(self, p: SearchParams) -> dict[str, Any]:
        args: dict[str, Any] = {"keywords": p.phrase, "page": p.page, "pageSize": 10}
        _copy_extra(args, p.extra, ("decision_no", "relevant_unit"))
        for key in ("decision_date", "publication_date"):
            if key in p.extra and p.extra[key]:
                args[key] = _format_date(str(p.extra[key]), dotted=False)
        return args

    def doc_ref_from_row(self, row: dict[str, Any]) -> DocRef:
        pdf_url = _text(row.get("pdf_url"))
        if not pdf_url:
            raise ValueError("pdf_url eksik")
        return DocRef(tool=self.doc_tool, args={"pdf_url": pdf_url}, chunked=True)

    def _normalize_row(self, row: dict[str, Any]) -> NormalizedDecision:
        doc_ref = self.doc_ref_from_row(row)
        return _decision(
            self.id,
            doc_ref,
            title=_text(row.get("title")) or "BTK Kararı",
            court="Bilgi Teknolojileri ve İletişim Kurumu",
            karar_no=_text(row.get("decision_no")),
            date_value=row.get("decision_date") or row.get("publication_date"),
            source_url=_text(row.get("pdf_url")),
            extra={
                "id": row.get("id"),
                "publication_date": row.get("publication_date"),
                "relevant_unit": row.get("relevant_unit"),
            },
        )

    def normalize(self, payload: Any, page: int) -> SearchOutcome:
        return self._normalize_rows(payload, page, self._normalize_row)


class GibAdapter(Adapter):
    id = "gib"
    name = "Gelir İdaresi"
    search_tool = "search_gib_ozelge"
    doc_tool = "get_gib_ozelge_document_markdown"
    doc_chunked = True

    def build_search_args(self, p: SearchParams) -> dict[str, Any]:
        args: dict[str, Any] = {"keywords": p.phrase, "page": p.page, "pageSize": 10}
        _copy_extra(args, p.extra, ("kanunNo", "ozelgeNo"))
        if date_from := _format_date(p.date_from, dotted=False):
            args["ozelgeStartDate"] = date_from
        if date_to := _format_date(p.date_to, dotted=False):
            args["ozelgeEndDate"] = date_to
        return args

    def doc_ref_from_row(self, row: dict[str, Any]) -> DocRef:
        try:
            ozelge_id = int(row["id"])
        except (KeyError, TypeError, ValueError) as error:
            raise ValueError("ozelge id geçersiz") from error
        return DocRef(tool=self.doc_tool, args={"ozelge_id": ozelge_id}, chunked=True)

    def _normalize_row(self, row: dict[str, Any]) -> NormalizedDecision:
        doc_ref = self.doc_ref_from_row(row)
        karar_no = _text(row.get("ozelgeNo"))
        return _decision(
            self.id,
            doc_ref,
            title=_text(row.get("title")) or f"GİB Özelgesi · {karar_no or row.get('id')}",
            court="Gelir İdaresi Başkanlığı",
            karar_no=karar_no,
            date_value=row.get("ozelgeTarih"),
            snippet=_text(row.get("kanunTitle")),
            source_url=_text(row.get("siteLink")),
            extra={"kanun_no": row.get("kanunNo"), "kanun_title": row.get("kanunTitle")},
        )

    def normalize(self, payload: Any, page: int) -> SearchOutcome:
        return self._normalize_rows(payload, page, self._normalize_row, rows_key="ozelgeler")


class SigortaAdapter(Adapter):
    id = "sigorta"
    name = "Sigorta Tahkim"
    search_tool = "search_sigorta_tahkim_decisions"
    doc_tool = "get_sigorta_tahkim_document_markdown"
    doc_chunked = True

    def build_search_args(self, p: SearchParams) -> dict[str, Any]:
        return {"keywords": p.phrase, "page": p.page}

    def doc_ref_from_row(self, row: dict[str, Any]) -> DocRef:
        try:
            issue_number = int(row["document_id"])
        except (KeyError, TypeError, ValueError) as error:
            raise ValueError("issue_number geçersiz") from error
        return DocRef(
            tool=self.doc_tool,
            args={"issue_number": issue_number},
            chunked=True,
        )

    def _normalize_row(self, row: dict[str, Any]) -> NormalizedDecision:
        doc_ref = self.doc_ref_from_row(row)
        return _decision(
            self.id,
            doc_ref,
            title=_text(row.get("title")) or f"Sigorta Tahkim Dergisi · {row.get('document_id')}",
            court="Sigorta Tahkim Komisyonu",
            snippet=_text(row.get("content")),
            source_url=_text(row.get("url")),
            extra={"issue_number": doc_ref.args["issue_number"]},
        )

    def normalize(self, payload: Any, page: int) -> SearchOutcome:
        return self._normalize_rows(payload, page, self._normalize_row)


ADAPTERS: dict[str, Adapter] = {
    adapter.id: adapter
    for adapter in (
        BedestenAdapter(),
        AnayasaAdapter(),
        EmsalAdapter(),
        KikAdapter(),
        RekabetAdapter(),
        SayistayAdapter(),
        KvkkAdapter(),
        BddkAdapter(),
        BtkAdapter(),
        GibAdapter(),
        UyusmazlikAdapter(),
        SigortaAdapter(),
    )
}

tool_to_db: dict[str, str] = {
    adapter.search_tool: adapter.id for adapter in ADAPTERS.values()
}

SOURCE_META: list[dict[str, Any]] = [
    {
        "id": "bedesten",
        "name": "Bedesten",
        "sub": "Yargıtay · Danıştay",
        "desc": "Yargıtay, Danıştay, yerel ve istinaf mahkemeleri ile Kanun Yararına Bozma kararları.",
        "color": "#8A2A33",
        "gated": False,
    },
    {
        "id": "anayasa",
        "name": "Anayasa Mahkemesi",
        "sub": "AYM",
        "desc": "Norm denetimi ve bireysel başvuru kararları.",
        "color": "#3B4B8C",
        "gated": False,
    },
    {
        "id": "emsal",
        "name": "Emsal",
        "sub": "UYAP",
        "desc": "İlk derece ve bölge adliye mahkemesi emsal kararları.",
        "color": "#2E6E6A",
        "gated": False,
    },
    {
        "id": "kik",
        "name": "Kamu İhale Kurulu",
        "sub": "KİK",
        "desc": "İhale uyuşmazlığı, düzenleyici ve mahkeme kararları.",
        "color": "#8A5A1E",
        "gated": False,
    },
    {
        "id": "rekabet",
        "name": "Rekabet Kurumu",
        "sub": "RK",
        "desc": "Birleşme/devralma, ihlal ve muafiyet kararları.",
        "color": "#3F7A55",
        "gated": False,
    },
    {
        "id": "sayistay",
        "name": "Sayıştay",
        "sub": "",
        "desc": "Genel kurul, temyiz ve daire kararları.",
        "color": "#556072",
        "gated": False,
    },
    {
        "id": "kvkk",
        "name": "KVKK",
        "sub": "",
        "desc": "Kişisel verilerin korunması kurul kararları.",
        "color": "#6B3FA0",
        "gated": False,
    },
    {
        "id": "bddk",
        "name": "BDDK",
        "sub": "",
        "desc": "Bankacılık düzenleme ve denetleme kararları.",
        "color": "#2A5B8A",
        "gated": True,
    },
    {
        "id": "btk",
        "name": "BTK",
        "sub": "",
        "desc": "Bilgi teknolojileri ve iletişim kurumu kararları.",
        "color": "#1E7A8A",
        "gated": False,
    },
    {
        "id": "gib",
        "name": "Gelir İdaresi",
        "sub": "Özelge",
        "desc": "Vergi özelgeleri ve muktezalar.",
        "color": "#8A6A2A",
        "gated": False,
    },
    {
        "id": "uyusmazlik",
        "name": "Uyuşmazlık Mahkemesi",
        "sub": "",
        "desc": "Görev ve hüküm uyuşmazlığı kararları.",
        "color": "#A03D5E",
        "gated": False,
    },
    {
        "id": "sigorta",
        "name": "Sigorta Tahkim",
        "sub": "",
        "desc": "Sigorta tahkim komisyonu hakem kararları.",
        "color": "#4A6E2E",
        "gated": True,
    },
]


def _option(value: str, label: str, group: str | None = None) -> dict[str, str]:
    option = {"value": value, "label": label}
    if group:
        option["group"] = group
    return option


BEDESTEN_BIRIM_OPTIONS = [
    _option("ALL", "Tümü"),
    *[
        _option(f"H{number}", f"{number}. Hukuk Dairesi", "Yargıtay Hukuk")
        for number in range(1, 24)
    ],
    *[
        _option(f"C{number}", f"{number}. Ceza Dairesi", "Yargıtay Ceza")
        for number in range(1, 24)
    ],
    _option("HGK", "Hukuk Genel Kurulu", "Yargıtay Kurulları"),
    _option("CGK", "Ceza Genel Kurulu", "Yargıtay Kurulları"),
    _option("BGK", "Büyük Genel Kurulu", "Yargıtay Kurulları"),
    _option("HBK", "Hukuk Daireleri Başkanlar Kurulu", "Yargıtay Kurulları"),
    _option("CBK", "Ceza Daireleri Başkanlar Kurulu", "Yargıtay Kurulları"),
    *[
        _option(f"D{number}", f"{number}. Daire", "Danıştay")
        for number in range(1, 18)
    ],
    _option("DBGK", "Büyük Genel Kurul", "Danıştay Kurulları"),
    _option("IDDK", "İdari Dava Daireleri Kurulu", "Danıştay Kurulları"),
    _option("VDDK", "Vergi Dava Daireleri Kurulu", "Danıştay Kurulları"),
    _option("IBK", "İçtihatları Birleştirme Kurulu", "Danıştay Kurulları"),
    _option("IIK", "İdari İşler Kurulu", "Danıştay Kurulları"),
    _option("DBK", "Başkanlar Kurulu", "Danıştay Kurulları"),
]

FORM_METADATA: dict[str, dict[str, Any]] = {
    "bedesten": {
        "court_types": [
            _option("YARGITAYKARARI", "Yargıtay"),
            _option("DANISTAYKARAR", "Danıştay"),
            _option("YERELHUKUK", "Yerel Mahkeme"),
            _option("ISTINAFHUKUK", "İstinaf (BAM)"),
            _option("KYB", "Kanun Yararına Bozma"),
        ],
        "birim_options": BEDESTEN_BIRIM_OPTIONS,
    },
    "anayasa": {
        "decision_types": [
            _option("norm_denetimi", "Norm Denetimi"),
            _option("bireysel_basvuru", "Bireysel Başvuru"),
        ]
    },
    "kik": {
        "decision_types": [
            _option("uyusmazlik", "Uyuşmazlık"),
            _option("duzenleyici", "Düzenleyici"),
            _option("mahkeme", "Mahkeme"),
        ]
    },
    "sayistay": {
        "decision_types": [
            _option("genel_kurul", "Genel Kurul"),
            _option("temyiz_kurulu", "Temyiz Kurulu"),
            _option("daire", "Daire"),
        ]
    },
    "rekabet": {
        "karar_turu_options": [
            _option("ALL", "Tümü"),
            _option("Birleşme ve Devralma", "Birleşme ve Devralma"),
            _option("Diğer", "Diğer"),
            _option("Menfi Tespit ve Muafiyet", "Menfi Tespit ve Muafiyet"),
            _option("Özelleştirme", "Özelleştirme"),
            _option("Rekabet İhlali", "Rekabet İhlali"),
        ]
    },
}

_registry_availability: dict[str, bool] = {db_id: True for db_id in ADAPTERS}


def validate_registry(mcp_bridge: Any) -> None:
    tool_names = mcp_bridge.tool_names()
    for db_id, adapter in ADAPTERS.items():
        missing = {adapter.search_tool, adapter.doc_tool} - tool_names
        _registry_availability[db_id] = not missing
        if missing:
            logger.error("%s kullanılamıyor; eksik MCP araçları: %s", db_id, sorted(missing))
        elif db_id in {"bddk", "sigorta"} and not os.environ.get(
            "TAVILY_API_KEY", ""
        ).strip():
            logger.warning("%s kullanılamıyor; TAVILY_API_KEY yok", db_id)
        else:
            logger.info("%s kullanılabilir", db_id)


def available(db_id: str) -> bool:
    if db_id not in ADAPTERS or not _registry_availability.get(db_id, False):
        return False
    if db_id in {"bddk", "sigorta"}:
        return bool(os.environ.get("TAVILY_API_KEY", "").strip())
    return True
