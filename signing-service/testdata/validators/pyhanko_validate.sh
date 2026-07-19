#!/bin/sh
# pyHanko wrapper for the Phase 1B validator matrix.
# Prints a single normalized cryptographic outcome token: valid | invalid.
PY="${YARGI_PYHANKO_PYTHON:-python3}"
DIR="$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)"
JSON="$("$PY" "$DIR/../../tools/pades_validate_pyhanko.py" "$1" 2>/dev/null)"
case "$JSON" in
  *'"crypto": "valid"'*) echo valid ;;
  *) echo invalid ;;
esac
