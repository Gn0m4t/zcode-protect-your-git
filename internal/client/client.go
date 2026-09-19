package client

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Gn0m4t/zcode-protect-your-git/internal/envelope"
	"github.com/Gn0m4t/zcode-protect-your-git/internal/protocol"
	"github.com/Gn0m4t/zcode-protect-your-git/internal/snapshot"
)

type UploadOptions struct {
	RepositoryRoot     string
	SettingsPath       string
	ExtraManifestPaths []string
	HTTPClient         *http.Client
}

type UploadResult struct {
	SnapshotID    string          `json:"snapshot_id"`
	EncryptedSize int64           `json:"encrypted_size"`
	ExpiresAt     time.Time       `json:"credential_expires_at"`
	Report        snapshot.Report `json:"report"`
	OSSResponse   string          `json:"oss_response,omitempty"`
}

func Upload(ctx context.Context, opts UploadOptions) (UploadResult, error) {
	settings, err := LoadSettings(opts.SettingsPath)
	if err != nil {
		return UploadResult{}, err
	}
	report, err := snapshot.Inspect(opts.RepositoryRoot)
	if err != nil {
		return UploadResult{}, err
	}
	base, err := validateServerURL(settings.ServerURL)
	if err != nil {
		return UploadResult{}, err
	}
	httpClient := opts.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 15 * time.Minute}
	}
	credential, err := requestCredential(ctx, httpClient, base, report)
	if err != nil {
		return UploadResult{}, err
	}
	if credential.MaxSize <= 0 || credential.SnapshotID == "" || credential.Upload.URL == "" {
		return UploadResult{}, errors.New("server returned an incomplete upload credential")
	}
	if time.Now().After(credential.ExpiresAt) {
		return UploadResult{}, errors.New("server returned an expired upload credential")
	}
	tempDir, err := os.MkdirTemp(report.RepositoryRoot, ".zcode-snapshot-")
	if err != nil {
		return UploadResult{}, fmt.Errorf("create repository-local temporary directory: %w", err)
	}
	defer os.RemoveAll(tempDir)
	archivePath := filepath.Join(tempDir, "snapshot.tar.gz")
	encryptedPath := filepath.Join(tempDir, "snapshot.tar.gz.enc")
	if _, err := snapshot.Build(report.RepositoryRoot, archivePath, opts.ExtraManifestPaths); err != nil {
		return UploadResult{}, fmt.Errorf("build snapshot: %w", err)
	}
	encryptedSize, err := envelope.EncryptFile(archivePath, encryptedPath, credential.SnapshotID, credential.PublicKeyPEM, credential.MaxSize)
	if err != nil {
		return UploadResult{}, fmt.Errorf("encrypt snapshot: %w", err)
	}
	ossResponse, err := postMultipart(ctx, httpClient, credential.Upload, encryptedPath, credential.SnapshotID+".tar.gz.enc")
	if err != nil {
		return UploadResult{}, err
	}
	return UploadResult{SnapshotID: credential.SnapshotID, EncryptedSize: encryptedSize, ExpiresAt: credential.ExpiresAt, Report: report, OSSResponse: ossResponse}, nil
}

func requestCredential(ctx context.Context, client *http.Client, base *url.URL, report snapshot.Report) (protocol.CredentialResponse, error) {
	random := make([]byte, 16)
	if _, err := io.ReadFull(rand.Reader, random); err != nil {
		return protocol.CredentialResponse{}, err
	}
	payload := protocol.CredentialRequest{ClientRequestID: hex.EncodeToString(random), RepositoryName: report.RepositoryName, EstimatedBytes: report.TotalBytes, FileCount: report.FileCount}
	body, _ := json.Marshal(payload)
	endpoint := *base
	endpoint.Path = strings.TrimRight(endpoint.Path, "/") + "/api/v1/snapshot/upload-credential"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), bytes.NewReader(body))
	if err != nil {
		return protocol.CredentialResponse{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return protocol.CredentialResponse{}, fmt.Errorf("request upload credential: %w", err)
	}
	defer resp.Body.Close()
	limited, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return protocol.CredentialResponse{}, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return protocol.CredentialResponse{}, fmt.Errorf("credential server returned %s: %s", resp.Status, strings.TrimSpace(string(limited)))
	}
	var credential protocol.CredentialResponse
	if err := json.Unmarshal(limited, &credential); err != nil {
		return credential, fmt.Errorf("decode credential response: %w", err)
	}
	return credential, nil
}

func postMultipart(ctx context.Context, client *http.Client, form protocol.UploadForm, filePath, fileName string) (string, error) {
	if !strings.EqualFold(form.Method, http.MethodPost) {
		return "", fmt.Errorf("unsupported server-directed upload method %q", form.Method)
	}
	if _, err := validateServerURL(form.URL); err != nil {
		return "", fmt.Errorf("server returned an invalid upload URL: %w", err)
	}
	file, err := os.Open(filePath)
	if err != nil {
		return "", err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return "", err
	}
	var preamble bytes.Buffer
	writer := multipart.NewWriter(&preamble)
	for key, value := range form.Fields {
		if err := writer.WriteField(key, value); err != nil {
			return "", err
		}
	}
	fileField := form.FileField
	if fileField == "" {
		fileField = "file"
	}
	if _, err := writer.CreateFormFile(fileField, fileName); err != nil {
		return "", err
	}
	epilogue := []byte("\r\n--" + writer.Boundary() + "--\r\n")
	body := io.MultiReader(bytes.NewReader(preamble.Bytes()), file, bytes.NewReader(epilogue))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, form.URL, body)
	if err != nil {
		return "", err
	}
	req.ContentLength = int64(preamble.Len()) + info.Size() + int64(len(epilogue))
	req.Header.Set("Content-Type", writer.FormDataContentType())
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("upload encrypted snapshot: %w", err)
	}
	defer resp.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return "", err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("object storage returned %s: %s", resp.Status, strings.TrimSpace(string(responseBody)))
	}
	return strings.TrimSpace(string(responseBody)), nil
}
