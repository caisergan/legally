from fastapi import APIRouter

from .auth import router as auth_router
from .meta import router as meta_router

api_router = APIRouter()
api_router.include_router(auth_router)
api_router.include_router(meta_router)
