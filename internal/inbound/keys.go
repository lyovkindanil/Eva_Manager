package inbound

import (
	"crypto/ecdh"
	"crypto/mlkem"
	"crypto/rand"
	"encoding/hex"
	"fmt"
)

// NewRealityKeypair — пара X25519 для Reality: приватный ключ (в конфиг
// Xray) и публичный (в ссылки), base64url. X25519 сам приводит скаляр,
// поэтому публичный ключ совпадает с тем, что вывел бы `xray x25519`.
func NewRealityKeypair() (priv, pub string, err error) {
	key, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return "", "", fmt.Errorf("ключи reality: %w", err)
	}
	return b64.EncodeToString(key.Bytes()), b64.EncodeToString(key.PublicKey().Bytes()), nil
}

// NewShortID — short id для Reality: 8 байт в hex.
func NewShortID() (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("short id: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// NewEncryptionSeed — пара ML-KEM-768 для VLESS Encryption: seed
// (приватный, в конфиг Xray) и публичный ключ (в ссылки), base64url.
func NewEncryptionSeed() (seed, pub string, err error) {
	key, err := mlkem.GenerateKey768()
	if err != nil {
		return "", "", fmt.Errorf("ключи vless encryption: %w", err)
	}
	return b64.EncodeToString(key.Bytes()), b64.EncodeToString(key.EncapsulationKey().Bytes()), nil
}
