from fastapi import APIRouter

from .account import router as account_router
from .auth import router as auth_router
from .bookmarks import router as bookmarks_router
from .conversations import router as conversations_router
from .documents import router as documents_router
from .history import router as history_router
from .meta import router as meta_router
from .search import router as search_router
from .signing import router as signing_router

api_router = APIRouter()
api_router.include_router(auth_router)
api_router.include_router(meta_router)
api_router.include_router(search_router)
api_router.include_router(documents_router)
api_router.include_router(history_router)
api_router.include_router(bookmarks_router)
api_router.include_router(account_router)
api_router.include_router(conversations_router)
api_router.include_router(signing_router)
