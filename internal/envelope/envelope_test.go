package envelope

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"os"
	"path/filepath"
	"testing"
)

func TestEncryptDecryptAndTamperDetection(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	plain := []byte("synthetic tar.gz bytes\x00with history")
	input := filepath.Join(dir, "input.tar.gz")
	if err := os.WriteFile(input, plain, 0o600); err != nil {
		t.Fatal(err)
	}
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	publicPEM, err := MarshalPublicKey(&privateKey.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	encrypted := filepath.Join(dir, "snapshot.enc")
	if _, err := EncryptFile(input, encrypted, "snapshot-test", publicPEM, 1<<20); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(encrypted)
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	header, err := Decrypt(f, &output, privateKey)
	f.Close()
	if err != nil {
		t.Fatal(err)
	}
	if header.SnapshotID != "snapshot-test" {
		t.Fatalf("unexpected snapshot id %q", header.SnapshotID)
	}
	if !bytes.Equal(output.Bytes(), plain) {
		t.Fatalf("round trip mismatch")
	}

	tampered, err := os.ReadFile(encrypted)
	if err != nil {
		t.Fatal(err)
	}
	tampered[len(tampered)-33] ^= 0xff
	if _, err := Decrypt(bytes.NewReader(tampered), &bytes.Buffer{}, privateKey); err == nil {
		t.Fatal("tampered ciphertext was accepted")
	}
}

func TestEncryptHonorsMaxSize(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	input := filepath.Join(dir, "input")
	if err := os.WriteFile(input, bytes.Repeat([]byte("x"), 4096), 0o600); err != nil {
		t.Fatal(err)
	}
	privateKey, _ := rsa.GenerateKey(rand.Reader, 2048)
	publicPEM, _ := MarshalPublicKey(&privateKey.PublicKey)
	if _, err := EncryptFile(input, filepath.Join(dir, "output"), "id", publicPEM, 512); err == nil {
		t.Fatal("expected max-size failure")
	}
}
