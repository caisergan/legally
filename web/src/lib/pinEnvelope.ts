// Browser-side PIN challenge verification and envelope encryption for the e-imza
// flow. The signer issues an ES256-signed challenge carrying a per-challenge
// ephemeral ECDH recipient key; the browser verifies that signature against the
// pinned challenge key, then encrypts the PIN as a compact JWE under the single
// allowlisted ECDH-ES + A256GCM profile with the protected-header `kid` bound to
// the challenge id. This matches the Go `internal/pin` DecryptPIN contract. The
// plaintext PIN never leaves this module except as ciphertext.

export interface JsonWebKey256 {
  kty: string;
  crv: string;
  x: string;
  y: string;
  [key: string]: unknown;
}

export interface VerifiedChallenge {
  challengeId: string;
  recipientJwk: JsonWebKey256;
  jweAlg: string;
  jweEnc: string;
  jobId: string;
  requestId: string;
  inputSha256: string;
  certificateFingerprintSha256: string;
  policyVersion: string;
  expiresAt: string;
}

const ENVELOPE_ALG = "ECDH-ES";
const ENVELOPE_ENC = "A256GCM";

function base64UrlEncode(bytes: Uint8Array): string {
  let binary = "";
  for (const byte of bytes) binary += String.fromCharCode(byte);
  return btoa(binary).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
}

// Bytes is an ArrayBuffer-backed view, required by the WebCrypto BufferSource
// parameter typings (which reject SharedArrayBuffer-backed views).
type Bytes = Uint8Array<ArrayBuffer>;

function base64UrlDecode(value: string): Bytes {
  const padded = value.replace(/-/g, "+").replace(/_/g, "/").padEnd(Math.ceil(value.length / 4) * 4, "=");
  const binary = atob(padded);
  const bytes = new Uint8Array(binary.length);
  for (let i = 0; i < binary.length; i += 1) bytes[i] = binary.charCodeAt(i);
  return bytes;
}

function utf8(value: string): Bytes {
  return new Uint8Array(new TextEncoder().encode(value));
}

function concat(...parts: Bytes[]): Bytes {
  const total = parts.reduce((sum, part) => sum + part.length, 0);
  const out = new Uint8Array(total);
  let offset = 0;
  for (const part of parts) {
    out.set(part, offset);
    offset += part.length;
  }
  return out;
}

function uint32BE(value: number): Bytes {
  const out = new Uint8Array(4);
  new DataView(out.buffer).setUint32(0, value, false);
  return out;
}

function lengthPrefixed(data: Bytes): Bytes {
  return concat(uint32BE(data.length), data);
}

// RFC 7638 thumbprint over the canonical EC JWK members (crv, kty, x, y).
export async function jwkThumbprint(jwk: JsonWebKey256): Promise<string> {
  const canonical = `{"crv":"${jwk.crv}","kty":"${jwk.kty}","x":"${jwk.x}","y":"${jwk.y}"}`;
  const digest = new Uint8Array(await crypto.subtle.digest("SHA-256", utf8(canonical)));
  return base64UrlEncode(digest);
}

// verifyChallenge validates the ES256 JWS against a pinned key and returns the
// signer-bound challenge fields. It throws if no pinned key verifies the
// signature or the recipient thumbprint in the payload does not match.
export async function verifyChallenge(jws: string, pinnedKeys: JsonWebKey[]): Promise<VerifiedChallenge> {
  const parts = jws.split(".");
  if (parts.length !== 3) throw new Error("İmza yanıtı biçimi geçersiz.");
  const headerPart = parts[0]!;
  const payloadPart = parts[1]!;
  const signaturePart = parts[2]!;
  const signingInput = utf8(`${headerPart}.${payloadPart}`);
  const signature = base64UrlDecode(signaturePart);

  let verified = false;
  for (const jwk of pinnedKeys) {
    const source = jwk as unknown as JsonWebKey256;
    if (source.kty !== "EC" || source.crv !== "P-256") continue;
    const clean = { kty: source.kty, crv: source.crv, x: source.x, y: source.y };
    try {
      const key = await crypto.subtle.importKey("jwk", clean, { name: "ECDSA", namedCurve: "P-256" }, false, ["verify"]);
      if (await crypto.subtle.verify({ name: "ECDSA", hash: "SHA-256" }, key, signature, signingInput)) {
        verified = true;
        break;
      }
    } catch {
      // try the next pinned key
    }
  }
  if (!verified) throw new Error("İmza yanıtının doğruluğu kanıtlanamadı.");

  const payload = JSON.parse(new TextDecoder().decode(base64UrlDecode(payloadPart))) as Record<string, unknown>;
  const recipientJwk = payload.recipient_jwk as JsonWebKey256 | undefined;
  if (!recipientJwk || recipientJwk.kty !== "EC" || recipientJwk.crv !== "P-256") {
    throw new Error("Alıcı anahtarı beklenen biçimde değil.");
  }
  const boundThumbprint = String(payload.recipient_jwk_thumbprint ?? "");
  if ((await jwkThumbprint(recipientJwk)) !== boundThumbprint) {
    throw new Error("Alıcı anahtarı parmak izi eşleşmiyor.");
  }
  const jweAlg = String(payload.jwe_alg ?? "");
  const jweEnc = String(payload.jwe_enc ?? "");
  if (jweAlg !== ENVELOPE_ALG || jweEnc !== ENVELOPE_ENC) {
    throw new Error("İzin verilmeyen zarf profili.");
  }
  return {
    challengeId: String(payload.challenge_id ?? ""),
    recipientJwk,
    jweAlg,
    jweEnc,
    jobId: String(payload.job_id ?? ""),
    requestId: String(payload.request_id ?? ""),
    inputSha256: String(payload.input_sha256 ?? ""),
    certificateFingerprintSha256: String(payload.certificate_fingerprint_sha256 ?? ""),
    policyVersion: String(payload.policy_version ?? ""),
    expiresAt: String(payload.expires_at ?? ""),
  };
}

// NIST SP 800-56A Concat KDF (single round; keydatalen 256) for ECDH-ES direct
// key agreement, matching RFC 7518 §4.6 and go-jose.
async function concatKdf(sharedSecret: Bytes, algorithmId: string): Promise<Bytes> {
  const otherInfo = concat(
    lengthPrefixed(utf8(algorithmId)),
    lengthPrefixed(new Uint8Array(0)),
    lengthPrefixed(new Uint8Array(0)),
    uint32BE(256),
  );
  const input = concat(uint32BE(1), sharedSecret, otherInfo);
  return new Uint8Array(await crypto.subtle.digest("SHA-256", input));
}

// encryptPinEnvelope produces a compact JWE (ECDH-ES + A256GCM) for the PIN,
// with an ephemeral sender key and the challenge id in the protected `kid`.
export async function encryptPinEnvelope(pin: string, recipientJwk: JsonWebKey256, challengeId: string): Promise<string> {
  const recipientKey = await crypto.subtle.importKey(
    "jwk",
    { kty: recipientJwk.kty, crv: recipientJwk.crv, x: recipientJwk.x, y: recipientJwk.y },
    { name: "ECDH", namedCurve: "P-256" },
    false,
    [],
  );
  const ephemeral = await crypto.subtle.generateKey({ name: "ECDH", namedCurve: "P-256" }, true, ["deriveBits"]);
  const sharedSecret = new Uint8Array(
    await crypto.subtle.deriveBits({ name: "ECDH", public: recipientKey }, ephemeral.privateKey, 256),
  );
  const cek = await concatKdf(sharedSecret, ENVELOPE_ENC);
  const aesKey = await crypto.subtle.importKey("raw", cek, { name: "AES-GCM" }, false, ["encrypt"]);

  const epk = await crypto.subtle.exportKey("jwk", ephemeral.publicKey);
  const protectedHeader = {
    alg: ENVELOPE_ALG,
    enc: ENVELOPE_ENC,
    epk: { kty: "EC", crv: "P-256", x: epk.x, y: epk.y },
    kid: challengeId,
  };
  const protectedB64 = base64UrlEncode(utf8(JSON.stringify(protectedHeader)));

  const iv = crypto.getRandomValues(new Uint8Array(12));
  const sealed = new Uint8Array(
    await crypto.subtle.encrypt(
      { name: "AES-GCM", iv, additionalData: utf8(protectedB64), tagLength: 128 },
      aesKey,
      utf8(pin),
    ),
  );
  const ciphertext = sealed.slice(0, sealed.length - 16);
  const tag = sealed.slice(sealed.length - 16);

  return [protectedB64, "", base64UrlEncode(iv), base64UrlEncode(ciphertext), base64UrlEncode(tag)].join(".");
}
