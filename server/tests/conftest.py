"""Shared fixtures for signing tests. Each test gets an isolated temp SQLite DB
built from the model metadata (tables + triggers) and a private artifact store."""

from __future__ import annotations

from collections.abc import AsyncIterator
from pathlib import Path

import pytest
import pytest_asyncio
from sqlalchemy import event
from sqlalchemy.ext.asyncio import AsyncSession, async_sessionmaker, create_async_engine

from app.artifact_capabilities import ArtifactBroker, CapabilityIssuer
from app.artifact_store import ArtifactStore
from app.models import Base


@pytest_asyncio.fixture
async def sessionmaker(tmp_path: Path) -> AsyncIterator[async_sessionmaker[AsyncSession]]:
    engine = create_async_engine(f"sqlite+aiosqlite:///{tmp_path / 'test.db'}")

    @event.listens_for(engine.sync_engine, "connect")
    def _pragmas(dbapi_connection, _record):
        cursor = dbapi_connection.cursor()
        cursor.execute("PRAGMA foreign_keys=ON")
        cursor.close()

    async with engine.begin() as conn:
        await conn.run_sync(Base.metadata.create_all)
    yield async_sessionmaker(engine, class_=AsyncSession, expire_on_commit=False)
    await engine.dispose()


@pytest_asyncio.fixture
async def db(sessionmaker) -> AsyncIterator[AsyncSession]:
    async with sessionmaker() as session:
        yield session


@pytest.fixture
def store(tmp_path: Path) -> ArtifactStore:
    return ArtifactStore(tmp_path / "artifacts")


@pytest.fixture
def issuer() -> CapabilityIssuer:
    return CapabilityIssuer(b"broker-capability-secret", "broker-cap-1", ttl_seconds=30)


@pytest.fixture
def broker(store: ArtifactStore, issuer: CapabilityIssuer) -> ArtifactBroker:
    return ArtifactBroker(store, issuer)


async def async_iter(chunks: list[bytes]):
    for chunk in chunks:
        yield chunk
