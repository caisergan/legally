from __future__ import annotations

import json
from typing import Annotated, Any

from fastapi import APIRouter, Depends, HTTPException, Response, status
from fastapi.responses import JSONResponse, StreamingResponse
from pydantic import BaseModel, ConfigDict, Field, field_validator
from sqlalchemy import select
from sqlalchemy.ext.asyncio import AsyncSession

from ..chat import (
    GenerationAlreadyRunning,
    release_generation,
    request_stop,
    reserve_generation,
    start_chat_stream,
)
from ..config import settings
from ..db import get_db
from ..models import Conversation, Message, User, utcnow
from ..quotas import check_and_touch
from ..security import get_current_user

router = APIRouter(prefix="/conversations", tags=["conversations"])

Database = Annotated[AsyncSession, Depends(get_db)]
CurrentUser = Annotated[User, Depends(get_current_user)]


class RequestModel(BaseModel):
    model_config = ConfigDict(extra="forbid")


class CreateConversationRequest(RequestModel):
    pass


class ConversationUpdateRequest(RequestModel):
    title: str | None = Field(default=None, max_length=200)
    archived: bool | None = None

    @field_validator("title")
    @classmethod
    def normalize_title(cls, value: str | None) -> str | None:
        if value is None:
            return None
        return value.strip() or None


class MessageCreateRequest(RequestModel):
    content: str = Field(min_length=1, max_length=50_000)

    @field_validator("content")
    @classmethod
    def normalize_content(cls, value: str) -> str:
        normalized = value.strip()
        if not normalized:
            raise ValueError("Mesaj boş olamaz")
        return normalized


async def _owned_conversation(
    conversation_id: str, user: User, db: AsyncSession
) -> Conversation:
    conversation = await db.scalar(
        select(Conversation).where(
            Conversation.id == conversation_id,
            Conversation.user_id == user.id,
        )
    )
    if conversation is None:
        raise HTTPException(status_code=404, detail="Sohbet bulunamadı")
    return conversation


def _load_json_list(value: str | None) -> list[Any]:
    if not value:
        return []
    try:
        parsed = json.loads(value)
    except (json.JSONDecodeError, TypeError):
        return []
    return parsed if isinstance(parsed, list) else []


@router.get("")
async def list_conversations(
    user: CurrentUser,
    db: Database,
) -> list[dict[str, Any]]:
    conversations = (
        await db.scalars(
            select(Conversation)
            .where(Conversation.user_id == user.id)
            .order_by(Conversation.updated_at.desc(), Conversation.created_at.desc())
        )
    ).all()
    return [
        {
            "id": conversation.id,
            "title": conversation.title,
            "updated_at": conversation.updated_at,
            "archived": conversation.archived_at is not None,
        }
        for conversation in conversations
    ]


@router.post("")
async def create_conversation(
    _payload: CreateConversationRequest,
    user: CurrentUser,
    db: Database,
) -> dict[str, str]:
    conversation = Conversation(user_id=user.id)
    db.add(conversation)
    await db.commit()
    return {"id": conversation.id}


@router.get("/{conversation_id}")
async def get_conversation(
    conversation_id: str,
    user: CurrentUser,
    db: Database,
) -> dict[str, Any]:
    conversation = await _owned_conversation(conversation_id, user, db)
    messages = (
        await db.scalars(
            select(Message)
            .where(Message.conversation_id == conversation.id)
            .order_by(Message.created_at, Message.id)
        )
    ).all()
    return {
        "id": conversation.id,
        "title": conversation.title,
        "created_at": conversation.created_at,
        "updated_at": conversation.updated_at,
        "archived": conversation.archived_at is not None,
        "messages": [
            {
                "id": message.id,
                "role": message.role,
                "content": message.content,
                "status": message.status,
                "tool_calls": _load_json_list(message.tool_calls_json),
                "citations": _load_json_list(message.citations_json),
                "created_at": message.created_at,
            }
            for message in messages
        ],
    }


@router.patch("/{conversation_id}")
async def update_conversation(
    conversation_id: str,
    payload: ConversationUpdateRequest,
    user: CurrentUser,
    db: Database,
) -> dict[str, Any]:
    conversation = await _owned_conversation(conversation_id, user, db)
    changed_fields = payload.model_fields_set
    if "title" in changed_fields:
        conversation.title = payload.title
    if "archived" in changed_fields and payload.archived is not None:
        conversation.archived_at = utcnow() if payload.archived else None
    if changed_fields:
        conversation.updated_at = utcnow()
        await db.commit()
    return {
        "id": conversation.id,
        "title": conversation.title,
        "updated_at": conversation.updated_at,
        "archived": conversation.archived_at is not None,
    }


@router.delete("/{conversation_id}", status_code=status.HTTP_204_NO_CONTENT)
async def delete_conversation(
    conversation_id: str,
    user: CurrentUser,
    db: Database,
) -> Response:
    conversation = await _owned_conversation(conversation_id, user, db)
    request_stop(conversation_id)
    await db.delete(conversation)
    await db.commit()
    return Response(status_code=status.HTTP_204_NO_CONTENT)


@router.post("/{conversation_id}/messages", response_model=None)
async def create_message(
    conversation_id: str,
    payload: MessageCreateRequest,
    user: CurrentUser,
    db: Database,
) -> Response:
    conversation = await _owned_conversation(conversation_id, user, db)
    try:
        cancel_event = reserve_generation(conversation_id)
    except GenerationAlreadyRunning as error:
        raise HTTPException(
            status_code=409,
            detail="Bu sohbet için zaten bir yanıt hazırlanıyor",
        ) from error

    try:
        try:
            usage = await check_and_touch(db, user.id)
        except HTTPException as error:
            if error.status_code == 429 and isinstance(error.detail, dict):
                release_generation(conversation_id, cancel_event)
                return JSONResponse(
                    status_code=429,
                    content=error.detail,
                    headers=error.headers,
                )
            raise
        if int(usage["llm_tokens"]) >= settings.daily_llm_token_quota:
            release_generation(conversation_id, cancel_event)
            return JSONResponse(
                status_code=429,
                content={
                    "detail": "Günlük LLM token kotası aşıldı",
                    "reset_at": usage["reset_at"],
                },
            )

        user_message = Message(
            conversation_id=conversation.id,
            role="user",
            content=payload.content,
            status="complete",
        )
        db.add(user_message)
        conversation.updated_at = utcnow()
        await db.commit()

        event_stream = start_chat_stream(
            conversation_id=conversation.id,
            user_id=user.id,
            cancel_event=cancel_event,
        )
    except Exception:
        release_generation(conversation_id, cancel_event)
        raise

    return StreamingResponse(
        event_stream,
        media_type="text/event-stream",
        headers={
            "Cache-Control": "no-cache, no-transform",
            "X-Accel-Buffering": "no",
        },
    )


@router.post("/{conversation_id}/stop")
async def stop_generation(
    conversation_id: str,
    user: CurrentUser,
    db: Database,
) -> dict[str, bool]:
    await _owned_conversation(conversation_id, user, db)
    return {"stopped": request_stop(conversation_id)}
