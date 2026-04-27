package config

import (
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strings"
)

// obfuscateHeader is the literal first line of every obfuscated credential
// file. Plain text by design so users can grep their config dir and learn
// what the file is. The "not encrypted" wording is intentional — this layer
// defends against accidental disclosure to LLM tools that read files
// verbatim, not against a determined attacker.
const obfuscateHeader = "# shell3-obfuscated-v1 — not encrypted; do not paste contents"

// obfuscateKey is a 32-byte repeating key derived from a fixed string.
var obfuscateKey = func() []byte {
	sum := sha256.Sum256([]byte("shell3-creds-obfuscation-v1"))
	return sum[:]
}()

// Wrap obfuscates plaintext into the on-disk format.
func Wrap(plaintext []byte) []byte {
	xored := make([]byte, len(plaintext))
	for i, b := range plaintext {
		xored[i] = b ^ obfuscateKey[i%len(obfuscateKey)]
	}
	encoded := base64.StdEncoding.EncodeToString(xored)
	return []byte(obfuscateHeader + "\n" + encoded)
}

// Unwrap reverses Wrap.
func Unwrap(blob []byte) ([]byte, error) {
	s := string(blob)
	if !strings.HasPrefix(s, obfuscateHeader) {
		return nil, errors.New("config: obfuscated file: missing magic header")
	}
	body := strings.TrimPrefix(s, obfuscateHeader)
	body = strings.TrimLeft(body, "\r\n")
	xored, err := base64.StdEncoding.DecodeString(strings.TrimSpace(body))
	if err != nil {
		return nil, err
	}
	out := make([]byte, len(xored))
	for i, b := range xored {
		out[i] = b ^ obfuscateKey[i%len(obfuscateKey)]
	}
	return out, nil
}
