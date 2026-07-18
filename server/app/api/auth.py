from __future__ import annotations

import asyncio
import re
from datetime import datetime
from typing import Annotated, Literal

from fastapi import APIRouter, Cookie, Depends, HTTPException, Request, Response, status
from pydantic import (
    BaseModel,
    ConfigDict,
    EmailStr,
    Field,
    TypeAdapter,
    ValidationError,
    field_validator,
)
from sqlalchemy import func, select
from sqlalchemy.exc import IntegrityError
from sqlalchemy.ext.asyncio import AsyncSession

from ..config import settings
from ..db import get_db
from ..models import AuthSession, User, utcnow
from ..security import (
    SESSION_COOKIE,
    get_current_user,
    hash_password,
    new_session_token,
    session_expiry,
    token_hash,
    verify_password,
)

router = APIRouter(prefix="/auth", tags=["auth"])
_registration_lock = asyncio.Lock()
_DUMMY_PASSWORD_HASH = (
    "$argon2id$v=19$m=65536,t=3,p=4$MIIRqgvgQbgj220qA6MPFg$"
    "YfwJSVjtjSU0zzV/P3S9nnQ/USre2wvJMjfCIjrTQbg"
)
_EMAIL_VALIDATOR = TypeAdapter(EmailStr)

Database = Annotated[AsyncSession, Depends(get_db)]
CurrentUser = Annotated[User, Depends(get_current_user)]
SessionCookie = Annotated[str | None, Cookie(alias=SESSION_COOKIE)]


class RequestModel(BaseModel):
    model_config = ConfigDict(extra="forbid")


class RegisterRequest(RequestModel):
    email: EmailStr
    password: str = Field(min_length=8, max_length=1024)
    display_name: str | None = Field(default=None, max_length=200)

    @field_validator("email", mode="before")
    @classmethod
    def return_clean_error_for_invalid_email(cls, value: object) -> object:
        try:
            _EMAIL_VALIDATOR.validate_python(value)
        except ValidationError as error:
            raise HTTPException(
                status_code=400, detail="Geçerli bir e-posta adresi girin"
            ) from error
        return value

    @field_validator("password", mode="before")
    @classmethod
    def return_clean_error_for_short_password(cls, value: object) -> object:
        if isinstance(value, str) and len(value) < 8:
            raise HTTPException(
                status_code=400, detail="Şifre en az 8 karakter olmalıdır"
            )
        return value


class LoginRequest(RequestModel):
    email: EmailStr
    password: str = Field(min_length=1, max_length=1024)


class ProfileUpdate(RequestModel):
    display_name: str | None = Field(default=None, max_length=200)
    locale: Literal["tr", "en"] | None = None
    theme: Literal["light", "dark"] | None = None
    preferred_model: Literal["sonnet5", "opus", "haiku"] | None = None
    password: str | None = Field(default=None, min_length=8, max_length=1024)
    current_password: str | None = Field(default=None, max_length=1024)


class UserOut(BaseModel):
    model_config = ConfigDict(from_attributes=True)

    id: int
    email: str
    display_name: str | None
    role: str
    locale: str
    theme: str
    preferred_model: str | None
    created_at: datetime


class SessionOut(BaseModel):
    token_prefix: str
    user_agent: str | None
    created_at: datetime
    last_seen_at: datetime
    current: bool


def _normalized_email(value: str) -> str:
    return value.strip().lower()


def _normalized_display_name(value: str | None) -> str | None:
    if value is None:
        return None
    return value.strip() or None


def _secure_cookie(request: Request) -> bool:
    return request.url.scheme == "https"


def _set_session_cookie(
    response: Response, request: Request, raw_token: str
) -> None:
    response.set_cookie(
        key=SESSION_COOKIE,
        value=raw_token,
        max_age=settings.session_ttl_days * 24 * 60 * 60,
        httponly=True,
        secure=_secure_cookie(request),
        samesite="lax",
        path="/",
    )


def _clear_session_cookie(response: Response, request: Request) -> None:
    response.delete_cookie(
        key=SESSION_COOKIE,
        httponly=True,
        secure=_secure_cookie(request),
        samesite="lax",
        path="/",
    )


def _add_session(db: AsyncSession, user: User, request: Request) -> str:
    raw_token = new_session_token()
    now = utcnow()
    user_agent = request.headers.get("user-agent")
    db.add(
        AuthSession(
            token_hash=token_hash(raw_token),
            user_id=user.id,
            created_at=now,
            expires_at=session_expiry(),
            last_seen_at=now,
            user_agent=user_agent[:500] if user_agent else None,
        )
    )
    return raw_token


@router.post("/register", response_model=UserOut, status_code=status.HTTP_201_CREATED)
async def register(
    payload: RegisterRequest,
    request: Request,
    response: Response,
    db: Database,
) -> User:
    if settings.registration_mode.lower() == "closed":
        raise HTTPException(status_code=403, detail="Kayıt kapalı")

    email = _normalized_email(str(payload.email))
    password_hash = await asyncio.to_thread(hash_password, payload.password)
    async with _registration_lock:
        existing_user_id = await db.scalar(select(User.id).where(User.email == email))
        if existing_user_id is not None:
            raise HTTPException(
                status_code=409, detail="Bu e-posta adresi zaten kayıtlı"
            )

        user_count = await db.scalar(select(func.count(User.id))) or 0
        user = User(
            email=email,
            password_hash=password_hash,
            display_name=_normalized_display_name(payload.display_name),
            role="admin" if user_count == 0 else "user",
        )
        db.add(user)
        try:
            await db.flush()
            raw_token = _add_session(db, user, request)
            await db.commit()
        except IntegrityError as error:
            await db.rollback()
            raise HTTPException(
                status_code=409, detail="Bu e-posta adresi zaten kayıtlı"
            ) from error

    await db.refresh(user)
    _set_session_cookie(response, request, raw_token)
    return user


@router.post("/login", response_model=UserOut)
async def login(
    payload: LoginRequest,
    request: Request,
    response: Response,
    db: Database,
) -> User:
    email = _normalized_email(str(payload.email))
    user = await db.scalar(select(User).where(User.email == email))
    password_hash = user.password_hash if user is not None else _DUMMY_PASSWORD_HASH
    password_matches = await asyncio.to_thread(
        verify_password, password_hash, payload.password
    )
    if user is None or not password_matches:
        raise HTTPException(status_code=401, detail="E-posta veya şifre hatalı")

    user.last_login_at = utcnow()
    raw_token = _add_session(db, user, request)
    await db.commit()
    await db.refresh(user)
    _set_session_cookie(response, request, raw_token)
    return user


@router.post("/logout", status_code=status.HTTP_204_NO_CONTENT)
async def logout(
    request: Request,
    db: Database,
    ya_session: SessionCookie = None,
) -> Response:
    if ya_session:
        session = await db.get(AuthSession, token_hash(ya_session))
        if session is not None:
            await db.delete(session)
            await db.commit()
    response = Response(status_code=status.HTTP_204_NO_CONTENT)
    _clear_session_cookie(response, request)
    return response


@router.get("/me", response_model=UserOut)
async def get_me(user: CurrentUser) -> User:
    return user


@router.get("/sessions", response_model=list[SessionOut])
async def list_sessions(
    user: CurrentUser,
    db: Database,
    ya_session: SessionCookie = None,
) -> list[SessionOut]:
    current_hash = token_hash(ya_session) if ya_session else None
    sessions = (
        await db.scalars(
            select(AuthSession)
            .where(AuthSession.user_id == user.id)
            .order_by(AuthSession.created_at.desc())
        )
    ).all()
    return [
        SessionOut(
            token_prefix=session.token_hash[:12],
            user_agent=session.user_agent,
            created_at=session.created_at,
            last_seen_at=session.last_seen_at,
            current=session.token_hash == current_hash,
        )
        for session in sessions
    ]


@router.delete("/sessions/{prefix}", status_code=status.HTTP_204_NO_CONTENT)
async def revoke_session(
    prefix: str,
    request: Request,
    user: CurrentUser,
    db: Database,
    ya_session: SessionCookie = None,
) -> Response:
    if re.fullmatch(r"[0-9a-f]{12}", prefix) is None:
        raise HTTPException(status_code=404, detail="Oturum bulunamadı")
    session = await db.scalar(
        select(AuthSession).where(
            AuthSession.user_id == user.id,
            AuthSession.token_hash.startswith(prefix),
        )
    )
    if session is None:
        raise HTTPException(status_code=404, detail="Oturum bulunamadı")
    is_current = bool(ya_session) and session.token_hash == token_hash(ya_session)
    await db.delete(session)
    await db.commit()
    response = Response(status_code=status.HTTP_204_NO_CONTENT)
    if is_current:
        _clear_session_cookie(response, request)
    return response


@router.patch("/me", response_model=UserOut)
async def update_me(
    payload: ProfileUpdate,
    user: CurrentUser,
    db: Database,
) -> User:
    changed_fields = payload.model_fields_set
    if "display_name" in changed_fields:
        user.display_name = _normalized_display_name(payload.display_name)
    if payload.locale is not None:
        user.locale = payload.locale
    if payload.theme is not None:
        user.theme = payload.theme
    if "preferred_model" in changed_fields:
        user.preferred_model = payload.preferred_model

    if payload.password is not None:
        if not payload.current_password:
            raise HTTPException(status_code=400, detail="Mevcut şifre gerekli")
        password_matches = await asyncio.to_thread(
            verify_password, user.password_hash, payload.current_password
        )
        if not password_matches:
            raise HTTPException(status_code=400, detail="Mevcut şifre hatalı")
        user.password_hash = await asyncio.to_thread(hash_password, payload.password)

    await db.commit()
    await db.refresh(user)
    return user
