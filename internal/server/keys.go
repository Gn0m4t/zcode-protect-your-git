package server

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Gn0m4t/zcode-protect-your-git/internal/envelope"
)

func loadOrCreateKeys(privatePath, publicPath string) (*rsa.PrivateKey, string, error) {
	b, err := os.ReadFile(privatePath)
	if errors.Is(err, os.ErrNotExist) {
		key, genErr := rsa.GenerateKey(rand.Reader, 3072)
		if genErr != nil {
			return nil, "", genErr
		}
		if err := os.MkdirAll(filepath.Dir(privatePath), 0o700); err != nil {
			return nil, "", err
		}
		der, err := x509.MarshalPKCS8PrivateKey(key)
		if err != nil {
			return nil, "", err
		}
		if err := os.WriteFile(privatePath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), 0o600); err != nil {
			return nil, "", err
		}
		publicPEM, err := envelope.MarshalPublicKey(&key.PublicKey)
		if err != nil {
			return nil, "", err
		}
		if publicPath != "" {
			if err := os.MkdirAll(filepath.Dir(publicPath), 0o700); err != nil {
				return nil, "", err
			}
			if err := os.WriteFile(publicPath, []byte(publicPEM), 0o644); err != nil {
				return nil, "", err
			}
		}
		return key, publicPEM, nil
	}
	if err != nil {
		return nil, "", err
	}
	block, _ := pem.Decode(b)
	if block == nil {
		return nil, "", errors.New("private key is not valid PEM")
	}
	var key *rsa.PrivateKey
	if parsed, parseErr := x509.ParsePKCS8PrivateKey(block.Bytes); parseErr == nil {
		var ok bool
		key, ok = parsed.(*rsa.PrivateKey)
		if !ok {
			return nil, "", errors.New("private key is not RSA")
		}
	} else {
		key, err = x509.ParsePKCS1PrivateKey(block.Bytes)
		if err != nil {
			return nil, "", fmt.Errorf("parse RSA private key: %w", err)
		}
	}
	if key.N.BitLen() < 2048 {
		return nil, "", errors.New("snapshot RSA private key must be at least 2048 bits")
	}
	publicPEM, err := envelope.MarshalPublicKey(&key.PublicKey)
	return key, publicPEM, err
}
