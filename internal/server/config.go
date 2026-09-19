package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type Config struct {
	Listen         string    `json:"listen"`
	PublicBaseURL  string    `json:"public_base_url"`
	PrivateKeyFile string    `json:"private_key_file"`
	PublicKeyFile  string    `json:"public_key_file"`
	MaxUploadBytes int64     `json:"max_upload_bytes"`
	CredentialTTL  string    `json:"credential_ttl"`
	UploadLogFile  string    `json:"upload_log_file"`
	OSS            OSSConfig `json:"oss"`
	baseDir        string
}

type OSSConfig struct {
	Mode                  string `json:"mode"`
	Endpoint              string `json:"endpoint"`
	AccessKeyID           string `json:"access_key_id"`
	AccessKeySecretEnv    string `json:"access_key_secret_env"`
	ObjectPrefix          string `json:"object_prefix"`
	CallbackURL           string `json:"callback_url"`
	CallbackPublicKeyFile string `json:"callback_public_key_file"`
	DevelopmentStorageDir string `json:"development_storage_dir"`
}

func LoadConfig(path string) (Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}
	var cfg Config
	decoder := json.NewDecoder(strings.NewReader(string(b)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&cfg); err != nil {
		return Config{}, fmt.Errorf("decode server config: %w", err)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return Config{}, err
	}
	cfg.baseDir = filepath.Dir(abs)
	cfg.applyDefaults()
	if err := cfg.validate(); err != nil {
		return Config{}, err
	}
	cfg.resolvePaths()
	return cfg, nil
}

func (c *Config) applyDefaults() {
	if c.Listen == "" {
		c.Listen = "127.0.0.1:8080"
	}
	if c.MaxUploadBytes == 0 {
		c.MaxUploadBytes = 512 << 20
	}
	if c.CredentialTTL == "" {
		c.CredentialTTL = "15m"
	}
	if c.PrivateKeyFile == "" {
		c.PrivateKeyFile = "../var/keys/snapshot-private.pem"
	}
	if c.PublicKeyFile == "" {
		c.PublicKeyFile = "../var/keys/snapshot-public.pem"
	}
	if c.UploadLogFile == "" {
		c.UploadLogFile = "../var/upload-log.jsonl"
	}
	if c.OSS.Mode == "" {
		c.OSS.Mode = "development"
	}
	if c.OSS.ObjectPrefix == "" {
		c.OSS.ObjectPrefix = "snapshots/"
	}
	if c.OSS.DevelopmentStorageDir == "" {
		c.OSS.DevelopmentStorageDir = "../var/objects"
	}
}

func (c Config) validate() error {
	if c.MaxUploadBytes < 1<<20 {
		return errors.New("max_upload_bytes must be at least 1 MiB")
	}
	if _, err := time.ParseDuration(c.CredentialTTL); err != nil {
		return fmt.Errorf("credential_ttl: %w", err)
	}
	switch c.OSS.Mode {
	case "development":
		if c.PublicBaseURL == "" {
			return errors.New("public_base_url is required in development mode")
		}
	case "cloud-oss":
		if c.OSS.Endpoint == "" || c.OSS.AccessKeyID == "" || c.OSS.AccessKeySecretEnv == "" || c.OSS.CallbackURL == "" || c.OSS.CallbackPublicKeyFile == "" {
			return errors.New("cloud-oss requires endpoint, access_key_id, access_key_secret_env, callback_url, and callback_public_key_file")
		}
		if !strings.HasPrefix(c.OSS.Endpoint, "https://") || !strings.HasPrefix(c.OSS.CallbackURL, "https://") {
			return errors.New("cloud-oss endpoint and callback_url must use HTTPS")
		}
	default:
		return fmt.Errorf("unsupported oss.mode %q", c.OSS.Mode)
	}
	return nil
}

func (c *Config) resolvePaths() {
	c.PrivateKeyFile = resolve(c.baseDir, c.PrivateKeyFile)
	c.PublicKeyFile = resolve(c.baseDir, c.PublicKeyFile)
	c.UploadLogFile = resolve(c.baseDir, c.UploadLogFile)
	c.OSS.CallbackPublicKeyFile = resolve(c.baseDir, c.OSS.CallbackPublicKeyFile)
	c.OSS.DevelopmentStorageDir = resolve(c.baseDir, c.OSS.DevelopmentStorageDir)
}

func resolve(base, path string) string {
	if path == "" || filepath.IsAbs(path) {
		return path
	}
	return filepath.Clean(filepath.Join(base, path))
}
