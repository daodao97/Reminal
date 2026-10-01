// The only parts of @noble/curves the viewer uses, for its CPace handshake.
// Kept deliberately small so what ships is easy to read against the source.
import { RistrettoPoint, ed25519 } from '@noble/curves/ed25519';
const n = ed25519.CURVE.n;
const ZERO = RistrettoPoint.ZERO;
window.ReminalRistretto = Object.freeze({
  order: n,
  // An element from 64 uniform bytes (RFC 9496 element derivation).
  fromUniform: (bytes64) => RistrettoPoint.hashToCurve(bytes64),
  // A canonical 32-byte encoding; throws on anything that is not one.
  decode: (bytes32) => RistrettoPoint.fromHex(bytes32),
  encode: (p) => p.toRawBytes(),
  multiply: (p, k) => p.multiply(k),
  isIdentity: (p) => p.equals(ZERO),
});
