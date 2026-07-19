"""Alembic baseline: fresh upgrade and populated-database migrate + restore."""

from __future__ import annotations

import os
import subprocess
import sys
from pathlib import Path

import pytest

SERVER_DIR = Path(__file__).resolve().parent.parent


def _alembic(db_path: Path, *args: str) -> subprocess.CompletedProcess:
    env = {**os.environ, "DATABASE_PATH": str(db_path)}
    return subprocess.run(
        [sys.executable, "-m", "alembic", "-c", "alembic.ini", *args],
        cwd=SERVER_DIR,
        env=env,
        capture_output=True,
        text=True,
    )


def _table_names(db_path: Path) -> set[str]:
    import sqlite3

    con = sqlite3.connect(db_path)
    try:
        return {r[0] for r in con.execute("SELECT name FROM sqlite_master WHERE type='table'")}
    finally:
        con.close()


@pytest.mark.skipif(
    subprocess.run([sys.executable, "-c", "import alembic"]).returncode != 0,
    reason="alembic not installed",
)
def test_fresh_upgrade_creates_all(tmp_path: Path):
    db = tmp_path / "fresh.db"
    result = _alembic(db, "upgrade", "head")
    assert result.returncode == 0, result.stderr
    tables = _table_names(db)
    assert "users" in tables
    assert sum(name.startswith("signing_") for name in tables) == 10


@pytest.mark.skipif(
    subprocess.run([sys.executable, "-c", "import alembic"]).returncode != 0,
    reason="alembic not installed",
)
def test_populated_db_stamp_and_upgrade_preserves_data(tmp_path: Path):
    db = tmp_path / "populated.db"
    # Build a populated legacy-only database.
    from sqlalchemy import create_engine, insert

    from app.models import Base, LEGACY_TABLE_NAMES, User

    engine = create_engine(f"sqlite:///{db}")
    Base.metadata.create_all(
        engine, tables=[Base.metadata.tables[name] for name in LEGACY_TABLE_NAMES]
    )
    with engine.begin() as conn:
        conn.execute(insert(User).values(email="keep@example.com", password_hash="x"))
    engine.dispose()

    assert _alembic(db, "stamp", "0001").returncode == 0
    upgrade = _alembic(db, "upgrade", "head")
    assert upgrade.returncode == 0, upgrade.stderr

    import sqlite3

    con = sqlite3.connect(db)
    try:
        rows = con.execute("SELECT email FROM users").fetchall()
        version = con.execute("SELECT version_num FROM alembic_version").fetchall()
    finally:
        con.close()
    assert rows == [("keep@example.com",)]
    assert version == [("0002",)]
    assert sum(name.startswith("signing_") for name in _table_names(db)) == 10
