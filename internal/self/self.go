package self

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
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
	BrowserDownloadURL string `json:"browser_download_url"`
	Size               int64  `json:"size"`
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

	client := &http.Client{Timeout: 10 * time.Second}
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

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, targetAsset.BrowserDownloadURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "gkebnkctl-updater")

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("download asset from %s: %w", targetAsset.BrowserDownloadURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("download error (HTTP %d)", resp.StatusCode)
	}

	out, err := os.OpenFile(destPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return fmt.Errorf("create dest %s: %w", destPath, err)
	}
	defer out.Close()

	if _, err := io.Copy(out, resp.Body); err != nil {
		return fmt.Errorf("write binary: %w", err)
	}

	return nil
}
