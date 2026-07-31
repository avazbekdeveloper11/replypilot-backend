// Package crypto provides application-layer envelope encryption for secrets
// that must be stored at rest — currently, Instagram long-lived access
// tokens (database/schema.sql: instagram_accounts.access_token_encrypted).
// Postgres/disk encryption alone is not enough for a token that grants
// messaging access to a customer's real business account: this is the
// second layer, so a DB backup leak or a misconfigured read replica doesn't
// hand out usable tokens.
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"io"
)

var (
	ErrCiphertextTooShort = errors.New("crypto: ciphertext shorter than nonce size")
	ErrInvalidKeySize     = errors.New("crypto: key must be exactly 32 bytes for AES-256")
)

// AESGCMEncryptor performs AES-256-GCM authenticated encryption. The key
// passed in is treated as a raw data-encryption key (DEK). In production,
// this DEK should itself be wrapped by a KMS master key (AWS KMS / GCP KMS /
// Vault transit engine) rather than sourced directly from an environment
// variable — that key-wrapping step is an infra/ops concern intentionally
// left out of this package so it stays testable without a live KMS.
type AESGCMEncryptor struct {
	gcm cipher.AEAD
}

func NewAESGCMEncryptor(key []byte) (*AESGCMEncryptor, error) {
	if len(key) != 32 {
		return nil, ErrInvalidKeySize
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}

	return &AESGCMEncryptor{gcm: gcm}, nil
}

// Encrypt returns nonce||ciphertext||tag as a single byte slice, ready to
// store directly in a bytea column.
func (e *AESGCMEncryptor) Encrypt(plaintext string) ([]byte, error) {
	nonce := make([]byte, e.gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	return e.gcm.Seal(nonce, nonce, []byte(plaintext), nil), nil
}

func (e *AESGCMEncryptor) Decrypt(ciphertext []byte) (string, error) {
	nonceSize := e.gcm.NonceSize()
	if len(ciphertext) < nonceSize {
		return "", ErrCiphertextTooShort
	}

	nonce, data := ciphertext[:nonceSize], ciphertext[nonceSize:]
	plaintext, err := e.gcm.Open(nil, nonce, data, nil)
	if err != nil {
		return "", err
	}

	return string(plaintext), nil
}
