package client

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const settingsVersion = 1

// Settings contains the only persistent plugin-side configuration. It is not a
// log: it records the one-time installation decision and the confirmed server.
type Settings struct {
	Version                 int       `json:"version"`
	InstallationConfirmedAt time.Time `json:"installation_confirmed_at"`
	ServerURL               string    `json:"server_url"`
	ServerConfirmedAt       time.Time `json:"server_confirmed_at"`
}

func DefaultSettingsPath() (string, error) {
	if override := os.Getenv("ZPUG_CONFIG_FILE"); override != "" {
		return filepath.Abs(override)
	}
	root, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("resolve user config directory: %w", err)
	}
	return filepath.Join(root, "zpug", "client.json"), nil
}

func LoadSettings(path string) (Settings, error) {
	if path == "" {
		var err error
		path, err = DefaultSettingsPath()
		if err != nil {
			return Settings{}, err
		}
	}
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Settings{}, errors.New("plugin setup is incomplete; run the installer again and confirm the server address")
		}
		return Settings{}, fmt.Errorf("read plugin settings: %w", err)
	}
	var settings Settings
	decoder := json.NewDecoder(strings.NewReader(string(b)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&settings); err != nil {
		return Settings{}, fmt.Errorf("decode plugin settings: %w", err)
	}
	if settings.Version != settingsVersion || settings.InstallationConfirmedAt.IsZero() || settings.ServerConfirmedAt.IsZero() {
		return Settings{}, errors.New("plugin setup is incomplete; run the installer again")
	}
	validated, err := validateServerURL(settings.ServerURL)
	if err != nil {
		return Settings{}, fmt.Errorf("configured server URL: %w", err)
	}
	settings.ServerURL = validated.String()
	return settings, nil
}

func Configure(serverURL, path string, in io.Reader, out io.Writer) (Settings, error) {
	validated, err := validateServerURL(serverURL)
	if err != nil {
		return Settings{}, err
	}
	if path == "" {
		path, err = DefaultSettingsPath()
		if err != nil {
			return Settings{}, err
		}
	}

	settings, loadErr := loadSettingsIfPresent(path)
	reader := bufio.NewReader(in)
	now := time.Now().UTC()
	installationJustConfirmed := false
	if loadErr != nil || settings.InstallationConfirmedAt.IsZero() {
		ok, err := confirm(reader, out, "This plugin packages the current workspace and its complete .git directory, encrypts it, and uploads it when an Agent invokes the upload tool. Continue? [y/N] ")
		if err != nil {
			return Settings{}, err
		}
		if !ok {
			return Settings{}, errors.New("installation was not confirmed")
		}
		settings.InstallationConfirmedAt = now
		settings.Version = settingsVersion
		installationJustConfirmed = true
	}
	if installationJustConfirmed {
		// Persist the installation decision before asking about the server so a
		// rejected or mistyped address does not cause the first prompt to repeat.
		if err := saveSettings(path, settings); err != nil {
			return Settings{}, err
		}
	}

	serverChanged := strings.TrimRight(settings.ServerURL, "/") != strings.TrimRight(validated.String(), "/")
	if serverChanged || settings.ServerConfirmedAt.IsZero() {
		ok, err := confirm(reader, out, fmt.Sprintf("Use %s as the plugin server? [y/N] ", validated.String()))
		if err != nil {
			return Settings{}, err
		}
		if !ok {
			return Settings{}, errors.New("server address was not confirmed")
		}
		settings.ServerConfirmedAt = now
	}
	settings.Version = settingsVersion
	settings.ServerURL = validated.String()
	if err := saveSettings(path, settings); err != nil {
		return Settings{}, err
	}
	fmt.Fprintf(out, "Plugin setup saved to %s\n", path)
	return settings, nil
}

func loadSettingsIfPresent(path string) (Settings, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Settings{}, err
	}
	var settings Settings
	if err := json.Unmarshal(b, &settings); err != nil {
		return Settings{}, err
	}
	return settings, nil
}

func saveSettings(path string, settings Settings) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create plugin settings directory: %w", err)
	}
	b, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	temporary, err := os.CreateTemp(filepath.Dir(path), ".client-*.json")
	if err != nil {
		return fmt.Errorf("create temporary plugin settings: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return err
	}
	if _, err := temporary.Write(b); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("save plugin settings: %w", err)
	}
	return nil
}

func confirm(reader *bufio.Reader, out io.Writer, prompt string) (bool, error) {
	if _, err := io.WriteString(out, prompt); err != nil {
		return false, err
	}
	answer, err := reader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return false, err
	}
	answer = strings.ToLower(strings.TrimSpace(answer))
	return answer == "y" || answer == "yes", nil
}

func validateServerURL(raw string) (*url.URL, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, errors.New("server URL is required")
	}
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, errors.New("server URL must be an absolute http(s) origin without credentials, query, or fragment")
	}
	host := strings.ToLower(u.Hostname())
	local := host == "127.0.0.1" || host == "localhost" || host == "::1"
	if u.Scheme != "https" && !local {
		return nil, errors.New("server URL must use HTTPS unless it is localhost")
	}
	u.Path = strings.TrimRight(u.Path, "/")
	return u, nil
}
