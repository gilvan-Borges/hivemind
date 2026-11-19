package relaywire

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"

	"golang.org/x/crypto/nacl/box"
)

// KeyPair is an X25519 keypair. The private half never leaves the device that
// generated it. The relay stores only public keys.
type KeyPair struct {
	Public  [32]byte
	Private [32]byte
}

// BoxPayload is the JSON object placed in Envelope.Payload when Enc is
// EncNaClBox. Nonce and Ciphertext are unpadded base64 (RFC 4648 §5).
type BoxPayload struct {
	Nonce      string `json:"nonce"`
	Ciphertext string `json:"ciphertext"`
}

// NewKeyPair generates an X25519 keypair.
func NewKeyPair() (*KeyPair, error) {
	pub, priv, err := box.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	return &KeyPair{Public: *pub, Private: *priv}, nil
}

// PublicB64 encodes the public key.
func (k *KeyPair) PublicB64() string {
	if k == nil {
		return ""
	}
	return EncodeKey(k.Public)
}

// EncodeKey encodes a 32-byte public key.
func EncodeKey(pub [32]byte) string {
	return base64.RawURLEncoding.EncodeToString(pub[:])
}

// ParseKey decodes a public key from standard or raw-url base64.
func ParseKey(s string) ([32]byte, error) {
	var out [32]byte
	s = trimSpace(s)
	if s == "" {
		return out, errors.New("chave pública vazia")
	}
	var raw []byte
	var err error
	if raw, err = base64.RawURLEncoding.DecodeString(s); err != nil || len(raw) != 32 {
		raw, err = base64.StdEncoding.DecodeString(s)
	}
	if err != nil || len(raw) != 32 {
		return out, errors.New("chave pública inválida")
	}
	copy(out[:], raw)
	return out, nil
}

func trimSpace(s string) string {
	i, j := 0, len(s)
	for i < j && (s[i] == ' ' || s[i] == '\n' || s[i] == '\t' || s[i] == '\r') {
		i++
	}
	for j > i && (s[j-1] == ' ' || s[j-1] == '\n' || s[j-1] == '\t' || s[j-1] == '\r') {
		j--
	}
	return s[i:j]
}

// Seal encrypts plaintext for peerPub. The plaintext is prefixed with version
// byte 0x01 so the format can evolve. Each call draws a fresh 24-byte nonce.
func (k *KeyPair) Seal(peerPub [32]byte, plaintext []byte) (BoxPayload, error) {
	if k == nil {
		return BoxPayload{}, errors.New("sem chave local")
	}
	var nonce [24]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return BoxPayload{}, err
	}
	msg := make([]byte, 1+len(plaintext))
	msg[0] = 0x01
	copy(msg[1:], plaintext)
	ct := box.Seal(nil, msg, &nonce, &peerPub, &k.Private)
	return BoxPayload{
		Nonce:      base64.RawURLEncoding.EncodeToString(nonce[:]),
		Ciphertext: base64.RawURLEncoding.EncodeToString(ct),
	}, nil
}

// Open decrypts a box sealed by the peer.
func (k *KeyPair) Open(peerPub [32]byte, p BoxPayload) ([]byte, error) {
	if k == nil {
		return nil, errors.New("sem chave local")
	}
	nonceRaw, err := base64.RawURLEncoding.DecodeString(p.Nonce)
	if err != nil || len(nonceRaw) != 24 {
		nonceRaw, err = base64.StdEncoding.DecodeString(p.Nonce)
	}
	if err != nil || len(nonceRaw) != 24 {
		return nil, errors.New("nonce inválido")
	}
	ct, err := base64.RawURLEncoding.DecodeString(p.Ciphertext)
	if err != nil {
		ct, err = base64.StdEncoding.DecodeString(p.Ciphertext)
	}
	if err != nil {
		return nil, errors.New("ciphertext inválido")
	}
	var nonce [24]byte
	copy(nonce[:], nonceRaw)
	opened, ok := box.Open(nil, ct, &nonce, &peerPub, &k.Private)
	if !ok || len(opened) < 1 || opened[0] != 0x01 {
		return nil, errors.New("falha ao abrir naclbox")
	}
	return opened[1:], nil
}

// SealJSON encrypts the JSON encoding of v.
func (k *KeyPair) SealJSON(peerPub [32]byte, v any) (json.RawMessage, error) {
	plain, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	p, err := k.Seal(peerPub, plain)
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(p)
	if err != nil {
		return nil, err
	}
	return raw, nil
}

// OpenJSON decrypts payload into dst.
func (k *KeyPair) OpenJSON(peerPub [32]byte, payload []byte, dst any) error {
	var p BoxPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return fmt.Errorf("payload cifrado inválido: %w", err)
	}
	plain, err := k.Open(peerPub, p)
	if err != nil {
		return err
	}
	if dst == nil {
		return nil
	}
	return json.Unmarshal(plain, dst)
}
