// Package nostr implements the Nostr platform adapter.
//
// This file implements secp256k1 and BIP-340 Schnorr signatures from scratch
// using math/big arithmetic. Performance is deliberately traded for
// simplicity: Nostr DM traffic here is low-frequency, so affine point
// arithmetic with modular inverses is perfectly adequate.
//
// The implementation follows the BIP-340 reference algorithms exactly
// (d'/n range check, even-Y key negation, tagged hashes "BIP0340/aux" and
// "BIP0340/challenge", R = k'*G with even-Y normalization). Correctness is
// pinned by the official BIP-340 test vectors in secp256k1_test.go.
package nostr

import (
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"math/big"
)

var (
	// secp256k1 field prime: p = 2^256 - 2^32 - 977
	secpP = mustBig("fffffffffffffffffffffffffffffffffffffffffffffffffffffffefffffc2f")
	// group order n
	secpN = mustBig("fffffffffffffffffffffffffffffffebaaedce6af48a03bbfd25e8cd0364141")
	// curve equation y^2 = x^3 + 7
	secpB = big.NewInt(7)

	// Generator point G.
	secpGx = mustBig("79be667ef9dcbbac55a06295ce870b07029bfcdb2dce28d959f2815b16f81798")
	secpGy = mustBig("483ada7726a3c4655da4fbfc0e1108a8fd17b448a68554199c47d08ffb10d4b8")

	errPointNotOnCurve = errors.New("secp256k1: point not on curve")
	errLiftX           = errors.New("secp256k1: invalid x coordinate (not a curve point)")
	errInvalidSeckey   = errors.New("secp256k1: invalid secret key")
	errSignatureFailed = errors.New("secp256k1: signing failed (probabilistic failure)")
)

func mustBig(hex string) *big.Int {
	v, ok := new(big.Int).SetString(hex, 16)
	if !ok {
		panic("secp256k1: bad constant " + hex)
	}
	return v
}

// point is an affine curve point; nil x/y represents the point at infinity.
type point struct {
	x, y *big.Int
}

// mod computes (a mod p) with a always non-negative (big.Int Mod is Euclidean
// for positive modulus, so this is already non-negative).
func mod(a, p *big.Int) *big.Int {
	return new(big.Int).Mod(a, p)
}

// modInv computes a^-1 mod p via Fermat's little theorem (p is prime).
func modInv(a, p *big.Int) *big.Int {
	exp := new(big.Int).Sub(p, big.NewInt(2))
	return new(big.Int).Exp(mod(a, p), exp, p)
}

func isOnCurve(pt *point) bool {
	if pt == nil || pt.x == nil {
		return false
	}
	lhs := mod(new(big.Int).Mul(pt.y, pt.y), secpP)
	rhs := mod(new(big.Int).Add(
		new(big.Int).Exp(pt.x, big.NewInt(3), secpP), secpB), secpP)
	return lhs.Cmp(rhs) == 0
}

// pointDouble returns 2*pt (pt must not be infinity and y must not be 0).
func pointDouble(pt *point) *point {
	if pt == nil || pt.x == nil {
		return &point{}
	}
	// lambda = 3x^2 / 2y
	num := mod(new(big.Int).Mul(big.NewInt(3),
		new(big.Int).Mul(pt.x, pt.x)), secpP)
	den := modInv(new(big.Int).Mul(big.NewInt(2), pt.y), secpP)
	lambda := mod(new(big.Int).Mul(num, den), secpP)

	x3 := mod(new(big.Int).Sub(
		new(big.Int).Mul(lambda, lambda),
		new(big.Int).Mul(big.NewInt(2), pt.x)), secpP)
	y3 := mod(new(big.Int).Sub(
		new(big.Int).Mul(lambda, new(big.Int).Sub(pt.x, x3)), pt.y), secpP)
	return &point{x: x3, y: y3}
}

// pointAdd returns p1 + p2 (either may be infinity).
func pointAdd(p1, p2 *point) *point {
	if p1 == nil || p1.x == nil {
		if p2 == nil {
			return &point{}
		}
		return &point{x: new(big.Int).Set(p2.x), y: new(big.Int).Set(p2.y)}
	}
	if p2 == nil || p2.x == nil {
		return &point{x: new(big.Int).Set(p1.x), y: new(big.Int).Set(p1.y)}
	}
	if p1.x.Cmp(p2.x) == 0 {
		if p1.y.Cmp(p2.y) != 0 {
			return &point{} // additive inverse: infinity
		}
		return pointDouble(p1)
	}
	// lambda = (y2 - y1) / (x2 - x1)
	num := mod(new(big.Int).Sub(p2.y, p1.y), secpP)
	den := modInv(mod(new(big.Int).Sub(p2.x, p1.x), secpP), secpP)
	lambda := mod(new(big.Int).Mul(num, den), secpP)

	x3 := mod(new(big.Int).Sub(
		new(big.Int).Sub(new(big.Int).Mul(lambda, lambda), p1.x), p2.x), secpP)
	y3 := mod(new(big.Int).Sub(
		new(big.Int).Mul(lambda, new(big.Int).Sub(p1.x, x3)), p1.y), secpP)
	return &point{x: x3, y: y3}
}

// scalarMult returns k*pt via double-and-add over big.Int bits.
func scalarMult(pt *point, k *big.Int) *point {
	res := &point{} // infinity
	base := &point{x: new(big.Int).Set(pt.x), y: new(big.Int).Set(pt.y)}
	kk := new(big.Int).Mod(k, secpN)
	for i := kk.BitLen() - 1; i >= 0; i-- {
		res = pointDouble(res)
		if kk.Bit(i) == 1 {
			res = pointAdd(res, base)
		}
	}
	return res
}

// scalarBaseMult returns k*G.
func scalarBaseMult(k *big.Int) *point {
	return scalarMult(&point{x: new(big.Int).Set(secpGx), y: new(big.Int).Set(secpGy)}, k)
}

// hasEvenY reports whether the point's y coordinate is even (BIP-340 parity).
func hasEvenY(pt *point) bool {
	return pt != nil && pt.x != nil && pt.y.Bit(0) == 0
}

// liftX converts a 32-byte x-only public key into a curve point with even y.
// Per BIP-340 lift_x: fail if x >= p or x^3+7 is a non-residue mod p.
func liftX(b []byte) (*point, error) {
	if len(b) != 32 {
		return nil, fmt.Errorf("%w: length %d", errLiftX, len(b))
	}
	x := new(big.Int).SetBytes(b)
	if x.Cmp(secpP) >= 0 {
		return nil, errLiftX
	}
	ySq := mod(new(big.Int).Add(
		new(big.Int).Exp(x, big.NewInt(3), secpP), secpB), secpP)
	// p ≡ 3 mod 4, so the square root is ySq^((p+1)/4); check correctness.
	exp := new(big.Int).Add(secpP, big.NewInt(1))
	exp.Rsh(exp, 2)
	y := new(big.Int).Exp(ySq, exp, secpP)
	if mod(new(big.Int).Mul(y, y), secpP).Cmp(ySq) != 0 {
		return nil, errLiftX
	}
	if y.Bit(0) != 0 { // lift_x returns the even-y point
		y.Sub(secpP, y)
	}
	return &point{x: x, y: y}, nil
}

// xOnlyPubkey derives the 32-byte x-only public key for a 32-byte secret key.
func xOnlyPubkey(seckey []byte) ([]byte, error) {
	d, _, err := adjustedPrivkey(seckey)
	if err != nil {
		return nil, err
	}
	P := scalarBaseMult(d)
	return pad32(P.x), nil
}

// adjustedPrivkey validates the secret key and returns the BIP-340 adjusted
// scalar d (negated when the base point has odd y) along with P = d*G.
func adjustedPrivkey(seckey []byte) (*big.Int, *point, error) {
	if len(seckey) != 32 {
		return nil, nil, fmt.Errorf("%w: length %d", errInvalidSeckey, len(seckey))
	}
	d0 := new(big.Int).SetBytes(seckey)
	if d0.Sign() == 0 || d0.Cmp(secpN) >= 0 {
		return nil, nil, errInvalidSeckey
	}
	P := scalarBaseMult(d0)
	if !isOnCurve(P) {
		return nil, nil, errPointNotOnCurve
	}
	d := new(big.Int).Set(d0)
	if !hasEvenY(P) {
		d.Sub(secpN, d)
	}
	return d, P, nil
}

// taggedHash computes SHA256(SHA256(tag) || SHA256(tag) || msg).
func taggedHash(tag string, parts ...[]byte) []byte {
	tagHash := sha256.Sum256([]byte(tag))
	h := sha256.New()
	h.Write(tagHash[:])
	h.Write(tagHash[:])
	for _, p := range parts {
		h.Write(p)
	}
	return h.Sum(nil)
}

// pad32 left-pads a big.Int into a fixed 32-byte big-endian representation.
func pad32(v *big.Int) []byte {
	out := make([]byte, 32)
	b := v.Bytes()
	copy(out[32-len(b):], b)
	return out
}

// bip340Sign computes a BIP-340 Schnorr signature over msg (any length; Nostr
// always uses 32-byte event ids) using seckey and auxRand (both must be 32
// bytes), following the reference algorithm.
func bip340Sign(msg, seckey, auxRand []byte) ([]byte, error) {
	if len(auxRand) != 32 {
		return nil, fmt.Errorf("secp256k1: aux_rand must be 32 bytes, got %d", len(auxRand))
	}

	d, P, err := adjustedPrivkey(seckey)
	if err != nil {
		return nil, err
	}

	// t = d XOR int(tag_hash("BIP0340/aux", aux_rand))
	auxInt := new(big.Int).SetBytes(taggedHash("BIP0340/aux", auxRand))
	t := new(big.Int).Xor(d, auxInt)

	// rand = SHA256(tag_hash("BIP0340/nonce", t || bytes(P) || m)) — note the
	// public key point, not the scalar d, per the BIP-340 reference.
	rand := taggedHash("BIP0340/nonce", pad32(t), pad32(P.x), msg)

	// k' = int(rand) mod n; fail if 0
	k0 := new(big.Int).Mod(new(big.Int).SetBytes(rand), secpN)
	if k0.Sign() == 0 {
		return nil, errSignatureFailed
	}
	R := scalarBaseMult(k0)

	// k = k' or n - k' depending on R's y parity
	k := new(big.Int).Set(k0)
	if !hasEvenY(R) {
		k.Sub(secpN, k)
	}

	// e = int(tag_hash("BIP0340/challenge", bytes(R) || bytes(P) || m)) mod n
	e := new(big.Int).Mod(new(big.Int).SetBytes(
		taggedHash("BIP0340/challenge", pad32(R.x), pad32(P.x), msg)), secpN)

	// sig = bytes(R) || bytes(k + e*d mod n)
	ks := mod(new(big.Int).Add(k, new(big.Int).Mul(e, d)), secpN)
	if ks.Sign() == 0 {
		return nil, errSignatureFailed
	}

	sig := make([]byte, 64)
	copy(sig[:32], pad32(R.x))
	copy(sig[32:], pad32(ks))
	return sig, nil
}

// bip340Verify checks a 64-byte BIP-340 signature over msg (any length) for a
// 32-byte x-only public key.
func bip340Verify(msg, pubkey32, sig64 []byte) bool {
	if len(pubkey32) != 32 || len(sig64) != 64 {
		return false
	}
	P, err := liftX(pubkey32)
	if err != nil {
		return false
	}
	r := new(big.Int).SetBytes(sig64[:32])
	if r.Cmp(secpP) >= 0 {
		return false
	}
	s := new(big.Int).SetBytes(sig64[32:])
	if s.Cmp(secpN) >= 0 {
		return false
	}
	// The challenge incorporates r = sig[0:32] as the expected R.x.
	e := new(big.Int).Mod(new(big.Int).SetBytes(
		taggedHash("BIP0340/challenge", sig64[:32], pubkey32, msg)), secpN)

	// R = s*G - e*P  (implemented as s*G + (n-e)*P; n-e == 0 yields infinity)
	sG := scalarBaseMult(s)
	negE := new(big.Int).Sub(secpN, e)
	eP := scalarMult(P, negE)
	R := pointAdd(sG, eP)

	if R == nil || R.x == nil {
		return false // infinite point
	}
	if !hasEvenY(R) || R.x.Cmp(r) != 0 {
		return false
	}
	return true
}

// GenerateSeckey returns a fresh random secp256k1 secret key.
func GenerateSeckey() ([]byte, error) {
	for i := 0; i < 64; i++ {
		k := make([]byte, 32)
		if _, err := rand.Read(k); err != nil {
			return nil, fmt.Errorf("secp256k1: rand: %w", err)
		}
		v := new(big.Int).SetBytes(k)
		if v.Sign() != 0 && v.Cmp(secpN) < 0 {
			return k, nil
		}
	}
	return nil, errSignatureFailed
}
