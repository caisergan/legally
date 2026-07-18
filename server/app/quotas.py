from __future__ import annotations

from datetime import date, datetime, time, timedelta, timezone

from fastapi import HTTPException
from sqlalchemy import and_, select
from sqlalchemy.dialects.sqlite import insert as sqlite_insert
from sqlalchemy.ext.asyncio import AsyncSession

from .config import settings
from .models import UsageDaily


def _utc_day() -> tuple[date, str]:
    today = datetime.now(timezone.utc).date()
    next_midnight = datetime.combine(
        today + timedelta(days=1), time.min, tzinfo=timezone.utc
    )
    return today, next_midnight.isoformat().replace("+00:00", "Z")


def _quota_exception(message: str, reset_at: str) -> HTTPException:
    return HTTPException(
        status_code=429,
        detail={"detail": message, "reset_at": reset_at},
    )


async def get_usage(db: AsyncSession, user_id: int) -> dict[str, int | str]:
    today, reset_at = _utc_day()
    usage = await db.get(UsageDaily, (user_id, today))
    input_tokens = usage.llm_input_tokens if usage else 0
    output_tokens = usage.llm_output_tokens if usage else 0
    tool_calls = usage.tool_calls if usage else 0
    return {
        "day": today.isoformat(),
        "llm_input_tokens": input_tokens,
        "llm_output_tokens": output_tokens,
        "llm_tokens": input_tokens + output_tokens,
        "tool_calls": tool_calls,
        "daily_llm_token_quota": settings.daily_llm_token_quota,
        "daily_tool_call_quota": settings.daily_tool_call_quota,
        "reset_at": reset_at,
    }


async def check_and_touch(
    db: AsyncSession,
    user_id: int,
    *,
    need_tool_call: bool = False,
    tokens: int = 0,
) -> dict[str, int | str]:
    if tokens < 0:
        raise ValueError("tokens negatif olamaz")

    today, reset_at = _utc_day()
    tool_increment = 1 if need_tool_call else 0
    if tokens > settings.daily_llm_token_quota:
        raise _quota_exception("Günlük LLM token kotası aşıldı", reset_at)
    if tool_increment > settings.daily_tool_call_quota:
        raise _quota_exception("Günlük araç çağrısı kotası aşıldı", reset_at)

    statement = sqlite_insert(UsageDaily).values(
        user_id=user_id,
        day=today,
        llm_input_tokens=tokens,
        llm_output_tokens=0,
        tool_calls=tool_increment,
    )
    statement = statement.on_conflict_do_update(
        index_elements=[UsageDaily.user_id, UsageDaily.day],
        set_={
            "llm_input_tokens": UsageDaily.llm_input_tokens + tokens,
            "tool_calls": UsageDaily.tool_calls + tool_increment,
        },
        where=and_(
            UsageDaily.llm_input_tokens
            + UsageDaily.llm_output_tokens
            + tokens
            <= settings.daily_llm_token_quota,
            UsageDaily.tool_calls + tool_increment
            <= settings.daily_tool_call_quota,
        ),
    )

    result = await db.execute(statement)
    if result.rowcount == 0:
        usage = await db.scalar(
            select(UsageDaily).where(
                UsageDaily.user_id == user_id,
                UsageDaily.day == today,
            )
        )
        current_tokens = 0
        current_tool_calls = 0
        if usage is not None:
            current_tokens = usage.llm_input_tokens + usage.llm_output_tokens
            current_tool_calls = usage.tool_calls
        await db.rollback()
        if current_tokens + tokens > settings.daily_llm_token_quota:
            raise _quota_exception("Günlük LLM token kotası aşıldı", reset_at)
        if current_tool_calls + tool_increment > settings.daily_tool_call_quota:
            raise _quota_exception("Günlük araç çağrısı kotası aşıldı", reset_at)
        raise _quota_exception("Günlük kullanım kotası aşıldı", reset_at)

    await db.commit()
    return await get_usage(db, user_id)
