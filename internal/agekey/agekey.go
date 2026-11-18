package agekey

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"math/big"
	"strings"
)

const (
	hrp           = "age"
	keySize       = 32
	sshEd25519    = "ssh-ed25519"
	bech32Charset = "qpzry9x8gf2tvdw0s3jn54khce6mua7l"
)

var (
	fieldPrime   = new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 255), big.NewInt(19))
	bech32Values = func() map[byte]byte {
		values := make(map[byte]byte, len(bech32Charset))
		for i := range byte(len(bech32Charset)) {
			values[bech32Charset[i]] = i
		}
		return values
	}()
)

type Recipient string

func ParseRecipient(s string) (Recipient, error) {
	gotHRP, data, err := bech32Decode(s)
	if err != nil {
		return "", fmt.Errorf("parsing age recipient %q: %w", s, err)
	}
	if gotHRP != hrp {
		return "", fmt.Errorf("parsing age recipient %q: prefix %q, want %q", s, gotHRP, hrp)
	}
	key, err := convertBits(data, 5, 8, false)
	if err != nil {
		return "", fmt.Errorf("parsing age recipient %q: %w", s, err)
	}
	if len(key) != keySize {
		return "", fmt.Errorf("parsing age recipient %q: %d-byte key, want %d", s, len(key), keySize)
	}
	return Recipient(s), nil
}

func FromSSHPublicKey(authorizedKey []byte) (Recipient, error) {
	fields := strings.Fields(string(authorizedKey))
	if len(fields) < 2 || fields[0] != sshEd25519 {
		return "", fmt.Errorf("not an %s public key", sshEd25519)
	}
	blob, err := base64.StdEncoding.DecodeString(fields[1])
	if err != nil {
		return "", fmt.Errorf("decoding the %s key: %w", sshEd25519, err)
	}
	keyType, rest, err := sshString(blob)
	if err != nil || string(keyType) != sshEd25519 {
		return "", fmt.Errorf("decoding the %s key: malformed key blob", sshEd25519)
	}
	point, rest, err := sshString(rest)
	if err != nil || len(point) != keySize || len(rest) != 0 {
		return "", fmt.Errorf("decoding the %s key: malformed key blob", sshEd25519)
	}
	u, err := edwardsToMontgomery(point)
	if err != nil {
		return "", err
	}
	data, err := convertBits(u, 8, 5, true)
	if err != nil {
		return "", err
	}
	return Recipient(bech32Encode(hrp, data)), nil
}

func sshString(b []byte) (value, rest []byte, err error) {
	if len(b) < 4 {
		return nil, nil, errors.New("truncated")
	}
	n, rest := int64(binary.BigEndian.Uint32(b)), b[4:]
	if n > int64(len(rest)) {
		return nil, nil, errors.New("truncated")
	}
	return rest[:n], rest[n:], nil
}

func edwardsToMontgomery(point []byte) ([]byte, error) {
	le := bytes.Clone(point)
	le[keySize-1] &= 0x7f
	y := new(big.Int).SetBytes(reverse(le))
	if y.Cmp(fieldPrime) >= 0 {
		return nil, errors.New("ed25519 key is not a canonical point encoding")
	}
	one := big.NewInt(1)
	denominator := new(big.Int).Mod(new(big.Int).Sub(one, y), fieldPrime)
	if denominator.Sign() == 0 {
		return nil, errors.New("ed25519 key has no X25519 equivalent")
	}
	u := new(big.Int).Add(one, y)
	u.Mul(u, new(big.Int).ModInverse(denominator, fieldPrime))
	u.Mod(u, fieldPrime)
	return reverse(u.FillBytes(make([]byte, keySize))), nil
}

func reverse(b []byte) []byte {
	out := make([]byte, len(b))
	for i, v := range b {
		out[len(b)-1-i] = v
	}
	return out
}

func polymod(values []byte) uint32 {
	generator := [5]uint32{0x3b6a57b2, 0x26508e6d, 0x1ea119fa, 0x3d4233dd, 0x2a1462b3}
	chk := uint32(1)
	for _, v := range values {
		top := chk >> 25
		chk = (chk&0x1ffffff)<<5 ^ uint32(v)
		for i, g := range generator {
			if (top>>i)&1 == 1 {
				chk ^= g
			}
		}
	}
	return chk
}

func hrpExpand(h string) []byte {
	out := make([]byte, 0, 2*len(h)+1)
	for i := range len(h) {
		out = append(out, h[i]>>5)
	}
	out = append(out, 0)
	for i := range len(h) {
		out = append(out, h[i]&31)
	}
	return out
}

func bech32Encode(h string, data []byte) string {
	values := append(hrpExpand(h), data...)
	mod := polymod(append(values, 0, 0, 0, 0, 0, 0)) ^ 1
	var b strings.Builder
	b.Grow(len(h) + 1 + len(data) + 6)
	b.WriteString(h)
	b.WriteByte('1')
	for _, d := range data {
		b.WriteByte(bech32Charset[d])
	}
	for i := range 6 {
		b.WriteByte(bech32Charset[(mod>>(5*(5-i)))&31])
	}
	return b.String()
}

func bech32Decode(s string) (string, []byte, error) {
	if strings.ToLower(s) != s && strings.ToUpper(s) != s {
		return "", nil, errors.New("mixed case")
	}
	s = strings.ToLower(s)
	sep := strings.LastIndexByte(s, '1')
	if sep < 1 || sep+7 > len(s) {
		return "", nil, errors.New("not bech32: missing separator or checksum")
	}
	h := s[:sep]
	data := make([]byte, 0, len(s)-sep-1)
	for i := sep + 1; i < len(s); i++ {
		d, ok := bech32Values[s[i]]
		if !ok {
			return "", nil, fmt.Errorf("not bech32: invalid character %q", s[i])
		}
		data = append(data, d)
	}
	if polymod(append(hrpExpand(h), data...)) != 1 {
		return "", nil, errors.New("bad checksum")
	}
	return h, data[:len(data)-6], nil
}

func convertBits(data []byte, from, to uint, pad bool) ([]byte, error) {
	var acc uint32
	var bits uint
	maxValue := uint32(1)<<to - 1
	out := make([]byte, 0, len(data)*int(from)/int(to)+1)
	for _, v := range data {
		if uint32(v)>>from != 0 {
			return nil, fmt.Errorf("invalid %d-bit value %d", from, v)
		}
		acc = acc<<from | uint32(v)
		bits += from
		for bits >= to {
			bits -= to
			out = append(out, byte(acc>>bits&maxValue)) //nolint:gosec
		}
	}
	if pad {
		if bits > 0 {
			out = append(out, byte(acc<<(to-bits)&maxValue)) //nolint:gosec
		}
		return out, nil
	}
	if bits >= from || acc<<(to-bits)&maxValue != 0 {
		return nil, errors.New("invalid padding")
	}
	return out, nil
}
