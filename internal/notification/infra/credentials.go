package infra

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"

	"github.com/real-uangi/posthaste/internal/notification/domain"
	"github.com/real-uangi/posthaste/internal/shared/config"
)

type CredentialCipher struct {
	aead cipher.AEAD
}

func NewCredentialCipher(cfg *config.Config) (*CredentialCipher, error) {
	block, err := aes.NewCipher(cfg.EncryptionKey)
	if err != nil {
		return nil, domain.ErrUnavailable
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, domain.ErrUnavailable
	}
	return &CredentialCipher{aead: aead}, nil
}

func (c *CredentialCipher) Encrypt(accountID string, credentials []byte) ([]byte, error) {
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, domain.ErrUnavailable
	}
	return c.aead.Seal(nonce, nonce, credentials, []byte(accountID)), nil
}

func (c *CredentialCipher) Decrypt(accountID string, ciphertext []byte) ([]byte, error) {
	nonceSize := c.aead.NonceSize()
	if len(ciphertext) < nonceSize+c.aead.Overhead() {
		return nil, domain.ErrUnavailable
	}
	plaintext, err := c.aead.Open(nil, ciphertext[:nonceSize], ciphertext[nonceSize:], []byte(accountID))
	if err != nil {
		return nil, domain.ErrUnavailable
	}
	return plaintext, nil
}
