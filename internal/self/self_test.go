package self

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestCheckUpdate(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rel := Release{
			TagName: "v1.2.0",
			Name:    "Release v1.2.0",
			HTMLURL: "https://github.com/JLCode-tech/gkebnkctl/releases/tag/v1.2.0",
		}
		_ = json.NewEncoder(w).Encode(rel)
	}))
	defer srv.Close()

	ctx := context.Background()
	// test parsing with direct request
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	var rel Release
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		t.Fatal(err)
	}
	if rel.TagName != "v1.2.0" {
		t.Errorf("expected tag v1.2.0, got %s", rel.TagName)
	}
}

func buildTarGz(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gzw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gzw)
	for name, content := range files {
		hdr := &tar.Header{
			Name:     name,
			Mode:     0o755,
			Size:     int64(len(content)),
			Typeflag: tar.TypeReg,
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("write tar header: %v", err)
		}
		if _, err := tw.Write(content); err != nil {
			t.Fatalf("write tar body: %v", err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("close tar writer: %v", err)
	}
	if err := gzw.Close(); err != nil {
		t.Fatalf("close gzip writer: %v", err)
	}
	return buf.Bytes()
}

func buildZip(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("create zip entry: %v", err)
		}
		if _, err := w.Write(content); err != nil {
			t.Fatalf("write zip entry: %v", err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("close zip writer: %v", err)
	}
	return buf.Bytes()
}

func TestDownloadAssetChecksumVerification(t *testing.T) {
	assetName := fmt.Sprintf("gkebnkctl_%s_%s", runtime.GOOS, runtime.GOARCH)
	payload := []byte("fake-binary-payload")
	sum := sha256.Sum256(payload)
	validHash := hex.EncodeToString(sum[:])

	var checksumLine string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/" + assetName:
			_, _ = w.Write(payload)
		case "/checksums.txt":
			_, _ = w.Write([]byte(checksumLine))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	rel := &Release{
		TagName: "v1.2.0",
		Assets: []Asset{
			{
				Name:               assetName,
				BrowserDownloadURL: srv.URL + "/" + assetName,
			},
			{
				Name:               "checksums.txt",
				BrowserDownloadURL: srv.URL + "/checksums.txt",
			},
		},
	}

	tmpDir := t.TempDir()
	destPath := filepath.Join(tmpDir, "gkebnkctl")

	// 1. Valid checksum succeeds
	checksumLine = fmt.Sprintf("%s  %s\n", validHash, assetName)
	if err := DownloadAsset(context.Background(), rel, destPath); err != nil {
		t.Fatalf("expected valid checksum download to succeed, got: %v", err)
	}
	got, err := os.ReadFile(destPath)
	if err != nil {
		t.Fatalf("reading downloaded binary: %v", err)
	}
	if string(got) != string(payload) {
		t.Fatalf("unexpected binary content: %q", string(got))
	}

	// 2. Mismatched checksum fails
	checksumLine = fmt.Sprintf("0000000000000000000000000000000000000000000000000000000000000000  %s\n", assetName)
	badDest := filepath.Join(tmpDir, "gkebnkctl-bad")
	err = DownloadAsset(context.Background(), rel, badDest)
	if err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("expected checksum mismatch error, got: %v", err)
	}
	if _, statErr := os.Stat(badDest); !os.IsNotExist(statErr) {
		t.Fatalf("expected bad destination file not to exist on checksum mismatch")
	}
}

func TestDownloadAsset_TarGzAndChecksum(t *testing.T) {
	wantBinary := []byte("#!/bin/sh\necho gkebnkctl-0.2.0\n")
	archiveBytes := buildTarGz(t, map[string][]byte{
		"README.md": []byte("readme"),
		"gkebnkctl": wantBinary,
	})
	assetName := "gkebnkctl_0.2.0_linux_amd64.tar.gz"
	sum := sha256.Sum256(archiveBytes)
	checksumsTxt := fmt.Sprintf("%x  %s\n", sum[:], assetName)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/" + assetName:
			_, _ = w.Write(archiveBytes)
		case "/checksums.txt":
			_, _ = w.Write([]byte(checksumsTxt))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	rel := &Release{
		TagName: "v0.2.0",
		Assets: []Asset{
			{Name: assetName, BrowserDownloadURL: srv.URL + "/" + assetName},
			{Name: "checksums.txt", BrowserDownloadURL: srv.URL + "/checksums.txt"},
		},
	}

	destPath := filepath.Join(t.TempDir(), "gkebnkctl")
	if err := DownloadAssetForPlatform(context.Background(), rel, "linux", "amd64", destPath); err != nil {
		t.Fatalf("DownloadAssetForPlatform failed: %v", err)
	}

	got, err := os.ReadFile(destPath)
	if err != nil {
		t.Fatalf("read extracted binary: %v", err)
	}
	if !bytes.Equal(got, wantBinary) {
		t.Fatalf("extracted binary = %q, want %q", got, wantBinary)
	}
}

func TestDownloadAsset_WindowsZip(t *testing.T) {
	wantBinary := []byte("MZ-fake-windows-pe")
	archiveBytes := buildZip(t, map[string][]byte{
		"gkebnkctl.exe": wantBinary,
	})
	assetName := "gkebnkctl_0.2.0_windows_amd64.zip"
	sum := sha256.Sum256(archiveBytes)
	checksumsTxt := fmt.Sprintf("%x  %s\n", sum[:], assetName)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/" + assetName:
			_, _ = w.Write(archiveBytes)
		case "/checksums.txt":
			_, _ = w.Write([]byte(checksumsTxt))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	rel := &Release{
		TagName: "v0.2.0",
		Assets: []Asset{
			{Name: assetName, BrowserDownloadURL: srv.URL + "/" + assetName},
			{Name: "checksums.txt", BrowserDownloadURL: srv.URL + "/checksums.txt"},
		},
	}

	destDir := t.TempDir()
	execPath := filepath.Join(destDir, "gkebnkctl.exe")
	if err := os.WriteFile(execPath, []byte("old-binary"), 0o600); err != nil {
		t.Fatal(err)
	}
	tmpPath := execPath + ".tmp"
	if err := DownloadAssetForPlatform(context.Background(), rel, "windows", "amd64", tmpPath); err != nil {
		t.Fatalf("DownloadAssetForPlatform windows zip failed: %v", err)
	}
	if err := replaceExecutableForOS("windows", tmpPath, execPath); err != nil {
		t.Fatalf("replaceExecutableForOS windows failed: %v", err)
	}
	got, err := os.ReadFile(execPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, wantBinary) {
		t.Fatalf("windows binary = %q, want %q", got, wantBinary)
	}
}
