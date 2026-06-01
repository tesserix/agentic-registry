// Client-side verification of the registry's Ed25519 digest attestation, using
// the browser's WebCrypto (Ed25519 is supported in recent Chrome/Safari/Firefox).
// The registry signs the "sha256:<hex>" digest string; we verify the signature
// against the published public key over those exact bytes.

function b64ToBytes(b64: string): Uint8Array {
  const bin = atob(b64);
  const out = new Uint8Array(bin.length);
  for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i);
  return out;
}

export async function verifyEd25519(publicKeyB64: string, signatureB64: string, message: string): Promise<boolean> {
  const pub = b64ToBytes(publicKeyB64) as BufferSource;
  const sig = b64ToBytes(signatureB64) as BufferSource;
  const msg = new TextEncoder().encode(message) as BufferSource;
  const key = await crypto.subtle.importKey("raw", pub, { name: "Ed25519" }, false, ["verify"]);
  return crypto.subtle.verify({ name: "Ed25519" }, key, sig, msg);
}
