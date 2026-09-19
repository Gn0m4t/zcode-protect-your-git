package snapshot

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestInspectAndBuild(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	repo := filepath.Join(base, "repo")
	mustWrite(t, filepath.Join(repo, ".git", "config"), "[remote \"origin\"]\nurl=ssh://internal/repo")
	mustWrite(t, filepath.Join(repo, ".git", "objects", "aa", "object"), "historical-secret")
	mustWrite(t, filepath.Join(repo, ".git", "logs", "HEAD"), "local-branch")
	mustWrite(t, filepath.Join(repo, "src", "main.go"), "package main")
	mustWrite(t, filepath.Join(repo, "settings.behavior.json"), "{\"synthetic\":true}")
	if err := os.Symlink(filepath.Join(base, "outside-secret"), filepath.Join(repo, "outside-link")); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(base, "outside-secret"), "must-not-be-followed")

	report, err := Inspect(repo)
	if err != nil {
		t.Fatal(err)
	}
	if !report.IncludesGitHistory || report.Categories["git_objects"].Files != 1 || report.Categories["git_logs"].Files != 1 {
		t.Fatalf("unexpected report: %#v", report)
	}

	archivePath := filepath.Join(base, "snapshot.tar.gz")
	if _, err := Build(repo, archivePath, []string{"settings.behavior.json"}); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(gz)
	found := map[string]bool{}
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		found[header.Name] = true
		if header.Name == "outside-link" && header.Typeflag != tar.TypeSymlink {
			t.Fatal("symlink was followed")
		}
		if header.Name == ".zcode/repo_snapshot_extra_manifest.json" {
			var manifest ExtraManifest
			if err := json.NewDecoder(tr).Decode(&manifest); err != nil {
				t.Fatal(err)
			}
			if len(manifest.Entries) != 1 || manifest.Entries[0].Path != "settings.behavior.json" {
				t.Fatalf("unexpected manifest: %#v", manifest)
			}
		}
	}
	for _, expected := range []string{".git/config", ".git/objects/aa/object", ".git/logs/HEAD", "src/main.go", ".zcode/repo_snapshot_extra_manifest.json"} {
		if !found[expected] {
			t.Errorf("archive missing %s", expected)
		}
	}
	if _, err := Build(repo, filepath.Join(base, "bad.tar.gz"), []string{filepath.Join(base, "outside-secret")}); err == nil {
		t.Fatal("outside manifest path was accepted")
	}
}

func TestRejectsExternalGitDirPointer(t *testing.T) {
	t.Parallel()
	repo := filepath.Join(t.TempDir(), "worktree")
	mustWrite(t, filepath.Join(repo, ".git"), "gitdir: ../outside/.git/worktrees/example")
	if _, err := Inspect(repo); err == nil {
		t.Fatal("external gitdir pointer was accepted")
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
