package syncclient

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/crypto/chacha20poly1305"
	"golang.org/x/crypto/scrypt"
)

// Cipher holds a key derived on this device. Only its random salt may be shared.
type Cipher struct {
	key  []byte
	Salt []byte
}

func Derive(passphrase string, salt []byte) (*Cipher, error) {
	if len(passphrase) < 12 {
		return nil, errors.New("pro sync passphrase must be at least 12 characters")
	}
	if len(salt) != 32 {
		return nil, errors.New("invalid Pro sync salt")
	}
	key, err := scrypt.Key([]byte(passphrase), salt, 1<<15, 8, 1, chacha20poly1305.KeySize)
	if err != nil {
		return nil, fmt.Errorf("deriving sync key: %w", err)
	}
	return &Cipher{key: key, Salt: append([]byte(nil), salt...)}, nil
}

// FromKey restores a cipher whose key was saved in the operating system
// credential store. The salt remains in the repository's .trace directory.
func FromKey(encoded string, salt []byte) (*Cipher, error) {
	key, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || len(key) != chacha20poly1305.KeySize || len(salt) != 32 {
		return nil, errors.New("invalid saved Pro sync key")
	}
	return &Cipher{key: key, Salt: append([]byte(nil), salt...)}, nil
}

func (c *Cipher) KeyForCredentialStore() string {
	return base64.StdEncoding.EncodeToString(c.key)
}

func LoadCipher(traceDir, passphrase string) (*Cipher, error) {
	path := filepath.Join(traceDir, "sync-salt")
	salt, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		salt = make([]byte, 32)
		if _, err := rand.Read(salt); err != nil {
			return nil, fmt.Errorf("generating sync salt: %w", err)
		}
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if os.IsExist(err) {
			salt, err = os.ReadFile(path)
		} else if err == nil {
			_, err = f.Write(salt)
			if closeErr := f.Close(); err == nil {
				err = closeErr
			}
		}
		if err != nil {
			return nil, fmt.Errorf("saving sync salt: %w", err)
		}
	} else if err != nil {
		return nil, fmt.Errorf("reading sync salt: %w", err)
	}
	return Derive(passphrase, salt)
}

func (c *Cipher) Seal(plain []byte, aad string) (string, error) {
	aead, err := chacha20poly1305.NewX(c.key)
	if err != nil {
		return "", fmt.Errorf("creating sync cipher: %w", err)
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("generating sync nonce: %w", err)
	}
	return base64.StdEncoding.EncodeToString(aead.Seal(nonce, nonce, plain, []byte(aad))), nil
}

func (c *Cipher) Open(encoded, aad string) ([]byte, error) {
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("decoding sync payload: %w", err)
	}
	aead, err := chacha20poly1305.NewX(c.key)
	if err != nil {
		return nil, fmt.Errorf("creating sync cipher: %w", err)
	}
	if len(data) < aead.NonceSize() {
		return nil, errors.New("sync payload is too short")
	}
	plain, err := aead.Open(nil, data[:aead.NonceSize()], data[aead.NonceSize():], []byte(aad))
	if err != nil {
		return nil, fmt.Errorf("authenticating sync payload: %w", err)
	}
	return plain, nil
}
