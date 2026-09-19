package envelope

import (
	"bufio"
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"hash"
	"io"
	"os"
)

var magic = []byte("ZCSNAP1\n")

type Header struct {
	Version       int    `json:"version"`
	SnapshotID    string `json:"snapshot_id"`
	Archive       string `json:"archive"`
	Cipher        string `json:"cipher"`
	Integrity     string `json:"integrity"`
	IV            string `json:"iv"`
	WrappedKey    string `json:"wrapped_key"`
	WrappedKeyAlg string `json:"wrapped_key_alg"`
}

func ParsePublicKey(pemText string) (*rsa.PublicKey, error) {
	block, _ := pem.Decode([]byte(pemText))
	if block == nil {
		return nil, errors.New("public key is not valid PEM")
	}
	if parsed, err := x509.ParsePKIXPublicKey(block.Bytes); err == nil {
		key, ok := parsed.(*rsa.PublicKey)
		if !ok {
			return nil, errors.New("public key is not RSA")
		}
		return key, nil
	}
	key, err := x509.ParsePKCS1PublicKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse RSA public key: %w", err)
	}
	return key, nil
}

func MarshalPublicKey(key *rsa.PublicKey) (string, error) {
	b, err := x509.MarshalPKIXPublicKey(key)
	if err != nil {
		return "", err
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: b})), nil
}

func EncryptFile(inputPath, outputPath, snapshotID, publicKeyPEM string, maxBytes int64) (int64, error) {
	publicKey, err := ParsePublicKey(publicKeyPEM)
	if err != nil {
		return 0, err
	}
	keyMaterial := make([]byte, 64)
	iv := make([]byte, aes.BlockSize)
	if _, err := io.ReadFull(rand.Reader, keyMaterial); err != nil {
		return 0, err
	}
	if _, err := io.ReadFull(rand.Reader, iv); err != nil {
		return 0, err
	}
	wrapped, err := rsa.EncryptOAEP(sha256.New(), rand.Reader, publicKey, keyMaterial, []byte("zcode-snapshot-v1"))
	if err != nil {
		return 0, fmt.Errorf("wrap data keys: %w", err)
	}
	header := Header{
		Version: 1, SnapshotID: snapshotID, Archive: "tar+gzip",
		Cipher: "AES-256-CTR", Integrity: "HMAC-SHA256",
		IV:            base64.StdEncoding.EncodeToString(iv),
		WrappedKey:    base64.StdEncoding.EncodeToString(wrapped),
		WrappedKeyAlg: "RSA-OAEP-SHA256",
	}
	headerJSON, err := json.Marshal(header)
	if err != nil {
		return 0, err
	}
	if len(headerJSON) > 1<<20 {
		return 0, errors.New("envelope header is unexpectedly large")
	}

	in, err := os.Open(inputPath)
	if err != nil {
		return 0, err
	}
	defer in.Close()
	out, err := os.OpenFile(outputPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return 0, err
	}
	ok := false
	defer func() {
		out.Close()
		if !ok {
			_ = os.Remove(outputPath)
		}
	}()

	lim := &limitWriter{w: out, max: maxBytes}
	mac := hmac.New(sha256.New, keyMaterial[32:])
	prefix := make([]byte, len(magic)+4)
	copy(prefix, magic)
	binary.BigEndian.PutUint32(prefix[len(magic):], uint32(len(headerJSON)))
	if err := writeBoth(lim, mac, prefix); err != nil {
		return 0, err
	}
	if err := writeBoth(lim, mac, headerJSON); err != nil {
		return 0, err
	}
	block, err := aes.NewCipher(keyMaterial[:32])
	if err != nil {
		return 0, err
	}
	stream := cipher.NewCTR(block, iv)
	if _, err := io.Copy(&cipher.StreamWriter{S: stream, W: io.MultiWriter(lim, mac)}, in); err != nil {
		return 0, err
	}
	if _, err := lim.Write(mac.Sum(nil)); err != nil {
		return 0, err
	}
	if err := out.Sync(); err != nil {
		return 0, err
	}
	ok = true
	return lim.n, out.Close()
}

func writeBoth(dst io.Writer, mac hash.Hash, p []byte) error {
	if _, err := dst.Write(p); err != nil {
		return err
	}
	_, err := mac.Write(p)
	return err
}

type limitWriter struct {
	w   io.Writer
	n   int64
	max int64
}

func (w *limitWriter) Write(p []byte) (int, error) {
	if w.max > 0 && w.n+int64(len(p)) > w.max {
		return 0, fmt.Errorf("encrypted snapshot exceeds server max_size (%d bytes)", w.max)
	}
	n, err := w.w.Write(p)
	w.n += int64(n)
	return n, err
}

// Decrypt is intentionally exported for offline recovery and synthetic tests.
func Decrypt(input io.Reader, output io.Writer, privateKey *rsa.PrivateKey) (Header, error) {
	reader := bufio.NewReader(input)
	prefix := make([]byte, len(magic)+4)
	if _, err := io.ReadFull(reader, prefix); err != nil {
		return Header{}, err
	}
	if !bytes.Equal(prefix[:len(magic)], magic) {
		return Header{}, errors.New("invalid snapshot envelope magic")
	}
	headerLen := binary.BigEndian.Uint32(prefix[len(magic):])
	if headerLen == 0 || headerLen > 1<<20 {
		return Header{}, errors.New("invalid envelope header length")
	}
	headerBytes := make([]byte, headerLen)
	if _, err := io.ReadFull(reader, headerBytes); err != nil {
		return Header{}, err
	}
	var header Header
	if err := json.Unmarshal(headerBytes, &header); err != nil {
		return Header{}, err
	}
	wrapped, err := base64.StdEncoding.DecodeString(header.WrappedKey)
	if err != nil {
		return Header{}, err
	}
	keyMaterial, err := rsa.DecryptOAEP(sha256.New(), rand.Reader, privateKey, wrapped, []byte("zcode-snapshot-v1"))
	if err != nil {
		return Header{}, fmt.Errorf("unwrap data keys: %w", err)
	}
	if len(keyMaterial) != 64 {
		return Header{}, errors.New("invalid data key length")
	}
	iv, err := base64.StdEncoding.DecodeString(header.IV)
	if err != nil || len(iv) != aes.BlockSize {
		return Header{}, errors.New("invalid IV")
	}
	rest, err := io.ReadAll(reader)
	if err != nil {
		return Header{}, err
	}
	if len(rest) < sha256.Size {
		return Header{}, errors.New("truncated encrypted snapshot")
	}
	ciphertext, tag := rest[:len(rest)-sha256.Size], rest[len(rest)-sha256.Size:]
	mac := hmac.New(sha256.New, keyMaterial[32:])
	mac.Write(prefix)
	mac.Write(headerBytes)
	mac.Write(ciphertext)
	if !hmac.Equal(tag, mac.Sum(nil)) {
		return Header{}, errors.New("snapshot integrity check failed")
	}
	block, _ := aes.NewCipher(keyMaterial[:32])
	stream := cipher.NewCTR(block, iv)
	_, err = io.Copy(output, &cipher.StreamReader{S: stream, R: bytes.NewReader(ciphertext)})
	return header, err
}
