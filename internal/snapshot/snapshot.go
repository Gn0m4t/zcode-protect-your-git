package snapshot

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type Category struct {
	Bytes int64 `json:"bytes"`
	Files int64 `json:"files"`
}

type Report struct {
	RepositoryRoot     string              `json:"repository_root"`
	RepositoryName     string              `json:"repository_name"`
	TotalBytes         int64               `json:"total_bytes"`
	FileCount          int64               `json:"file_count"`
	Categories         map[string]Category `json:"categories"`
	SensitivePaths     []string            `json:"sensitive_paths"`
	IncludesGitHistory bool                `json:"includes_git_history"`
	Warnings           []string            `json:"warnings"`
}

type ManifestEntry struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

type ExtraManifest struct {
	Version   int             `json:"version"`
	CreatedAt time.Time       `json:"created_at"`
	Entries   []ManifestEntry `json:"entries"`
}

func Inspect(root string) (Report, error) {
	root, err := validateRoot(root)
	if err != nil {
		return Report{}, err
	}
	report := Report{
		RepositoryRoot: root,
		RepositoryName: filepath.Base(root),
		Categories: map[string]Category{
			"git_lfs": {}, "git_objects": {}, "git_logs": {}, "git_other": {}, "workspace": {},
		},
		IncludesGitHistory: true,
		Warnings: []string{
			"The snapshot includes .git history, reflogs, local branch metadata, and .git/config.",
			"Deleted secrets may remain recoverable from Git objects and LFS cache.",
		},
	}
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(root, path)
		if err != nil || rel == "." {
			return err
		}
		if shouldSkip(rel, d) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() && info.Mode()&os.ModeSymlink == 0 {
			return nil
		}
		size := info.Size()
		cat := category(filepath.ToSlash(rel))
		entry := report.Categories[cat]
		entry.Bytes += size
		entry.Files++
		report.Categories[cat] = entry
		report.TotalBytes += size
		report.FileCount++
		if isSensitivePath(filepath.ToSlash(rel)) {
			report.SensitivePaths = append(report.SensitivePaths, filepath.ToSlash(rel))
		}
		return nil
	})
	if err != nil {
		return Report{}, err
	}
	sort.Strings(report.SensitivePaths)
	return report, nil
}

func Build(root, outputPath string, extraManifestPaths []string) (Report, error) {
	report, err := Inspect(root)
	if err != nil {
		return Report{}, err
	}
	manifest, err := buildManifest(report.RepositoryRoot, extraManifestPaths)
	if err != nil {
		return Report{}, err
	}
	out, err := os.OpenFile(outputPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return Report{}, err
	}
	ok := false
	defer func() {
		out.Close()
		if !ok {
			_ = os.Remove(outputPath)
		}
	}()
	gz := gzip.NewWriter(out)
	tw := tar.NewWriter(gz)
	err = filepath.WalkDir(report.RepositoryRoot, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(report.RepositoryRoot, path)
		if err != nil || rel == "." {
			return err
		}
		if samePath(path, outputPath) || shouldSkip(rel, d) {
			if d.IsDir() && shouldSkip(rel, d) {
				return filepath.SkipDir
			}
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		var link string
		if info.Mode()&os.ModeSymlink != 0 {
			link, err = os.Readlink(path)
			if err != nil {
				return err
			}
		}
		header, err := tar.FileInfoHeader(info, link)
		if err != nil {
			return err
		}
		header.Name = filepath.ToSlash(rel)
		if err := tw.WriteHeader(header); err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(tw, file)
		closeErr := file.Close()
		if copyErr != nil {
			return copyErr
		}
		return closeErr
	})
	if err == nil && len(manifest.Entries) > 0 {
		var b []byte
		b, err = json.MarshalIndent(manifest, "", "  ")
		if err == nil {
			header := &tar.Header{Name: ".zcode/repo_snapshot_extra_manifest.json", Mode: 0o600, Size: int64(len(b)), ModTime: manifest.CreatedAt}
			if err = tw.WriteHeader(header); err == nil {
				_, err = tw.Write(b)
			}
		}
	}
	if closeErr := tw.Close(); err == nil {
		err = closeErr
	}
	if closeErr := gz.Close(); err == nil {
		err = closeErr
	}
	if closeErr := out.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return Report{}, err
	}
	ok = true
	return report, nil
}

func validateRoot(root string) (string, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", fmt.Errorf("resolve repository path: %w", err)
	}
	info, err := os.Stat(real)
	if err != nil || !info.IsDir() {
		return "", errors.New("repository root must be a directory")
	}
	gitInfo, err := os.Lstat(filepath.Join(real, ".git"))
	if err != nil {
		return "", errors.New("repository root does not contain .git")
	}
	if !gitInfo.IsDir() {
		return "", errors.New(".git is not a directory; external gitdir/worktree snapshots are rejected to prevent reading outside the repository")
	}
	return real, nil
}

func shouldSkip(rel string, d fs.DirEntry) bool {
	clean := filepath.ToSlash(rel)
	return clean == ".tmp-test" || strings.HasPrefix(clean, ".tmp-test/") ||
		strings.HasPrefix(clean, ".zcode-snapshot-") || strings.Contains(clean, "/.zcode-snapshot-")
}

func category(rel string) string {
	switch {
	case strings.HasPrefix(rel, ".git/lfs/"):
		return "git_lfs"
	case strings.HasPrefix(rel, ".git/objects/"):
		return "git_objects"
	case strings.HasPrefix(rel, ".git/logs/"):
		return "git_logs"
	case rel == ".git" || strings.HasPrefix(rel, ".git/"):
		return "git_other"
	default:
		return "workspace"
	}
}

func isSensitivePath(rel string) bool {
	base := strings.ToLower(filepath.Base(rel))
	if rel == ".git/config" || strings.HasPrefix(rel, ".git/logs/") {
		return true
	}
	if strings.HasPrefix(base, ".env") || strings.Contains(base, "credential") || strings.Contains(base, "secret") {
		return true
	}
	return base == "id_rsa" || base == "id_ed25519" || strings.HasSuffix(base, ".pem") || strings.HasSuffix(base, ".key")
}

func buildManifest(root string, paths []string) (ExtraManifest, error) {
	manifest := ExtraManifest{Version: 1, CreatedAt: time.Now().UTC()}
	for _, candidate := range paths {
		abs := candidate
		if !filepath.IsAbs(abs) {
			abs = filepath.Join(root, abs)
		}
		real, err := filepath.EvalSymlinks(abs)
		if err != nil {
			return manifest, fmt.Errorf("resolve manifest path %q: %w", candidate, err)
		}
		if !within(root, real) {
			return manifest, fmt.Errorf("extra manifest path %q is outside repository root", candidate)
		}
		info, err := os.Stat(real)
		if err != nil {
			return manifest, err
		}
		if !info.Mode().IsRegular() {
			return manifest, fmt.Errorf("extra manifest path %q must be a regular file", candidate)
		}
		f, err := os.Open(real)
		if err != nil {
			return manifest, err
		}
		h := sha256.New()
		_, copyErr := io.Copy(h, f)
		closeErr := f.Close()
		if copyErr != nil {
			return manifest, copyErr
		}
		if closeErr != nil {
			return manifest, closeErr
		}
		rel, _ := filepath.Rel(root, real)
		manifest.Entries = append(manifest.Entries, ManifestEntry{Path: filepath.ToSlash(rel), SHA256: hex.EncodeToString(h.Sum(nil)), Size: info.Size()})
	}
	sort.Slice(manifest.Entries, func(i, j int) bool { return manifest.Entries[i].Path < manifest.Entries[j].Path })
	return manifest, nil
}

func within(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func samePath(a, b string) bool {
	aa, _ := filepath.Abs(a)
	bb, _ := filepath.Abs(b)
	return aa == bb
}
