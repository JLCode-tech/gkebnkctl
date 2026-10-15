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
const GitHubRepo = "JLCode-tech/gkebnkctl"

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

// DownloadAsset downloads the binary asset matching current OS/Arch.
func DownloadAsset(ctx context.Context, rel *Release, destPath string) error {
	expectedSuffix := fmt.Sprintf("%s_%s", runtime.GOOS, runtime.GOARCH)
	var targetAsset *Asset
	for _, a := range rel.Assets {
		if strings.Contains(a.Name, expectedSuffix) {
			targetAsset = &a
			break
		}
	}

	if targetAsset == nil {
		return fmt.Errorf("no matching release binary for platform %s/%s in release %s",
			runtime.GOOS, runtime.GOARCH, rel.TagName)
	}

	downloadURL := targetAsset.BrowserDownloadURL
	token := getGitHubToken()
	if token != "" && targetAsset.URL != "" {
		downloadURL = targetAsset.URL
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, downloadURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "gkebnkctl-updater")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
		if targetAsset.URL != "" && downloadURL == targetAsset.URL {
			req.Header.Set("Accept", "application/octet-stream")
		}
	}

	client := &http.Client{Timeout: 120 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("download asset from %s: %w", downloadURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("download error (HTTP %d)", resp.StatusCode)
	}

	const maxBinarySize = 500 * 1024 * 1024 // 500MB max limit to guard against decompression bombs
	archiveBytes, err := io.ReadAll(io.LimitReader(resp.Body, maxBinarySize))
	if err != nil {
		return fmt.Errorf("read asset body: %w", err)
	}

	var sumsAsset *Asset
	for i := range rel.Assets {
		if rel.Assets[i].Name == "checksums.txt" {
			sumsAsset = &rel.Assets[i]
			break
		}
	}
	if sumsAsset != nil {
		if err := verifyAssetChecksum(ctx, client, token, sumsAsset, targetAsset.Name, archiveBytes); err != nil {
			return err
		}
	}

	targetName := "gkebnkctl"
	if runtime.GOOS == "windows" {
		targetName = "gkebnkctl.exe"
	}

	if strings.HasSuffix(targetAsset.Name, ".tar.gz") || strings.HasSuffix(targetAsset.Name, ".tgz") {
		gzr, err := gzip.NewReader(bytes.NewReader(archiveBytes))
		if err != nil {
			return fmt.Errorf("open gzip archive: %w", err)
		}
		defer gzr.Close()

		tr := tar.NewReader(gzr)
		var found bool
		for {
			hdr, err := tr.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				return fmt.Errorf("read tar entry: %w", err)
			}
			if hdr.Typeflag == tar.TypeReg && (filepath.Base(hdr.Name) == targetName || hdr.Name == targetName) {
				out, err := os.OpenFile(destPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755) // #nosec G304 G302
				if err != nil {
					return fmt.Errorf("create dest %s: %w", destPath, err)
				}
				if _, err := io.Copy(out, io.LimitReader(tr, maxBinarySize)); err != nil { // #nosec G110
					_ = out.Close()
					return fmt.Errorf("extract binary from tar: %w", err)
				}
				_ = out.Close()
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("binary %s not found inside %s", targetName, targetAsset.Name)
		}
		return nil
	}

	if strings.HasSuffix(targetAsset.Name, ".zip") {
		zr, err := zip.NewReader(bytes.NewReader(archiveBytes), int64(len(archiveBytes)))
		if err != nil {
			return fmt.Errorf("open zip archive: %w", err)
		}
		var found bool
		for _, f := range zr.File {
			if filepath.Base(f.Name) == targetName || f.Name == targetName {
				rc, err := f.Open()
				if err != nil {
					return fmt.Errorf("open file in zip: %w", err)
				}
				out, err := os.OpenFile(destPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755) // #nosec G304 G302
				if err != nil {
					_ = rc.Close()
					return fmt.Errorf("create dest %s: %w", destPath, err)
				}
				_, copyErr := io.Copy(out, io.LimitReader(rc, maxBinarySize)) // #nosec G110
				_ = rc.Close()
				_ = out.Close()
				if copyErr != nil {
					return fmt.Errorf("extract binary from zip: %w", copyErr)
				}
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("binary %s not found inside %s", targetName, targetAsset.Name)
		}
		return nil
	}

	out, err := os.OpenFile(destPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755) // #nosec G304 G302 -- self-update destination binary requires executable bit
	if err != nil {
		return fmt.Errorf("create dest %s: %w", destPath, err)
	}
	defer out.Close()

	if _, err := io.Copy(out, bytes.NewReader(archiveBytes)); err != nil {
		return fmt.Errorf("write binary to %s: %w", destPath, err)
	}

	return nil
}

func verifyAssetChecksum(ctx context.Context, client *http.Client, token string, sumsAsset *Asset, assetName string, data []byte) error {
	sumsURL := sumsAsset.BrowserDownloadURL
	if token != "" && sumsAsset.URL != "" {
		sumsURL = sumsAsset.URL
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, sumsURL, nil)
	if err != nil {
		return fmt.Errorf("create checksums request: %w", err)
	}
	req.Header.Set("User-Agent", "gkebnkctl-updater")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
		if sumsAsset.URL != "" && sumsURL == sumsAsset.URL {
			req.Header.Set("Accept", "application/octet-stream")
		}
	}

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("download checksums.txt: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("download checksums.txt error (HTTP %d)", resp.StatusCode)
	}

	var expectedHash string
	scanner := bufio.NewScanner(io.LimitReader(resp.Body, 1<<20))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) >= 2 && strings.TrimPrefix(fields[1], "*") == assetName {
			expectedHash = fields[0]
			break
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read checksums.txt: %w", err)
	}
	if expectedHash == "" {
		return fmt.Errorf("asset %s not found in checksums.txt", assetName)
	}

	sum := sha256.Sum256(data)
	actualHash := hex.EncodeToString(sum[:])
	if !strings.EqualFold(actualHash, expectedHash) {
		return fmt.Errorf("checksum mismatch for %s: expected %s, got %s", assetName, expectedHash, actualHash)
	}
	return nil
}
