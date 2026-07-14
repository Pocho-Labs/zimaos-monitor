package selfupdate

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

const maxBinarySize = 100 << 20

type CommandRunner func(ctx context.Context, name string, args ...string) error

type ApplyOptions struct {
	CurrentVersion string
	ExecutablePath string
	ServiceName    string
	UserAgent      string
	Endpoint       string
	HTTPClient     *http.Client
	HealthDelay    time.Duration
	RunCommand     CommandRunner
}

func Apply(ctx context.Context, opts ApplyOptions) error {
	setApplyDefaults(&opts)
	lock, err := acquireLock(opts.ExecutablePath + ".update.lock")
	if err != nil {
		return err
	}
	defer lock.Close()

	release, err := FetchLatest(ctx, opts.HTTPClient, opts.Endpoint, opts.UserAgent)
	if err != nil {
		return err
	}
	if !IsNewer(opts.CurrentVersion, release.Version) {
		return fmt.Errorf("release %s is not newer than %s", release.Version, opts.CurrentVersion)
	}
	if release.AssetURL == "" || (release.SHA256 == "" && release.ChecksumURL == "") {
		return fmt.Errorf("release %s lacks required amd64 artifact or SHA-256 digest", release.Version)
	}

	workDir, err := os.MkdirTemp(filepath.Dir(opts.ExecutablePath), ".zimaos-monitor-update-")
	if err != nil {
		return fmt.Errorf("create update directory: %w", err)
	}
	defer os.RemoveAll(workDir)

	archivePath := filepath.Join(workDir, release.AssetName)
	if err := downloadFile(ctx, opts.HTTPClient, release.AssetURL, archivePath, maxBinarySize*2); err != nil {
		return err
	}
	expected := release.SHA256
	if expected == "" {
		expected, err = downloadChecksum(ctx, opts.HTTPClient, release.ChecksumURL)
		if err != nil {
			return err
		}
	}
	if err := verifyChecksum(archivePath, expected); err != nil {
		return err
	}

	candidate := filepath.Join(workDir, "zimaos-monitor")
	if err := extractBinary(archivePath, candidate); err != nil {
		return err
	}
	output, err := exec.CommandContext(ctx, candidate, "--version").Output()
	if err != nil {
		return fmt.Errorf("verify candidate binary: %w", err)
	}
	if strings.TrimSpace(string(output)) != release.Version {
		return fmt.Errorf("candidate version %q does not match release %q", strings.TrimSpace(string(output)), release.Version)
	}

	backup := opts.ExecutablePath + ".previous"
	if err := copyFile(opts.ExecutablePath, backup, 0o755); err != nil {
		return fmt.Errorf("backup current binary: %w", err)
	}
	if err := os.Rename(candidate, opts.ExecutablePath); err != nil {
		os.Remove(backup)
		return fmt.Errorf("replace binary: %w", err)
	}

	if err := restartAndCheck(ctx, opts); err != nil {
		if rollbackErr := rollback(ctx, opts, backup); rollbackErr != nil {
			return fmt.Errorf("update failed: %v; rollback failed: %w", err, rollbackErr)
		}
		return fmt.Errorf("update failed and was rolled back: %w", err)
	}
	if err := os.Remove(backup); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove update backup: %w", err)
	}
	return nil
}

func setApplyDefaults(opts *ApplyOptions) {
	if opts.Endpoint == "" {
		opts.Endpoint = LatestReleaseEndpoint
	}
	if opts.HTTPClient == nil {
		opts.HTTPClient = &http.Client{Timeout: 2 * time.Minute}
	}
	if opts.ServiceName == "" {
		opts.ServiceName = "zimaos-monitor.service"
	}
	if opts.HealthDelay == 0 {
		opts.HealthDelay = 10 * time.Second
	}
	if opts.RunCommand == nil {
		opts.RunCommand = runCommand
	}
}

func acquireLock(path string) (*os.File, error) {
	lock, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open update lock: %w", err)
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		lock.Close()
		return nil, fmt.Errorf("another update is already running")
	}
	return lock, nil
}

func downloadFile(ctx context.Context, client *http.Client, url, path string, limit int64) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("create download request: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("download %s: %w", filepath.Base(path), err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download %s: http %d", filepath.Base(path), resp.StatusCode)
	}
	if resp.ContentLength > limit {
		return fmt.Errorf("download %s exceeds size limit", filepath.Base(path))
	}

	dst, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("create %s: %w", filepath.Base(path), err)
	}
	defer dst.Close()
	written, err := io.Copy(dst, io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return fmt.Errorf("write %s: %w", filepath.Base(path), err)
	}
	if written > limit {
		return fmt.Errorf("download %s exceeds size limit", filepath.Base(path))
	}
	return dst.Sync()
}

func downloadChecksum(ctx context.Context, client *http.Client, url string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("create checksum request: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("download checksum: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download checksum: http %d", resp.StatusCode)
	}
	payload, err := io.ReadAll(io.LimitReader(resp.Body, 4097))
	if err != nil {
		return "", fmt.Errorf("read checksum: %w", err)
	}
	fields := strings.Fields(string(payload))
	if len(fields) == 0 || len(fields[0]) != sha256.Size*2 {
		return "", fmt.Errorf("invalid checksum payload")
	}
	if _, err := hex.DecodeString(fields[0]); err != nil {
		return "", fmt.Errorf("invalid checksum payload: %w", err)
	}
	return strings.ToLower(fields[0]), nil
}

func verifyChecksum(path, expected string) error {
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open artifact for checksum: %w", err)
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return fmt.Errorf("hash artifact: %w", err)
	}
	actual := hex.EncodeToString(hash.Sum(nil))
	if actual != expected {
		return fmt.Errorf("artifact checksum mismatch")
	}
	return nil
}

func extractBinary(archivePath, targetPath string) error {
	file, err := os.Open(archivePath)
	if err != nil {
		return fmt.Errorf("open release archive: %w", err)
	}
	defer file.Close()
	gz, err := gzip.NewReader(file)
	if err != nil {
		return fmt.Errorf("open release gzip: %w", err)
	}
	defer gz.Close()

	reader := tar.NewReader(gz)
	found := false
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("read release archive: %w", err)
		}
		if filepath.Base(header.Name) != "zimaos-monitor" {
			continue
		}
		if found || header.Typeflag != tar.TypeReg || header.Size <= 0 || header.Size > maxBinarySize {
			return fmt.Errorf("release archive contains an invalid binary entry")
		}
		target, err := os.OpenFile(targetPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o755)
		if err != nil {
			return fmt.Errorf("create candidate binary: %w", err)
		}
		written, copyErr := io.Copy(target, io.LimitReader(reader, maxBinarySize+1))
		closeErr := target.Close()
		if copyErr != nil || closeErr != nil || written != header.Size || written > maxBinarySize {
			return fmt.Errorf("extract candidate binary")
		}
		found = true
	}
	if !found {
		return fmt.Errorf("release archive does not contain zimaos-monitor")
	}
	return nil
}

func restartAndCheck(ctx context.Context, opts ApplyOptions) error {
	if err := opts.RunCommand(ctx, "systemctl", "restart", opts.ServiceName); err != nil {
		return err
	}
	timer := time.NewTimer(opts.HealthDelay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
	}
	return opts.RunCommand(ctx, "systemctl", "is-active", "--quiet", opts.ServiceName)
}

func rollback(ctx context.Context, opts ApplyOptions, backup string) error {
	if err := os.Rename(backup, opts.ExecutablePath); err != nil {
		return fmt.Errorf("restore previous binary: %w", err)
	}
	if err := opts.RunCommand(ctx, "systemctl", "restart", opts.ServiceName); err != nil {
		return fmt.Errorf("restart previous binary: %w", err)
	}
	return nil
}

func runCommand(ctx context.Context, name string, args ...string) error {
	output, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}
	return nil
}

func copyFile(source, target string, mode os.FileMode) error {
	src, err := os.Open(source)
	if err != nil {
		return err
	}
	defer src.Close()
	dst, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(dst, src); err != nil {
		dst.Close()
		return err
	}
	if err := dst.Sync(); err != nil {
		dst.Close()
		return err
	}
	return dst.Close()
}
