"""Private artifact store: atomic write, hash, size cap, traversal, symlink."""

from __future__ import annotations

import hashlib
import os
from pathlib import Path

import pytest

from app.artifact_store import (
    ArtifactNotFound,
    ArtifactStore,
    ArtifactTooLarge,
    UnsafeStorageKey,
)

from .conftest import async_iter


async def test_store_stream_hashes_and_atomic(store: ArtifactStore):
    data = b"%PDF-1.4 hello world"
    stored = await store.store_stream(async_iter([data[:5], data[5:]]), max_bytes=1024)
    assert stored.byte_count == len(data)
    assert stored.sha256 == hashlib.sha256(data).hexdigest()
    assert await store.read_all(stored.storage_key) == data
    # No leftover temp part files.
    assert not list((store._tmp).glob("*.part"))


async def test_store_stream_rejects_oversize_and_cleans_up(store: ArtifactStore):
    with pytest.raises(ArtifactTooLarge):
        await store.store_stream(async_iter([b"x" * 10, b"y" * 10]), max_bytes=15)
    assert not list((store._tmp).glob("*.part"))


async def test_unsafe_storage_key_rejected(store: ArtifactStore):
    for bad in ("../etc/passwd", "..", "abc", "/abs", "zz" * 16 + "GG"):
        with pytest.raises(UnsafeStorageKey):
            store._final_path(bad)


async def test_symlink_escape_rejected(store: ArtifactStore, tmp_path: Path):
    stored = await store.store_stream(async_iter([b"data"]), max_bytes=64)
    key = stored.storage_key
    final = store._final_path(key)
    # Replace the stored file's shard directory member with a symlink to /etc.
    outside = tmp_path / "outside"
    outside.mkdir()
    (outside / "secret").write_bytes(b"top secret")
    os.unlink(final)
    os.symlink(outside / "secret", final)
    # The read refuses: realpath escaping the root is rejected before open, and
    # O_NOFOLLOW would refuse the symlink even if it did not.
    with pytest.raises((UnsafeStorageKey, ArtifactNotFound)):
        await store.read_all(key)


async def test_delete_missing_is_false(store: ArtifactStore):
    stored = await store.store_stream(async_iter([b"z"]), max_bytes=8)
    assert await store.delete(stored.storage_key) is True
    assert await store.delete(stored.storage_key) is False
