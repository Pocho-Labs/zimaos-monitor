package selfupdate

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestApplyInstallsVerifiedRelease(t *testing.T) {
	currentPath := writeCurrentBinary(t)
	server := releaseFixture(t, "#!/bin/sh\necho v1.1.0\n", "", false)
	var commands []string
	err := Apply(context.Background(), ApplyOptions{
		CurrentVersion: "v1.0.0",
		ExecutablePath: currentPath,
		Endpoint:       server.URL + "/release",
		HTTPClient:     server.Client(),
		HealthDelay:    time.Nanosecond,
		RunCommand: func(_ context.Context, name string, args ...string) error {
			commands = append(commands, name+" "+strings.Join(args, " "))
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	installed, err := os.ReadFile(currentPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(installed, []byte("v1.1.0")) {
		t.Fatalf("candidate was not installed: %q", installed)
	}
	if len(commands) != 2 || !strings.Contains(commands[0], "restart") || !strings.Contains(commands[1], "is-active") {
		t.Fatalf("unexpected commands: %v", commands)
	}
	if _, err := os.Stat(currentPath + ".previous"); !os.IsNotExist(err) {
		t.Fatal("backup should be removed after a healthy update")
	}
}

func TestApplyRollsBackWhenHealthCheckFails(t *testing.T) {
	currentPath := writeCurrentBinary(t)
	original, _ := os.ReadFile(currentPath)
	server := releaseFixture(t, "#!/bin/sh\necho v1.1.0\n", "", false)
	restarts := 0
	err := Apply(context.Background(), ApplyOptions{
		CurrentVersion: "v1.0.0",
		ExecutablePath: currentPath,
		Endpoint:       server.URL + "/release",
		HTTPClient:     server.Client(),
		HealthDelay:    time.Nanosecond,
		RunCommand: func(_ context.Context, _ string, args ...string) error {
			if args[0] == "restart" {
				restarts++
				return nil
			}
			return errors.New("service is inactive")
		},
	})
	if err == nil || !strings.Contains(err.Error(), "rolled back") {
		t.Fatalf("expected rollback error, got %v", err)
	}
	restored, _ := os.ReadFile(currentPath)
	if !bytes.Equal(restored, original) {
		t.Fatalf("previous binary was not restored: %q", restored)
	}
	if restarts != 2 {
		t.Fatalf("restart count = %d, want 2", restarts)
	}
}

func TestApplyRejectsChecksumMismatch(t *testing.T) {
	currentPath := writeCurrentBinary(t)
	original, _ := os.ReadFile(currentPath)
	server := releaseFixture(t, "#!/bin/sh\necho v1.1.0\n", strings.Repeat("0", 64), false)
	err := Apply(context.Background(), ApplyOptions{
		CurrentVersion: "v1.0.0",
		ExecutablePath: currentPath,
		Endpoint:       server.URL + "/release",
		HTTPClient:     server.Client(),
		HealthDelay:    time.Nanosecond,
		RunCommand: func(context.Context, string, ...string) error {
			t.Fatal("systemctl must not run for an invalid checksum")
			return nil
		},
	})
	if err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("expected checksum mismatch, got %v", err)
	}
	unchanged, _ := os.ReadFile(currentPath)
	if !bytes.Equal(unchanged, original) {
		t.Fatal("current binary changed after invalid checksum")
	}
}

func TestApplyDevelopmentBuildUsingGitHubDigest(t *testing.T) {
	currentPath := writeCurrentBinary(t)
	server := releaseFixture(t, "#!/bin/sh\necho v1.1.0\n", "", true)
	err := Apply(context.Background(), ApplyOptions{
		CurrentVersion: "dev",
		ExecutablePath: currentPath,
		Endpoint:       server.URL + "/release",
		HTTPClient:     server.Client(),
		HealthDelay:    time.Nanosecond,
		RunCommand: func(context.Context, string, ...string) error {
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	installed, _ := os.ReadFile(currentPath)
	if !bytes.Contains(installed, []byte("v1.1.0")) {
		t.Fatal("development build was not updated from the GitHub asset digest")
	}
}

func writeCurrentBinary(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "zimaos-monitor")
	if err := os.WriteFile(path, []byte("#!/bin/sh\necho v1.0.0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func releaseFixture(t *testing.T, candidate, checksumOverride string, useDigest bool) *httptest.Server {
	t.Helper()
	archive := buildArchive(t, []byte(candidate))
	sum := sha256.Sum256(archive)
	checksum := hex.EncodeToString(sum[:])
	if checksumOverride != "" {
		checksum = checksumOverride
	}

	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/release":
			archiveAsset := githubAsset{
				Name:               "zimaos-monitor-v1.1.0-linux-amd64.tar.gz",
				BrowserDownloadURL: server.URL + "/archive",
			}
			assets := []githubAsset{archiveAsset}
			if useDigest {
				assets[0].Digest = "sha256:" + hex.EncodeToString(sum[:])
			} else {
				assets = append(assets, githubAsset{
					Name:               "zimaos-monitor-v1.1.0-linux-amd64.tar.gz.sha256",
					BrowserDownloadURL: server.URL + "/checksum",
				})
			}
			json.NewEncoder(w).Encode(githubRelease{
				TagName: "v1.1.0",
				HTMLURL: server.URL + "/notes",
				Assets:  assets,
			})
		case "/archive":
			w.Write(archive)
		case "/checksum":
			w.Write([]byte(checksum + "  zimaos-monitor-v1.1.0-linux-amd64.tar.gz\n"))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

func buildArchive(t *testing.T, binary []byte) []byte {
	t.Helper()
	var output bytes.Buffer
	gz := gzip.NewWriter(&output)
	tarWriter := tar.NewWriter(gz)
	header := &tar.Header{
		Name: "zimaos-monitor-v1.1.0-linux-amd64/zimaos-monitor",
		Mode: 0o755,
		Size: int64(len(binary)),
	}
	if err := tarWriter.WriteHeader(header); err != nil {
		t.Fatal(err)
	}
	if _, err := tarWriter.Write(binary); err != nil {
		t.Fatal(err)
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}
