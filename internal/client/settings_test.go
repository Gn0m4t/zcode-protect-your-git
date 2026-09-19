package client

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfigureConfirmsInstallationAndServerOnlyOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config", "client.json")
	var firstOutput bytes.Buffer
	first, err := Configure("https://server.example.com/", path, strings.NewReader("yes\nyes\n"), &firstOutput)
	if err != nil {
		t.Fatal(err)
	}
	if first.ServerURL != "https://server.example.com" {
		t.Fatalf("unexpected server URL: %q", first.ServerURL)
	}
	if strings.Count(firstOutput.String(), "[y/N]") != 2 {
		t.Fatalf("expected two setup confirmations, got %q", firstOutput.String())
	}

	var repeatOutput bytes.Buffer
	second, err := Configure("https://server.example.com", path, strings.NewReader(""), &repeatOutput)
	if err != nil {
		t.Fatal(err)
	}
	if second.InstallationConfirmedAt != first.InstallationConfirmedAt || second.ServerConfirmedAt != first.ServerConfirmedAt {
		t.Fatal("repeated setup changed confirmation timestamps")
	}
	if strings.Contains(repeatOutput.String(), "[y/N]") {
		t.Fatalf("repeated setup prompted again: %q", repeatOutput.String())
	}
}

func TestConfigureOnlyReconfirmsChangedServer(t *testing.T) {
	path := filepath.Join(t.TempDir(), "client.json")
	if _, err := Configure("https://one.example.com", path, strings.NewReader("y\ny\n"), &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	settings, err := Configure("https://two.example.com", path, strings.NewReader("y\n"), &output)
	if err != nil {
		t.Fatal(err)
	}
	if settings.ServerURL != "https://two.example.com" || strings.Count(output.String(), "[y/N]") != 1 {
		t.Fatalf("changed server should produce exactly one confirmation: %#v %q", settings, output.String())
	}
}

func TestConfigureKeepsInstallationConsentWhenServerIsRejected(t *testing.T) {
	path := filepath.Join(t.TempDir(), "client.json")
	var firstOutput bytes.Buffer
	if _, err := Configure("https://one.example.com", path, strings.NewReader("y\nn\n"), &firstOutput); err == nil {
		t.Fatal("rejected server was accepted")
	}
	var retryOutput bytes.Buffer
	settings, err := Configure("https://one.example.com", path, strings.NewReader("y\n"), &retryOutput)
	if err != nil {
		t.Fatal(err)
	}
	if settings.ServerURL != "https://one.example.com" || strings.Count(retryOutput.String(), "[y/N]") != 1 {
		t.Fatalf("retry should ask only for server confirmation: %#v %q", settings, retryOutput.String())
	}
}

func TestConfigureRejectsPlainRemoteHTTP(t *testing.T) {
	_, err := Configure("http://server.example.com", filepath.Join(t.TempDir(), "client.json"), strings.NewReader("y\ny\n"), &bytes.Buffer{})
	if err == nil {
		t.Fatal("plain remote HTTP server was accepted")
	}
}
