package server

import (
	"bufio"
	"context"
	"crypto"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Gn0m4t/zcode-protect-your-git/internal/protocol"
)

type pendingSnapshot struct {
	ID        string
	Token     string
	ObjectKey string
	ExpiresAt time.Time
}

type Server struct {
	cfg          Config
	publicKeyPEM string
	callbackKey  *rsa.PublicKey
	mu           sync.Mutex
	pending      map[string]pendingSnapshot
	receipts     map[string]protocol.CallbackReceipt
	mux          *http.ServeMux
}

func New(cfg Config) (*Server, error) {
	_, publicKey, err := loadOrCreateKeys(cfg.PrivateKeyFile, cfg.PublicKeyFile)
	if err != nil {
		return nil, fmt.Errorf("load snapshot encryption keys: %w", err)
	}
	s := &Server{cfg: cfg, publicKeyPEM: publicKey, pending: make(map[string]pendingSnapshot), receipts: make(map[string]protocol.CallbackReceipt), mux: http.NewServeMux()}
	if cfg.OSS.CallbackPublicKeyFile != "" {
		keyBytes, err := os.ReadFile(cfg.OSS.CallbackPublicKeyFile)
		if err != nil {
			return nil, fmt.Errorf("read OSS callback public key: %w", err)
		}
		block, _ := pem.Decode(keyBytes)
		if block == nil {
			return nil, errors.New("OSS callback public key is not PEM")
		}
		parsed, err := x509ParsePublicKey(block.Bytes)
		if err != nil {
			return nil, err
		}
		s.callbackKey = parsed
	}
	s.routes()
	return s, nil
}

func (s *Server) Handler() http.Handler { return s.mux }

func (s *Server) routes() {
	s.mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	s.mux.HandleFunc("POST /api/v1/snapshot/upload-credential", s.handleCredential)
	s.mux.HandleFunc("POST /api/v1/snapshot/callback", s.handleCallback)
	if s.cfg.OSS.Mode == "development" {
		s.mux.HandleFunc("POST /api/v1/dev-oss", s.handleDevelopmentOSS)
	}
}

func (s *Server) handleCredential(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	var request protocol.CredentialRequest
	if err := decoder.Decode(&request); err != nil {
		writeJSON(w, http.StatusBadRequest, protocol.APIError{Error: "invalid request: " + err.Error()})
		return
	}
	if request.ClientRequestID == "" || request.RepositoryName == "" || request.EstimatedBytes < 0 || request.FileCount < 0 {
		writeJSON(w, http.StatusBadRequest, protocol.APIError{Error: "missing or invalid request fields"})
		return
	}
	id, err := randomHex(16)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, protocol.APIError{Error: err.Error()})
		return
	}
	token, err := randomHex(32)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, protocol.APIError{Error: err.Error()})
		return
	}
	ttl, _ := time.ParseDuration(s.cfg.CredentialTTL)
	expires := time.Now().UTC().Add(ttl)
	objectKey := strings.Trim(s.cfg.OSS.ObjectPrefix, "/") + "/" + time.Now().UTC().Format("2006/01/02") + "/" + id + ".tar.gz.enc"
	pending := pendingSnapshot{ID: id, Token: token, ObjectKey: objectKey, ExpiresAt: expires}
	s.mu.Lock()
	s.pending[id] = pending
	s.mu.Unlock()
	form, err := s.makeUploadForm(pending)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, protocol.APIError{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, protocol.CredentialResponse{ProtocolVersion: protocol.Version, SnapshotID: id, PublicKeyPEM: s.publicKeyPEM, MaxSize: s.cfg.MaxUploadBytes, ExpiresAt: expires, Upload: form})
}

func (s *Server) makeUploadForm(p pendingSnapshot) (protocol.UploadForm, error) {
	if s.cfg.OSS.Mode == "development" {
		return protocol.UploadForm{Method: http.MethodPost, URL: strings.TrimRight(s.cfg.PublicBaseURL, "/") + "/api/v1/dev-oss", FileField: "file", Fields: map[string]string{"key": p.ObjectKey, "snapshot_id": p.ID, "upload_token": p.Token}}, nil
	}
	callbackURL := s.cfg.OSS.CallbackURL
	separator := "?"
	if strings.Contains(callbackURL, "?") {
		separator = "&"
	}
	callbackURL += separator + "snapshot_id=" + url.QueryEscape(p.ID) + "&token=" + url.QueryEscape(p.Token)
	callback := map[string]any{
		"callbackUrl":      callbackURL,
		"callbackBody":     "snapshot_id=${x:callback-var-snapshot}&object=${object}&size=${size}&etag=${etag}",
		"callbackBodyType": "application/x-www-form-urlencoded",
	}
	callbackJSON, _ := json.Marshal(callback)
	callbackField := base64.StdEncoding.EncodeToString(callbackJSON)
	expiration := p.ExpiresAt.Format(time.RFC3339)
	policyObject := map[string]any{
		"expiration": expiration,
		"conditions": []any{
			[]any{"content-length-range", 1, s.cfg.MaxUploadBytes},
			[]any{"eq", "$key", p.ObjectKey},
			[]any{"eq", "$x-oss-meta-snapshot-id", p.ID},
			[]any{"eq", "$x:callback-var-snapshot", p.ID},
			[]any{"eq", "$callback", callbackField},
		},
	}
	policyJSON, err := json.Marshal(policyObject)
	if err != nil {
		return protocol.UploadForm{}, err
	}
	policy := base64.StdEncoding.EncodeToString(policyJSON)
	secret := os.Getenv(s.cfg.OSS.AccessKeySecretEnv)
	if secret == "" {
		return protocol.UploadForm{}, fmt.Errorf("OSS secret environment variable %s is empty", s.cfg.OSS.AccessKeySecretEnv)
	}
	mac := hmac.New(sha1.New, []byte(secret))
	mac.Write([]byte(policy))
	signature := base64.StdEncoding.EncodeToString(mac.Sum(nil))
	return protocol.UploadForm{Method: http.MethodPost, URL: s.cfg.OSS.Endpoint, FileField: "file", Fields: map[string]string{
		"key": p.ObjectKey, "policy": policy, "OSSAccessKeyId": s.cfg.OSS.AccessKeyID, "Signature": signature,
		"x-oss-meta-snapshot-id": p.ID, "x:callback-var-snapshot": p.ID,
		"callback": callbackField,
	}}, nil
}

func (s *Server) handleDevelopmentOSS(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, s.cfg.MaxUploadBytes+(2<<20))
	if err := r.ParseMultipartForm(s.cfg.MaxUploadBytes + (1 << 20)); err != nil {
		writeJSON(w, http.StatusBadRequest, protocol.APIError{Error: "invalid multipart upload: " + err.Error()})
		return
	}
	id, token, objectKey := r.FormValue("snapshot_id"), r.FormValue("upload_token"), r.FormValue("key")
	pending, ok := s.validPending(id, token, objectKey)
	if !ok {
		writeJSON(w, http.StatusForbidden, protocol.APIError{Error: "invalid or expired upload credential"})
		return
	}
	file, _, err := r.FormFile("file")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, protocol.APIError{Error: "file is required"})
		return
	}
	defer file.Close()
	destination := filepath.Join(s.cfg.OSS.DevelopmentStorageDir, filepath.FromSlash(pending.ObjectKey))
	root, _ := filepath.Abs(s.cfg.OSS.DevelopmentStorageDir)
	destAbs, _ := filepath.Abs(destination)
	if !strings.HasPrefix(destAbs, root+string(filepath.Separator)) {
		writeJSON(w, http.StatusBadRequest, protocol.APIError{Error: "invalid object key"})
		return
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
		writeJSON(w, http.StatusInternalServerError, protocol.APIError{Error: err.Error()})
		return
	}
	out, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		writeJSON(w, http.StatusConflict, protocol.APIError{Error: err.Error()})
		return
	}
	hash := sha256.New()
	size, copyErr := io.Copy(io.MultiWriter(out, hash), io.LimitReader(file, s.cfg.MaxUploadBytes+1))
	closeErr := out.Close()
	if copyErr != nil || closeErr != nil || size > s.cfg.MaxUploadBytes {
		_ = os.Remove(destination)
		writeJSON(w, http.StatusRequestEntityTooLarge, protocol.APIError{Error: "upload exceeds limit or could not be stored"})
		return
	}
	receipt := protocol.CallbackReceipt{SnapshotID: id, ObjectKey: objectKey, Size: size, ETag: hex.EncodeToString(hash.Sum(nil)), ReceivedAt: time.Now().UTC()}
	if err := s.recordReceipt(receipt); err != nil {
		_ = os.Remove(destination)
		writeJSON(w, http.StatusInternalServerError, protocol.APIError{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, receipt)
}

func (s *Server) handleCallback(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, protocol.APIError{Error: err.Error()})
		return
	}
	if s.callbackKey != nil && !verifyOSSCallback(r, body, s.callbackKey) {
		writeJSON(w, http.StatusForbidden, protocol.APIError{Error: "invalid OSS callback signature"})
		return
	}
	values, err := url.ParseQuery(string(body))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, protocol.APIError{Error: "invalid callback body"})
		return
	}
	id, token := r.URL.Query().Get("snapshot_id"), r.URL.Query().Get("token")
	objectKey := values.Get("object")
	pending, ok := s.validPending(id, token, objectKey)
	if !ok {
		writeJSON(w, http.StatusForbidden, protocol.APIError{Error: "invalid or expired callback"})
		return
	}
	size, err := strconv.ParseInt(values.Get("size"), 10, 64)
	if err != nil || size < 0 || size > s.cfg.MaxUploadBytes {
		writeJSON(w, http.StatusBadRequest, protocol.APIError{Error: "invalid callback size"})
		return
	}
	receipt := protocol.CallbackReceipt{SnapshotID: pending.ID, ObjectKey: pending.ObjectKey, Size: size, ETag: values.Get("etag"), ReceivedAt: time.Now().UTC()}
	if err := s.recordReceipt(receipt); err != nil {
		writeJSON(w, http.StatusInternalServerError, protocol.APIError{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "snapshot_id": id})
}

func (s *Server) validPending(id, token, objectKey string) (pendingSnapshot, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.pending[id]
	return p, ok && time.Now().Before(p.ExpiresAt) && hmac.Equal([]byte(p.Token), []byte(token)) && p.ObjectKey == objectKey
}

func (s *Server) recordReceipt(receipt protocol.CallbackReceipt) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.receipts[receipt.SnapshotID]; exists {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(s.cfg.UploadLogFile), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(s.cfg.UploadLogFile, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	b, _ := json.Marshal(receipt)
	_, writeErr := f.Write(append(b, '\n'))
	closeErr := f.Close()
	if writeErr != nil {
		return writeErr
	}
	if closeErr != nil {
		return closeErr
	}
	s.receipts[receipt.SnapshotID] = receipt
	delete(s.pending, receipt.SnapshotID)
	return nil
}

// ReadUploadLog returns the newest completed uploads first. The log lives only
// on the server; the plugin does not create a local activity log.
func ReadUploadLog(path string, limit int) ([]protocol.CallbackReceipt, error) {
	if limit <= 0 {
		limit = 100
	}
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []protocol.CallbackReceipt{}, nil
		}
		return nil, err
	}
	defer f.Close()
	entries := make([]protocol.CallbackReceipt, 0)
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 2<<20)
	for scanner.Scan() {
		var entry protocol.CallbackReceipt
		if err := json.Unmarshal(scanner.Bytes(), &entry); err != nil {
			return nil, fmt.Errorf("decode upload log: %w", err)
		}
		entries = append(entries, entry)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if len(entries) > limit {
		entries = entries[len(entries)-limit:]
	}
	for left, right := 0, len(entries)-1; left < right; left, right = left+1, right-1 {
		entries[left], entries[right] = entries[right], entries[left]
	}
	return entries, nil
}

func verifyOSSCallback(r *http.Request, body []byte, key *rsa.PublicKey) bool {
	signature, err := base64.StdEncoding.DecodeString(strings.TrimSpace(r.Header.Get("Authorization")))
	if err != nil {
		return false
	}
	pathAndQuery := r.URL.Path
	if r.URL.RawQuery != "" {
		pathAndQuery += "?" + r.URL.RawQuery
	}
	h := sha1.Sum(append(append([]byte(pathAndQuery), '\n'), body...))
	return rsa.VerifyPKCS1v15(key, crypto.SHA1, h[:], signature) == nil
}

func x509ParsePublicKey(der []byte) (*rsa.PublicKey, error) {
	parsed, err := x509.ParsePKIXPublicKey(der)
	if err == nil {
		key, ok := parsed.(*rsa.PublicKey)
		if !ok {
			return nil, errors.New("callback public key is not RSA")
		}
		return key, nil
	}
	key, err := x509.ParsePKCS1PublicKey(der)
	if err != nil {
		return nil, fmt.Errorf("parse callback public key: %w", err)
	}
	return key, nil
}

func randomHex(size int) (string, error) {
	b := make([]byte, size)
	if _, err := io.ReadFull(rand.Reader, b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func ListenAndServe(ctx context.Context, cfg Config) error {
	server, err := New(cfg)
	if err != nil {
		return err
	}
	httpServer := &http.Server{Addr: cfg.Listen, Handler: server.Handler(), ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 20 * time.Minute, IdleTimeout: 2 * time.Minute}
	errCh := make(chan error, 1)
	go func() { errCh <- httpServer.ListenAndServe() }()
	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return httpServer.Shutdown(shutdownCtx)
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}
