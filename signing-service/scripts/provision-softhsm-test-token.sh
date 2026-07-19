#!/usr/bin/env bash
# Provision an isolated, TEST-ONLY SoftHSM token for the live signing end-to-end
# test (test/integration/live_softhsm_test.go, build tag pkcs11). It creates a
# self-signed RSA-2048 credential that mirrors a real qualified token: the
# private-key object is CKA_PRIVATE (hidden until login) and CKA_SENSITIVE, with
# a matching public-key object left visible pre-login so enrollment's
# token.Resolve can bind it without a PIN. This is a self-signed test credential:
# its output is never a qualified signature and always stays QUARANTINED.
#
# Requires: softhsm2-util, openssl, and a Go toolchain (for the pkcs11-tagged
# yargi-softhsm-provision helper). Prints the SCRATCH_* environment the live test
# consumes. Nothing here is a secret worth protecting.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

WORKDIR="${1:-${SOFTHSM_TEST_DIR:-$(mktemp -d)/softhsm}}"
MODULE="${SOFTHSM_MODULE:-/opt/homebrew/lib/softhsm/libsofthsm2.so}"
LABEL="${SOFTHSM_TOKEN_LABEL:-yargi-test-token}"
SOPIN="${SOFTHSM_SO_PIN:-3737}"
USERPIN="${SOFTHSM_USER_PIN:-648219}"
CKAID="${SOFTHSM_CKAID:-a1b2c3d4}"

[ -f "$MODULE" ] || { echo "PKCS#11 module not found: $MODULE" >&2; exit 1; }

rm -rf "$WORKDIR"
mkdir -p "$WORKDIR/tokens"
cat > "$WORKDIR/softhsm2.conf" <<EOF
directories.tokendir = $WORKDIR/tokens
objectstore.backend = file
objectstore.umask = 0077
log.level = ERROR
EOF
export SOFTHSM2_CONF="$WORKDIR/softhsm2.conf"

softhsm2-util --init-token --free --label "$LABEL" --so-pin "$SOPIN" --pin "$USERPIN" >/dev/null

openssl genpkey -algorithm RSA -pkeyopt rsa_keygen_bits:2048 -out "$WORKDIR/key.p8" 2>/dev/null
openssl pkey -in "$WORKDIR/key.p8" -outform DER -out "$WORKDIR/key.der" 2>/dev/null
openssl req -x509 -new -key "$WORKDIR/key.p8" -sha256 -days 365 \
  -subj "/CN=Ege Ayyildiz/O=Yargi Test/C=TR" -out "$WORKDIR/cert.pem" 2>/dev/null
openssl x509 -in "$WORKDIR/cert.pem" -outform DER -out "$WORKDIR/cert.der"

# Hidden CKA_PRIVATE private key + visible public-key object + certificate,
# created via C_CreateObject. Mirrors a real qualified token; enrollment binds
# the credential via the public-key object (token.Resolve fallback).
( cd "$REPO_ROOT" && go run -tags pkcs11 ./cmd/yargi-softhsm-provision \
  --module "$MODULE" --token-label "$LABEL" --pin "$USERPIN" \
  --key-der "$WORKDIR/key.der" --cert-der "$WORKDIR/cert.der" --id "$CKAID" >/dev/null )

SLOT=$(softhsm2-util --show-slots | awk '/^Slot [0-9]/{s=$2} /Label:[[:space:]]*'"$LABEL"'/{print s; exit}')
FINGERPRINT=$(openssl dgst -sha256 "$WORKDIR/cert.der" | awk '{print $2}')

echo "# SoftHSM test token provisioned"
echo "export SOFTHSM2_CONF=$WORKDIR/softhsm2.conf"
echo "export SCRATCH_MODULE=$MODULE"
echo "export SCRATCH_CERT_DER=$WORKDIR/cert.der"
echo "export SCRATCH_PIN=$USERPIN"
echo "export SCRATCH_CKAID=$CKAID"
echo "export SCRATCH_SLOT=$SLOT"
echo "# certificate sha256=$FINGERPRINT"
