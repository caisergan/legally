from __future__ import annotations

import asyncio
import json
from datetime import date, datetime, timezone
from typing import Annotated, Any

from fastapi import APIRouter, Depends, HTTPException, Request, Response, status
from pydantic import BaseModel, ConfigDict, Field
from sqlalchemy import delete, select
from sqlalchemy.ext.asyncio import AsyncSession

from ..db import get_db
from ..models import (
    AuthSession,
    Bookmark,
    Conversation,
    DocumentView,
    Message,
    Search,
    SearchResultSnapshot,
    SigningApproval,
    SigningArtifact,
    SigningCertificateAssignment,
    SigningDownloadAuthorization,
    SigningOutbox,
    SigningRequest,
    SigningRequestEvent,
    UsageDaily,
    User,
    utcnow,
)
from ..security import SESSION_COOKIE, get_current_user, verify_password

router = APIRouter(prefix="/account", tags=["account"])

Database = Annotated[AsyncSession, Depends(get_db)]
CurrentUser = Annotated[User, Depends(get_current_user)]


class AccountDeleteRequest(BaseModel):
    model_config = ConfigDict(extra="forbid")

    password: str = Field(min_length=1, max_length=1024)


def _iso(value: datetime | date | None) -> str | None:
    if value is None:
        return None
    if isinstance(value, datetime):
        normalized = value if value.tzinfo else value.replace(tzinfo=timezone.utc)
        return normalized.astimezone(timezone.utc).isoformat().replace("+00:00", "Z")
    return value.isoformat()


def _load_json(value: str | None, fallback: Any) -> Any:
    if value is None:
        return fallback
    try:
        return json.loads(value)
    except (json.JSONDecodeError, TypeError):
        return value


@router.get("/export")
async def export_account(user: CurrentUser, db: Database) -> dict[str, Any]:
    sessions = (
        await db.scalars(
            select(AuthSession)
            .where(AuthSession.user_id == user.id)
            .order_by(AuthSession.created_at)
        )
    ).all()
    conversations = (
        await db.scalars(
            select(Conversation)
            .where(Conversation.user_id == user.id)
            .order_by(Conversation.created_at)
        )
    ).all()
    conversation_ids = [conversation.id for conversation in conversations]
    messages = []
    if conversation_ids:
        messages = (
            await db.scalars(
                select(Message)
                .where(Message.conversation_id.in_(conversation_ids))
                .order_by(Message.created_at)
            )
        ).all()
    searches = (
        await db.scalars(
            select(Search)
            .where(Search.user_id == user.id)
            .order_by(Search.created_at)
        )
    ).all()
    search_ids = [search.id for search in searches]
    snapshots = []
    if search_ids:
        snapshots = (
            await db.scalars(
                select(SearchResultSnapshot)
                .where(SearchResultSnapshot.search_id.in_(search_ids))
                .order_by(SearchResultSnapshot.search_id, SearchResultSnapshot.rank)
            )
        ).all()
    document_views = (
        await db.scalars(
            select(DocumentView)
            .where(DocumentView.user_id == user.id)
            .order_by(DocumentView.opened_at)
        )
    ).all()
    bookmarks = (
        await db.scalars(
            select(Bookmark)
            .where(Bookmark.user_id == user.id)
            .order_by(Bookmark.created_at)
        )
    ).all()
    usage_rows = (
        await db.scalars(
            select(UsageDaily)
            .where(UsageDaily.user_id == user.id)
            .order_by(UsageDaily.day)
        )
    ).all()
    signing_requests = (
        await db.scalars(
            select(SigningRequest)
            .where(SigningRequest.owner_id == user.id)
            .order_by(SigningRequest.created_at)
        )
    ).all()
    signing_artifacts = (
        await db.scalars(
            select(SigningArtifact)
            .where(SigningArtifact.owner_id == user.id)
            .order_by(SigningArtifact.created_at)
        )
    ).all()

    return {
        "exported_at": _iso(utcnow()),
        "user": {
            "id": user.id,
            "email": user.email,
            "display_name": user.display_name,
            "role": user.role,
            "locale": user.locale,
            "theme": user.theme,
            "preferred_model": user.preferred_model,
            "created_at": _iso(user.created_at),
            "last_login_at": _iso(user.last_login_at),
        },
        "sessions": [
            {
                "token_prefix": session.token_hash[:12],
                "user_agent": session.user_agent,
                "created_at": _iso(session.created_at),
                "expires_at": _iso(session.expires_at),
                "last_seen_at": _iso(session.last_seen_at),
            }
            for session in sessions
        ],
        "conversations": [
            {
                "id": conversation.id,
                "title": conversation.title,
                "created_at": _iso(conversation.created_at),
                "updated_at": _iso(conversation.updated_at),
                "archived_at": _iso(conversation.archived_at),
            }
            for conversation in conversations
        ],
        "messages": [
            {
                "id": message.id,
                "conversation_id": message.conversation_id,
                "role": message.role,
                "content": message.content,
                "status": message.status,
                "tool_calls": _load_json(message.tool_calls_json, []),
                "citations": _load_json(message.citations_json, []),
                "model": message.model,
                "input_tokens": message.input_tokens,
                "output_tokens": message.output_tokens,
                "created_at": _iso(message.created_at),
            }
            for message in messages
        ],
        "searches": [
            {
                "id": search.id,
                "origin": search.origin,
                "conversation_id": search.conversation_id,
                "source_db": search.source_db,
                "tool_name": search.tool_name,
                "params": _load_json(search.params_json, {}),
                "query_text": search.query_text,
                "result_count": search.result_count,
                "status": search.status,
                "error_text": search.error_text,
                "duration_ms": search.duration_ms,
                "created_at": _iso(search.created_at),
            }
            for search in searches
        ],
        "search_results_snapshot": [
            {
                "search_id": snapshot.search_id,
                "rank": snapshot.rank,
                "normalized": _load_json(snapshot.normalized_json, {}),
            }
            for snapshot in snapshots
        ],
        "document_views": [
            {
                "id": view.id,
                "source_db": view.source_db,
                "doc_key": view.doc_key,
                "title": view.title,
                "meta": _load_json(view.meta_json, {}),
                "opened_at": _iso(view.opened_at),
            }
            for view in document_views
        ],
        "bookmarks": [
            {
                "id": bookmark.id,
                "source_db": bookmark.source_db,
                "doc_key": bookmark.doc_key,
                "doc_ref": _load_json(bookmark.doc_ref_json, {}),
                "title": bookmark.title,
                "meta": _load_json(bookmark.meta_json, {}),
                "note": bookmark.note,
                "tags": _load_json(bookmark.tags_json, []),
                "created_at": _iso(bookmark.created_at),
            }
            for bookmark in bookmarks
        ],
        "usage_daily": [
            {
                "day": _iso(usage.day),
                "llm_input_tokens": usage.llm_input_tokens,
                "llm_output_tokens": usage.llm_output_tokens,
                "tool_calls": usage.tool_calls,
            }
            for usage in usage_rows
        ],
        # Safe signing metadata only. No document bytes, PIN, capability, or path.
        "signing_requests": [
            {
                "id": request.id,
                "state": request.state,
                "profile": request.profile,
                "algorithm": request.algorithm,
                "policy_version": request.policy_version,
                "certificate_fingerprint_sha256": request.certificate_fingerprint_sha256,
                "input_sha256": request.input_sha256,
                "input_byte_count": request.input_byte_count,
                "output_sha256": request.output_sha256,
                "output_byte_count": request.output_byte_count,
                "failure_code": request.failure_code,
                "created_at": _iso(request.created_at),
                "queued_at": _iso(request.queued_at),
                "completed_at": _iso(request.completed_at),
            }
            for request in signing_requests
        ],
        "signing_artifacts": [
            {
                "id": artifact.id,
                "kind": artifact.kind,
                "display_filename": artifact.display_filename,
                "mime_type": artifact.mime_type,
                "byte_count": artifact.byte_count,
                "sha256": artifact.sha256,
                "created_at": _iso(artifact.created_at),
                "expires_at": _iso(artifact.expires_at),
            }
            for artifact in signing_artifacts
        ],
    }


@router.post("/delete-history")
async def delete_history(user: CurrentUser, db: Database) -> dict[str, Any]:
    user_search_ids = select(Search.id).where(Search.user_id == user.id)
    user_conversation_ids = select(Conversation.id).where(
        Conversation.user_id == user.id
    )
    snapshot_result = await db.execute(
        delete(SearchResultSnapshot).where(
            SearchResultSnapshot.search_id.in_(user_search_ids)
        )
    )
    message_result = await db.execute(
        delete(Message).where(Message.conversation_id.in_(user_conversation_ids))
    )
    search_result = await db.execute(delete(Search).where(Search.user_id == user.id))
    view_result = await db.execute(
        delete(DocumentView).where(DocumentView.user_id == user.id)
    )
    conversation_result = await db.execute(
        delete(Conversation).where(Conversation.user_id == user.id)
    )
    await db.commit()
    return {
        "deleted": {
            "search_results_snapshot": snapshot_result.rowcount,
            "messages": message_result.rowcount,
            "searches": search_result.rowcount,
            "document_views": view_result.rowcount,
            "conversations": conversation_result.rowcount,
        }
    }


@router.post("/delete", status_code=status.HTTP_204_NO_CONTENT)
async def delete_account(
    payload: AccountDeleteRequest,
    request: Request,
    user: CurrentUser,
    db: Database,
) -> Response:
    password_matches = await asyncio.to_thread(
        verify_password,
        user.password_hash,
        payload.password,
    )
    if not password_matches:
        raise HTTPException(status_code=400, detail="Şifre hatalı")

    # Retained completed signing evidence must never be cascade-deleted (§9.1).
    retained = await db.scalar(
        select(SigningRequest.id)
        .where(SigningRequest.owner_id == user.id, SigningRequest.retained == True)  # noqa: E712
        .limit(1)
    )
    retained_output = await db.scalar(
        select(SigningArtifact.id)
        .where(SigningArtifact.owner_id == user.id, SigningArtifact.retained == True)  # noqa: E712
        .limit(1)
    )
    if retained is not None or retained_output is not None:
        raise HTTPException(
            status_code=409,
            detail="Saklanan imza kayıtları bulunduğu için hesap silinemiyor.",
        )

    # Remove non-retained signing rows (owner FKs use RESTRICT, so purge first).
    user_request_ids = select(SigningRequest.id).where(SigningRequest.owner_id == user.id)
    await db.execute(
        delete(SigningRequestEvent).where(
            SigningRequestEvent.request_id.in_(user_request_ids)
        )
    )
    await db.execute(
        delete(SigningOutbox).where(SigningOutbox.request_id.in_(user_request_ids))
    )
    await db.execute(
        delete(SigningApproval).where(SigningApproval.owner_id == user.id)
    )
    await db.execute(
        delete(SigningDownloadAuthorization).where(
            SigningDownloadAuthorization.owner_id == user.id
        )
    )
    await db.execute(delete(SigningRequest).where(SigningRequest.owner_id == user.id))
    await db.execute(
        delete(SigningCertificateAssignment).where(
            SigningCertificateAssignment.user_id == user.id
        )
    )
    await db.execute(delete(SigningArtifact).where(SigningArtifact.owner_id == user.id))

    await db.delete(user)
    await db.commit()
    response = Response(status_code=status.HTTP_204_NO_CONTENT)
    response.delete_cookie(
        key=SESSION_COOKIE,
        httponly=True,
        secure=request.url.scheme == "https",
        samesite="lax",
        path="/",
    )
    return response
