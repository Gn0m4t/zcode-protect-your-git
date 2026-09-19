package client_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Gn0m4t/zcode-protect-your-git/internal/client"
	"github.com/Gn0m4t/zcode-protect-your-git/internal/envelope"
	appserver "github.com/Gn0m4t/zcode-protect-your-git/internal/server"
)

func TestDevelopmentEndToEnd(t *testing.T) {
	base := t.TempDir()
	var handler http.Handler
	httpClient := &http.Client{Transport: roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		response := recorder.Result()
		response.Request = request
		return response, nil
	})}
	baseURL := "http://localhost"
	cfg := appserver.Config{
		PublicBaseURL: baseURL, PrivateKeyFile: filepath.Join(base, "keys", "private.pem"), PublicKeyFile: filepath.Join(base, "keys", "public.pem"),
		MaxUploadBytes: 16 << 20, CredentialTTL: "5m", UploadLogFile: filepath.Join(base, "upload-log.jsonl"),
		OSS: appserver.OSSConfig{Mode: "development", ObjectPrefix: "snapshots/", DevelopmentStorageDir: filepath.Join(base, "objects")},
	}
	server, err := appserver.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	handler = server.Handler()
	repo := filepath.Join(base, "repo")
	writeSynthetic(t, filepath.Join(repo, ".git", "config"), "[remote \"origin\"]\nurl=ssh://internal/repo")
	writeSynthetic(t, filepath.Join(repo, ".git", "objects", "aa", "object"), "deleted-secret")
	writeSynthetic(t, filepath.Join(repo, "src", "main.txt"), "synthetic source")
	settingsPath := filepath.Join(base, "config", "client.json")
	if _, err := client.Configure(baseURL, settingsPath, strings.NewReader("yes\nyes\n"), &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}

	result, err := client.Upload(context.Background(), client.UploadOptions{RepositoryRoot: repo, SettingsPath: settingsPath, HTTPClient: httpClient})
	if err != nil {
		t.Fatal(err)
	}
	if result.SnapshotID == "" || result.EncryptedSize == 0 {
		t.Fatalf("unexpected result: %#v", result)
	}
	entries, err := appserver.ReadUploadLog(cfg.UploadLogFile, 10)
	if err != nil || len(entries) != 1 || entries[0].SnapshotID != result.SnapshotID {
		t.Fatalf("server upload log mismatch: %#v, %v", entries, err)
	}

	var objectPath string
	err = filepath.Walk(filepath.Join(base, "objects"), func(path string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() && strings.HasSuffix(path, ".enc") {
			objectPath = path
		}
		return err
	})
	if err != nil || objectPath == "" {
		t.Fatalf("uploaded object not found: %v", err)
	}
	privateKey := readPrivateKey(t, filepath.Join(base, "keys", "private.pem"))
	encrypted, err := os.Open(objectPath)
	if err != nil {
		t.Fatal(err)
	}
	archivePath := filepath.Join(base, "recovered.tar.gz")
	archive, err := os.Create(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	_, err = envelope.Decrypt(encrypted, archive, privateKey)
	encrypted.Close()
	archive.Close()
	if err != nil {
		t.Fatal(err)
	}
	if !archiveContains(t, archivePath, ".git/objects/aa/object") {
		t.Fatal("recovered archive lacks Git history")
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }

func writeSynthetic(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func readPrivateKey(t *testing.T, path string) *rsa.PrivateKey {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode(b)
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	return parsed.(*rsa.PrivateKey)
}

func archiveContains(t *testing.T, path, expected string) bool {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(gz)
	for {
		header, err := tr.Next()
		if err == io.EOF {
			return false
		}
		if err != nil {
			t.Fatal(err)
		}
		if header.Name == expected {
			return true
		}
	}
}
