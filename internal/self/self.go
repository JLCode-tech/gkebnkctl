package self

import (
	"archive/tar"
	"archive/zip"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// GitHubRepo is the default repository for release updates.
const (
	GitHubRepo     = "JLCode-tech/gkebnkctl"
	binaryBaseName = "gkebnkctl"
	maxBinaryBytes = 500 << 20 // 500 MiB cap for decompression safety
)

// Release represents a GitHub release descriptor.
type Release struct {
	TagName     string  `json:"tag_name"`
	Name        string  `json:"name"`
	HTMLURL     string  `json:"html_url"`
	PublishedAt string  `json:"published_at"`
	Body        string  `json:"body"`
	Assets      []Asset `json:"assets"`
}

// Asset represents a release asset download item.
type Asset struct {
	Name               string `json:"name"`
	URL                string `json:"url"`
	BrowserDownloadURL string `json:"browser_download_url"`
	Size               int64  `json:"size"`
}

func getGitHubToken() string {
	if token := os.Getenv("GITHUB_TOKEN"); token != "" {
		return token
	}
	if token := os.Getenv("GH_TOKEN"); token != "" {
		return token
	}
	// Fallback to gh auth token if available
	if path, err := exec.LookPath("gh"); err == nil {
		out, err := exec.Command(path, "auth", "token").Output() // #nosec G204 -- lookpath resolved gh binary
		if err == nil {
			return strings.TrimSpace(string(out))
		}
	}
	return ""
}

// CheckUpdate queries GitHub for the latest release tag.
func CheckUpdate(ctx context.Context, repo string, currentVersion string) (*Release, bool, error) {
	if repo == "" {
		repo = GitHubRepo
	}

	url := fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", repo)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, false, err
	}
	req.Header.Set("User-Agent", "gkebnkctl-updater")
	req.Header.Set("Accept", "application/vnd.github+json")
	if token := getGitHubToken(); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, false, fmt.Errorf("check updates from %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, false, fmt.Errorf("no releases found for repository %s", repo)
	}
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return nil, false, fmt.Errorf("github api error (HTTP %d): %s", resp.StatusCode, string(body))
	}

	var rel Release
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return nil, false, fmt.Errorf("decode release json: %w", err)
	}

	latestClean := strings.TrimPrefix(rel.TagName, "v")
	currentClean := strings.TrimPrefix(currentVersion, "v")

	hasUpdate := latestClean != "" && latestClean != currentClean && currentClean != "dev"
	return &rel, hasUpdate, nil
}

// ExpectedChecksum parses a GoReleaser checksums.txt body and returns the hex
// SHA-256 digest for assetName.
func ExpectedChecksum(checksumsTxt, assetName string) (string, error) {
	scanner := bufio.NewScanner(strings.NewReader(checksumsTxt))
	for scanner.Scan() {
		fields := strings.Fields(strings.TrimSpace(scanner.Text()))
		if len(fields) >= 2 && strings.TrimPrefix(fields[1], "*") == assetName {
			return strings.ToLower(fields[0]), nil
		}
	}
	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("read checksums.txt: %w", err)
	}
	return "", fmt.Errorf("asset %q not found in checksums.txt", assetName)
}

// VerifyChecksum verifies that data matches the SHA-256 digest in checksumsTxt for assetName.
func VerifyChecksum(data []byte, checksumsTxt, assetName string) error {
	want, err := ExpectedChecksum(checksumsTxt, assetName)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(data)
	got := hex.EncodeToString(sum[:])
	if !strings.EqualFold(got, want) {
		return fmt.Errorf("checksum mismatch (sha256) for %s: got %s, want %s", assetName, got, want)
	}
	return nil
}

func downloadAssetBytes(ctx context.Context, asset Asset) ([]byte, error) {
	token := getGitHubToken()
	downloadURL := asset.BrowserDownloadURL
	if token != "" && asset.URL != "" {
		downloadURL = asset.URL
	}
	if downloadURL == "" {
		return nil, fmt.Errorf("asset %q has no download URL", asset.Name)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, downloadURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "gkebnkctl-updater")
	req.Header.Set("Accept", "application/octet-stream")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	client := &http.Client{Timeout: 120 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download asset from %s: %w", downloadURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("download error (HTTP %d)", resp.StatusCode)
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBinaryBytes))
	if err != nil {
		return nil, fmt.Errorf("read %s body: %w", asset.Name, err)
	}
	return data, nil
}

// DownloadAsset downloads the binary asset matching current OS/Arch, verifies
// its SHA-256 digest against checksums.txt (when present in the release), and
// extracts the gkebnkctl binary from .tar.gz/.tgz or .zip archives.
func DownloadAsset(ctx context.Context, rel *Release, destPath string) error {
	return DownloadAssetForPlatform(ctx, rel, runtime.GOOS, runtime.GOARCH, destPath)
}

// DownloadAssetForPlatform is the platform-parameterized implementation of DownloadAsset.
func DownloadAssetForPlatform(ctx context.Context, rel *Release, goos, goarch, destPath string) error {
	expectedSuffix := fmt.Sprintf("%s_%s", goos, goarch)
	var targetAsset *Asset
	var checksumAsset *Asset

	for i := range rel.Assets {
		a := &rel.Assets[i]
		if strings.EqualFold(a.Name, "checksums.txt") {
			checksumAsset = a
			continue
		}
		if strings.Contains(strings.ToLower(a.Name), strings.ToLower(expectedSuffix)) {
			targetAsset = a
		}
	}

	if targetAsset == nil {
		return fmt.Errorf("no matching release binary for platform %s/%s in release %s",
			goos, goarch, rel.TagName)
	}

	archiveBytes, err := downloadAssetBytes(ctx, *targetAsset)
	if err != nil {
		return err
	}

	if checksumAsset != nil {
		sumBytes, err := downloadAssetBytes(ctx, *checksumAsset)
		if err != nil {
			return fmt.Errorf("download checksums.txt: %w", err)
		}
		if err := VerifyChecksum(archiveBytes, string(sumBytes), targetAsset.Name); err != nil {
			return err
		}
	}

	targetBin := binaryBaseName
	if goos == "windows" {
		targetBin = binaryBaseName + ".exe"
	}

	lowerName := strings.ToLower(targetAsset.Name)
	switch {
	case strings.HasSuffix(lowerName, ".tar.gz") || strings.HasSuffix(lowerName, ".tgz"):
		return extractTarGz(archiveBytes, targetBin, destPath)
	case strings.HasSuffix(lowerName, ".zip"):
		return extractZip(archiveBytes, targetBin, destPath)
	default:
		return writeExecutable(destPath, archiveBytes)
	}
}

func extractTarGz(archiveBytes []byte, targetBin, destPath string) error {
	gzr, err := gzip.NewReader(bytes.NewReader(archiveBytes))
	if err != nil {
		return fmt.Errorf("open gzip archive: %w", err)
	}
	defer gzr.Close()

	tr := tar.NewReader(gzr)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("read tar entry: %w", err)
		}
		if hdr.Typeflag == tar.TypeReg && filepath.Base(hdr.Name) == targetBin {
			binBytes, err := io.ReadAll(io.LimitReader(tr, maxBinaryBytes))
			if err != nil {
				return fmt.Errorf("extract %s from tar.gz: %w", targetBin, err)
			}
			return writeExecutable(destPath, binBytes)
		}
	}
	return fmt.Errorf("binary %q not found inside tar.gz archive", targetBin)
}

func extractZip(archiveBytes []byte, targetBin, destPath string) error {
	zr, err := zip.NewReader(bytes.NewReader(archiveBytes), int64(len(archiveBytes)))
	if err != nil {
		return fmt.Errorf("open zip archive: %w", err)
	}
	for _, f := range zr.File {
		if filepath.Base(f.Name) == targetBin {
			rc, err := f.Open()
			if err != nil {
				return fmt.Errorf("open %s in zip: %w", targetBin, err)
			}
			binBytes, readErr := io.ReadAll(io.LimitReader(rc, maxBinaryBytes))
			_ = rc.Close()
			if readErr != nil {
				return fmt.Errorf("extract %s from zip: %w", targetBin, readErr)
			}
			return writeExecutable(destPath, binBytes)
		}
	}
	return fmt.Errorf("binary %q not found inside zip archive", targetBin)
}

func writeExecutable(destPath string, data []byte) error {
	out, err := os.OpenFile(destPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755) // #nosec G304 G302 -- self-update destination binary requires executable bit
	if err != nil {
		return fmt.Errorf("create dest %s: %w", destPath, err)
	}
	if _, err := out.Write(data); err != nil {
		_ = out.Close()
		return fmt.Errorf("write binary: %w", err)
	}
	return out.Close()
}

// ReplaceExecutable atomically swaps tmpPath into execPath, handling Windows
// where an active executable must be renamed aside before replacement.
func ReplaceExecutable(tmpPath, execPath string) error {
	return replaceExecutableForOS(runtime.GOOS, tmpPath, execPath)
}

func replaceExecutableForOS(goos, tmpPath, execPath string) error {
	if goos == "windows" {
		oldPath := execPath + ".old"
		_ = os.Remove(oldPath)
		if err := os.Rename(execPath, oldPath); err != nil {
			return fmt.Errorf("move active windows binary aside (%s -> %s): %w", execPath, oldPath, err)
		}
		if err := os.Rename(tmpPath, execPath); err != nil {
			_ = os.Rename(oldPath, execPath)
			return fmt.Errorf("replace binary %s: %w", execPath, err)
		}
		_ = os.Remove(oldPath)
		return nil
	}
	if err := os.Rename(tmpPath, execPath); err != nil {
		return fmt.Errorf("replace binary %s: %w", execPath, err)
	}
	return nil
}
