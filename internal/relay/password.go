package relay

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// Argon holds argon2id parameters. Memory is in KiB, matching golang.org/x/crypto/argon2.
// The production default (19 MiB, t=2, p=1) follows the OWASP argon2id floor and
// stays small enough for a 1 GB VM when hashing is serialized.
type Argon struct {
	Time    uint32
	Memory  uint32
	Threads uint8
	KeyLen  uint32
}

// DefaultArgon is the production parameter set.
func DefaultArgon() Argon {
	return Argon{Time: 2, Memory: 19 * 1024, Threads: 1, KeyLen: 32}
}

const (
	minPasswordLen = 8
	maxPasswordLen = 200
)

func (p Argon) normalize() (Argon, error) {
	if p.Time == 0 {
		p.Time = 2
	}
	if p.Memory == 0 {
		p.Memory = 19 * 1024
	}
	if p.Threads == 0 {
		p.Threads = 1
	}
	if p.KeyLen == 0 {
		p.KeyLen = 32
	}
	if p.Memory < 8 || p.KeyLen < 16 {
		return Argon{}, errors.New("parâmetros argon2id fracos demais")
	}
	return p, nil
}

// HashPassword returns a PHC-style argon2id string.
func HashPassword(password string, params Argon) (string, error) {
	if err := CheckPassword(password); err != nil {
		return "", err
	}
	params, err := params.normalize()
	if err != nil {
		return "", err
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	sum := argon2.IDKey([]byte(password), salt, params.Time, params.Memory, params.Threads, params.KeyLen)
	return fmt.Sprintf("$argon2id$v=19$m=%d,t=%d,p=%d$%s$%s",
		params.Memory, params.Time, params.Threads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(sum),
	), nil
}

// CheckPassword enforces the length policy before any hash work.
func CheckPassword(password string) error {
	n := len(password)
	if n < minPasswordLen {
		return fmt.Errorf("senha precisa ter pelo menos %d caracteres", minPasswordLen)
	}
	if n > maxPasswordLen {
		return fmt.Errorf("senha passa de %d caracteres", maxPasswordLen)
	}
	return nil
}

// VerifyPassword checks a password against a PHC string. The comparison of the
// derived key is constant-time. Callers should run this even when the account
// does not exist (against a dummy hash) so missing emails are not faster.
func VerifyPassword(password, encoded string) (bool, error) {
	if password == "" || len(password) > maxPasswordLen {
		return false, nil
	}
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" || parts[2] != "v=19" {
		return false, errors.New("hash de senha inválido")
	}
	var memory uint32
	var timeCost uint32
	var threads uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &timeCost, &threads); err != nil {
		return false, errors.New("parâmetros de hash inválidos")
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false, err
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return false, err
	}
	sum := argon2.IDKey([]byte(password), salt, timeCost, memory, threads, uint32(len(want)))
	return subtle.ConstantTimeCompare(sum, want) == 1, nil
}
