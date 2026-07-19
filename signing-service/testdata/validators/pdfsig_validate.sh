#!/bin/sh
# Poppler pdfsig wrapper for the Phase 1B validator matrix.
# Prints a single normalized cryptographic outcome token: valid | invalid.
if command -v pdfsig >/dev/null 2>&1 && pdfsig "$1" 2>&1 | grep -q "Signature is Valid"; then
  echo valid
else
  echo invalid
fi
