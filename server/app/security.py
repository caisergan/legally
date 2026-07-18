"""Password hashing + session token helpers."""

import hashlib
import secrets
from datetime import timedelta

from argon2 import PasswordHasher
from argon2.exceptions import VerifyMismatchError
from fastapi import Cookie, Depends, HTTPException, Request
from sqlalchemy import select
from sqlalchemy.ext.asyncio import AsyncSession

from .config import settings
from .db import get_db
from .models import AuthSession, User, utcnow

_hasher = PasswordHasher()

SESSION_COOKIE = "ya_session"


def hash_password(password: str) -> str:
    return _hasher.hash(password)


def verify_password(password_hash: str, password: str) -> bool:
    try:
        return _hasher.verify(password_hash, password)
    except VerifyMismatchError:
        return False


def new_session_token() -> str:
    return secrets.token_urlsafe(32)


def token_hash(token: str) -> str:
    return hashlib.sha256(token.encode()).hexdigest()


def session_expiry():
    return utcnow() + timedelta(days=settings.session_ttl_days)


async def get_current_user(
    request: Request,
    db: AsyncSession = Depends(get_db),
    ya_session: str | None = Cookie(default=None),
) -> User:
    if not ya_session:
        raise HTTPException(status_code=401, detail="Oturum bulunamadı")
    row = await db.get(AuthSession, token_hash(ya_session))
    if row is None or row.expires_at.replace(tzinfo=None) < utcnow().replace(tzinfo=None):
        raise HTTPException(status_code=401, detail="Oturum geçersiz veya süresi dolmuş")
    user = await db.get(User, row.user_id)
    if user is None:
        raise HTTPException(status_code=401, detail="Kullanıcı bulunamadı")
    # sliding renewal
    row.last_seen_at = utcnow()
    row.expires_at = session_expiry()
    await db.commit()
    return user


async def get_session_row(
    db: AsyncSession, token: str
) -> AuthSession | None:
    return await db.get(AuthSession, token_hash(token))
