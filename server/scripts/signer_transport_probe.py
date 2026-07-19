"""Transport interop probe: sign an artifact ingest with the real FastAPI
CommandSigner and PUT it to a signerd socket, proving the Python command-auth
and artifact transfer interoperate with the Go verifier and broker. Prints a
single JSON line. Run from the server directory with the app venv.

Usage: python scripts/signer_transport_probe.py <socket> <key_pem> <kid>
"""

from __future__ import annotations

import asyncio
import hashlib
import json
import sys

from app.signing_client import CommandSigner, SignerClient, load_ed25519_private_key


async def main() -> None:
    socket_path, key_pem_path, kid = sys.argv[1], sys.argv[2], sys.argv[3]
    with open(key_pem_path, "rb") as handle:
        signer = CommandSigner(load_ed25519_private_key(handle.read()), kid)
    client = SignerClient(socket_path, signer, timeout_seconds=10)
    data = b"%PDF-1.7 transport interop artifact\n"
    sha = hashlib.sha256(data).hexdigest()
    try:
        result = await client.put_input("art-interop", data, sha)
        print(json.dumps({"ok": True, "byte_count": len(data), "sha256": sha, "result": result}))
    except Exception as error:  # noqa: BLE001 - probe reports any failure as JSON
        print(json.dumps({"ok": False, "error": type(error).__name__, "detail": str(error)}))
    finally:
        await client.aclose()


if __name__ == "__main__":
    asyncio.run(main())
