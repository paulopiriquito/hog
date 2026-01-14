package session

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
)

// Data represents the decrypted session cookie content
type Data struct {
	JWT       string `json:"jwt"`
	Identity  string `json:"identity"`
	SessionID string `json:"session_id"`
}

// SessionData is an alias for Data for backwards compatibility
type SessionData = Data

// EncryptSessionCookie encrypts the session cookie using AES-256-GCM
func EncryptSessionCookie(data Data, key string) (string, error) {
	if len(key) != 32 {
		return "", errors.New("encryption key must be exactly 32 bytes for AES-256")
	}

	block, err := aes.NewCipher([]byte(key))
	if err != nil {
		return "", err
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}

	jsonData, err := json.Marshal(data)
	if err != nil {
		return "", err
	}

	ciphertext := gcm.Seal(nonce, nonce, jsonData, nil)
	return base64.StdEncoding.EncodeToString(ciphertext), nil
}

// DecryptSessionCookie decrypts the session cookie using AES-256-GCM
func DecryptSessionCookie(encrypted string, key string) (*Data, error) {
	if len(key) != 32 {
		return nil, errors.New("decryption key must be exactly 32 bytes for AES-256")
	}

	ciphertext, err := base64.StdEncoding.DecodeString(encrypted)
	if err != nil {
		return nil, err
	}

	block, err := aes.NewCipher([]byte(key))
	if err != nil {
		return nil, err
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}

	nonceSize := gcm.NonceSize()
	if len(ciphertext) < nonceSize {
		return nil, errors.New("ciphertext too short")
	}

	nonce, ciphertext := ciphertext[:nonceSize], ciphertext[nonceSize:]
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, err
	}

	var data Data
	if err := json.Unmarshal(plaintext, &data); err != nil {
		return nil, err
	}

	return &data, nil
}

// GenerateRandomKey generates a random 32-byte key for AES-256
func GenerateRandomKey() (string, error) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return "", err
	}
	return string(key), nil
}
