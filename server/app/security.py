"""Password hashing + session token helpers, CSRF, and origin checks."""

import asyncio
import hashlib
import hmac
import secrets
from datetime import timedelta
from urllib.parse import urlparse

from argon2 import PasswordHasher
from argon2.exceptions import VerifyMismatchError
from fastapi import Cookie, Depends, HTTPException, Request, Response
from sqlalchemy import select
from sqlalchemy.ext.asyncio import AsyncSession

from .config import settings
from .db import get_db
from .models import AuthSession, User, utcnow

_hasher = PasswordHasher()

SESSION_COOKIE = "ya_session"
CSRF_COOKIE = "ya_csrf"
CSRF_HEADER = "x-csrf-token"


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


# --- CSRF (double-submit) + origin/host checks -------------------------------


def new_csrf_token() -> str:
    return secrets.token_urlsafe(32)


def ensure_csrf_cookie(request: Request, response: Response) -> str:
    """Return the caller's CSRF token, minting and setting the cookie if absent.

    Readable by the SPA (not httponly) so it can echo it in the CSRF header;
    `samesite=strict` keeps a cross-site page from causing it to be sent.
    """
    existing = request.cookies.get(CSRF_COOKIE)
    token = existing or new_csrf_token()
    if not existing:
        response.set_cookie(
            key=CSRF_COOKIE,
            value=token,
            max_age=settings.session_ttl_days * 24 * 60 * 60,
            httponly=False,
            secure=request.url.scheme == "https",
            samesite="strict",
            path="/",
        )
    return token


def require_csrf(request: Request) -> None:
    cookie = request.cookies.get(CSRF_COOKIE)
    header = request.headers.get(CSRF_HEADER)
    if not cookie or not header or not hmac.compare_digest(cookie, header):
        raise HTTPException(status_code=403, detail="Güvenlik doğrulaması başarısız")


def require_same_origin(request: Request) -> None:
    host = request.headers.get("host")
    origin = request.headers.get("origin")
    if origin:
        if urlparse(origin).netloc != host:
            raise HTTPException(status_code=403, detail="Kaynak doğrulaması başarısız")
        return
    # No Origin: fall back to Referer host equality when present.
    referer = request.headers.get("referer")
    if referer and urlparse(referer).netloc != host:
        raise HTTPException(status_code=403, detail="Kaynak doğrulaması başarısız")


def require_state_change_guards(request: Request) -> None:
    """CSRF + same-origin guard for every state-changing signing route."""
    require_same_origin(request)
    require_csrf(request)


async def verify_fresh_password(user: User, current_password: str) -> bool:
    """Fresh re-authentication for sensitive confirm/download actions."""
    if not current_password:
        return False
    return await asyncio.to_thread(verify_password, user.password_hash, current_password)
