package auth

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"net/http"
	"testing"
)

// softAuthenticator is a minimal software WebAuthn authenticator (ES256 key,
// "none" attestation) that drives real ceremonies through go-webauthn in tests.
type softAuthenticator struct {
	t       *testing.T
	rpID    string
	origin  string
	key     *ecdsa.PrivateKey
	credID  []byte
	counter uint32
}

var b64url = base64.RawURLEncoding

func newSoftAuthenticator(t *testing.T, rpID, origin string) *softAuthenticator {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	id := make([]byte, 16)
	if _, err := rand.Read(id); err != nil {
		t.Fatal(err)
	}
	return &softAuthenticator{t: t, rpID: rpID, origin: origin, key: key, credID: id}
}

func cborHead(major byte, n uint64) []byte {
	switch {
	case n < 24:
		return []byte{major<<5 | byte(n)}
	case n < 1<<8:
		return []byte{major<<5 | 24, byte(n)}
	case n < 1<<16:
		return []byte{major<<5 | 25, byte(n >> 8), byte(n)}
	default:
		return []byte{major<<5 | 26, byte(n >> 24), byte(n >> 16), byte(n >> 8), byte(n)}
	}
}

func cborInt(v int64) []byte {
	if v >= 0 {
		return cborHead(0, uint64(v))
	}
	return cborHead(1, uint64(-1-v))
}

func cborBytes(b []byte) []byte { return append(cborHead(2, uint64(len(b))), b...) }
func cborText(s string) []byte  { return append(cborHead(3, uint64(len(s))), s...) }

func cborMap(kv ...[]byte) []byte {
	out := cborHead(5, uint64(len(kv)/2))
	for _, x := range kv {
		out = append(out, x...)
	}
	return out
}

func (a *softAuthenticator) coseKey() []byte {
	pub, err := a.key.PublicKey.ECDH()
	if err != nil {
		a.t.Fatal(err)
	}
	raw := pub.Bytes() // 0x04 || X(32) || Y(32)
	return cborMap(
		cborInt(1), cborInt(2), // kty: EC2
		cborInt(3), cborInt(-7), // alg: ES256
		cborInt(-1), cborInt(1), // crv: P-256
		cborInt(-2), cborBytes(raw[1:33]),
		cborInt(-3), cborBytes(raw[33:65]),
	)
}

func challengeOf(t *testing.T, options []byte) string {
	t.Helper()
	var o struct {
		PublicKey struct {
			Challenge string `json:"challenge"`
		} `json:"publicKey"`
	}
	if err := json.Unmarshal(options, &o); err != nil || o.PublicKey.Challenge == "" {
		t.Fatalf("options without challenge: %s (%v)", options, err)
	}
	return o.PublicKey.Challenge
}

func (a *softAuthenticator) clientData(typ, challenge string) []byte {
	b, err := json.Marshal(map[string]any{"type": typ, "challenge": challenge, "origin": a.origin, "crossOrigin": false})
	if err != nil {
		a.t.Fatal(err)
	}
	return b
}

func (a *softAuthenticator) marshal(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		a.t.Fatal(err)
	}
	return b
}

// register answers navigator.credentials.create() for the given options JSON.
func (a *softAuthenticator) register(options []byte) json.RawMessage {
	a.t.Helper()
	cd := a.clientData("webauthn.create", challengeOf(a.t, options))
	rpHash := sha256.Sum256([]byte(a.rpID))
	ad := append([]byte{}, rpHash[:]...)
	ad = append(ad, 0x45) // flags: UP | UV | AT
	ad = binary.BigEndian.AppendUint32(ad, a.counter)
	ad = append(ad, make([]byte, 16)...) // AAGUID
	ad = binary.BigEndian.AppendUint16(ad, uint16(len(a.credID)))
	ad = append(ad, a.credID...)
	ad = append(ad, a.coseKey()...)
	att := cborMap(cborText("fmt"), cborText("none"), cborText("attStmt"), cborMap(), cborText("authData"), cborBytes(ad))
	return a.marshal(map[string]any{
		"id": b64url.EncodeToString(a.credID), "rawId": b64url.EncodeToString(a.credID), "type": "public-key",
		"authenticatorAttachment": "platform", "clientExtensionResults": map[string]any{},
		"response": map[string]any{
			"clientDataJSON":    b64url.EncodeToString(cd),
			"attestationObject": b64url.EncodeToString(att),
			"transports":        []string{"internal"},
		},
	})
}

// assert answers navigator.credentials.get(); the sign counter increases by one per call.
func (a *softAuthenticator) assert(options []byte) json.RawMessage {
	a.t.Helper()
	cd := a.clientData("webauthn.get", challengeOf(a.t, options))
	a.counter++
	rpHash := sha256.Sum256([]byte(a.rpID))
	ad := append([]byte{}, rpHash[:]...)
	ad = append(ad, 0x05) // flags: UP | UV
	ad = binary.BigEndian.AppendUint32(ad, a.counter)
	cdHash := sha256.Sum256(cd)
	digest := sha256.Sum256(append(append([]byte{}, ad...), cdHash[:]...))
	sig, err := ecdsa.SignASN1(rand.Reader, a.key, digest[:])
	if err != nil {
		a.t.Fatal(err)
	}
	return a.marshal(map[string]any{
		"id": b64url.EncodeToString(a.credID), "rawId": b64url.EncodeToString(a.credID), "type": "public-key",
		"authenticatorAttachment": "platform", "clientExtensionResults": map[string]any{},
		"response": map[string]any{
			"clientDataJSON":    b64url.EncodeToString(cd),
			"authenticatorData": b64url.EncodeToString(ad),
			"signature":         b64url.EncodeToString(sig),
		},
	})
}

// registerPasskey runs begin + finish for c with a new software authenticator.
func (e *testEnv) registerPasskey(c *client, name string) (*softAuthenticator, response) {
	e.t.Helper()
	a := newSoftAuthenticator(e.t, e.cfg.RPID(), e.cfg.Origin())
	begin := c.do(http.MethodPost, "/api/me/passkeys/begin", nil)
	if begin.code != 200 {
		e.t.Fatalf("passkey begin = %d %s", begin.code, begin.body)
	}
	return a, c.do(http.MethodPost, "/api/me/passkeys/finish", map[string]any{"name": name, "credential": a.register(begin.body)})
}
