"""Private streaming artifact storage (plan §8.1, §8.3).

Document bytes live only on a private `0700` filesystem tree, never in SQLite,
logs, or shared caches. Writes are streamed to a temporary file, hashed while
streaming, size-capped, fsynced, and atomically renamed. Storage keys are opaque
hex tokens; every path is validated and realpath-checked to defeat traversal and
symlink attacks.
"""

from __future__ import annotations

import asyncio
import hashlib
import os
import re
import secrets
from collections.abc import AsyncIterator, Iterator
from dataclasses import dataclass
from pathlib import Path

_KEY_RE = re.compile(r"^[0-9a-f]{32}$")
_READ_CHUNK = 1 << 16


class ArtifactStoreError(Exception):
    pass


class ArtifactTooLarge(ArtifactStoreError):
    pass


class ArtifactNotFound(ArtifactStoreError):
    pass


class UnsafeStorageKey(ArtifactStoreError):
    pass


@dataclass(frozen=True)
class StoredArtifact:
    storage_key: str
    sha256: str
    byte_count: int


def new_storage_key() -> str:
    return secrets.token_hex(16)


class ArtifactStore:
    def __init__(self, root: Path) -> None:
        self._root = Path(root).resolve()
        self._root.mkdir(parents=True, exist_ok=True)
        os.chmod(self._root, 0o700)
        self._tmp = self._root / "tmp"
        self._tmp.mkdir(exist_ok=True)
        os.chmod(self._tmp, 0o700)

    def _final_path(self, storage_key: str) -> Path:
        if not _KEY_RE.match(storage_key):
            raise UnsafeStorageKey("invalid storage key")
        shard = self._root / storage_key[:2]
        path = (shard / storage_key).resolve()
        # Reject any resolved path that escapes the root (symlink / traversal).
        if os.path.commonpath([self._root, path]) != str(self._root):
            raise UnsafeStorageKey("storage key escapes root")
        return path

    async def store_stream(
        self, chunks: AsyncIterator[bytes], max_bytes: int
    ) -> StoredArtifact:
        storage_key = new_storage_key()
        final = self._final_path(storage_key)
        await asyncio.to_thread(final.parent.mkdir, parents=True, exist_ok=True)
        await asyncio.to_thread(os.chmod, final.parent, 0o700)
        tmp = self._tmp / f"{storage_key}.part"

        digest = hashlib.sha256()
        total = 0
        fd = await asyncio.to_thread(os.open, tmp, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
        try:
            async for chunk in chunks:
                if not chunk:
                    continue
                total += len(chunk)
                if total > max_bytes:
                    raise ArtifactTooLarge(f"artifact exceeds {max_bytes} bytes")
                digest.update(chunk)
                await asyncio.to_thread(_write_all, fd, chunk)
            await asyncio.to_thread(os.fsync, fd)
        except BaseException:
            await asyncio.to_thread(os.close, fd)
            await asyncio.to_thread(_unlink_quiet, tmp)
            raise
        else:
            await asyncio.to_thread(os.close, fd)

        await asyncio.to_thread(os.replace, tmp, final)
        return StoredArtifact(storage_key, digest.hexdigest(), total)

    def read_iter(self, storage_key: str) -> Iterator[bytes]:
        final = self._final_path(storage_key)
        try:
            fd = os.open(final, os.O_RDONLY | getattr(os, "O_NOFOLLOW", 0))
        except FileNotFoundError as error:
            raise ArtifactNotFound(storage_key) from error
        try:
            with os.fdopen(fd, "rb", closefd=True) as handle:
                while True:
                    block = handle.read(_READ_CHUNK)
                    if not block:
                        return
                    yield block
        except OSError as error:
            raise ArtifactNotFound(storage_key) from error

    async def read_all(self, storage_key: str) -> bytes:
        return await asyncio.to_thread(self._read_all_sync, storage_key)

    def _read_all_sync(self, storage_key: str) -> bytes:
        return b"".join(self.read_iter(storage_key))

    def byte_count(self, storage_key: str) -> int:
        final = self._final_path(storage_key)
        try:
            return final.stat().st_size
        except FileNotFoundError as error:
            raise ArtifactNotFound(storage_key) from error

    async def delete(self, storage_key: str) -> bool:
        final = self._final_path(storage_key)
        return await asyncio.to_thread(_unlink_quiet, final)


def _write_all(fd: int, data: bytes) -> None:
    view = memoryview(data)
    while view:
        written = os.write(fd, view)
        view = view[written:]


def _unlink_quiet(path: Path) -> bool:
    try:
        os.unlink(path)
        return True
    except FileNotFoundError:
        return False
