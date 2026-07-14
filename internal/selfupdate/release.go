package selfupdate

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

const LatestReleaseEndpoint = "https://api.github.com/repos/Pocho-Labs/zimaos-monitor/releases/latest"

var stableVersionPattern = regexp.MustCompile(`^v?(\d+)\.(\d+)\.(\d+)$`)
var developmentVersionPattern = regexp.MustCompile(`^v?\d+\.\d+\.\d+-\d+-g[0-9a-f]+(?:-dirty)?$`)

type Release struct {
	Version      string
	ReleaseURL   string
	AssetURL     string
	ChecksumURL  string
	SHA256       string
	AssetName    string
	ChecksumName string
}

type Checker struct {
	endpoint   string
	interval   time.Duration
	httpClient *http.Client
	userAgent  string
	mu         sync.RWMutex
	latest     Release
}

type githubRelease struct {
	TagName    string        `json:"tag_name"`
	HTMLURL    string        `json:"html_url"`
	Draft      bool          `json:"draft"`
	Prerelease bool          `json:"prerelease"`
	Assets     []githubAsset `json:"assets"`
}

type githubAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
	Digest             string `json:"digest"`
}

func NewChecker(interval time.Duration, userAgent string) *Checker {
	if interval < time.Hour {
		log.Printf("warn: monitor update interval %v too short, enforcing minimum 1h", interval)
		interval = time.Hour
	}
	return &Checker{
		endpoint:  LatestReleaseEndpoint,
		interval:  interval,
		userAgent: userAgent,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

func (c *Checker) Start(ctx context.Context) {
	go func() {
		c.fetch(ctx)
		ticker := time.NewTicker(c.interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				c.fetch(ctx)
			}
		}
	}()
}

func (c *Checker) Latest() Release {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.latest
}

func (c *Checker) fetch(ctx context.Context) {
	release, err := FetchLatest(ctx, c.httpClient, c.endpoint, c.userAgent)
	if err != nil {
		log.Printf("warn: monitor release check: %v", err)
		return
	}
	c.mu.Lock()
	c.latest = release
	c.mu.Unlock()
}

func FetchLatest(ctx context.Context, client *http.Client, endpoint, userAgent string) (Release, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return Release{}, fmt.Errorf("create release request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", userAgent)

	resp, err := client.Do(req)
	if err != nil {
		return Release{}, fmt.Errorf("fetch release: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Release{}, fmt.Errorf("fetch release: http %d", resp.StatusCode)
	}

	var raw githubRelease
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return Release{}, fmt.Errorf("decode release: %w", err)
	}
	if raw.Draft || raw.Prerelease || !stableVersionPattern.MatchString(raw.TagName) {
		return Release{}, fmt.Errorf("latest release %q is not stable", raw.TagName)
	}

	assetName := fmt.Sprintf("zimaos-monitor-%s-linux-amd64.tar.gz", raw.TagName)
	checksumName := assetName + ".sha256"
	release := Release{
		Version:      raw.TagName,
		ReleaseURL:   raw.HTMLURL,
		AssetName:    assetName,
		ChecksumName: checksumName,
	}
	for _, asset := range raw.Assets {
		switch asset.Name {
		case assetName:
			release.AssetURL = asset.BrowserDownloadURL
			release.SHA256 = strings.TrimPrefix(asset.Digest, "sha256:")
		case checksumName:
			release.ChecksumURL = asset.BrowserDownloadURL
		}
	}
	return release, nil
}

func IsNewer(current, latest string) bool {
	latestParts, ok := parseVersion(latest)
	if !ok {
		return false
	}
	currentParts, ok := parseVersion(current)
	if !ok {
		return current == "dev" || developmentVersionPattern.MatchString(current)
	}
	for i := range currentParts {
		if latestParts[i] != currentParts[i] {
			return latestParts[i] > currentParts[i]
		}
	}
	return false
}

func parseVersion(value string) ([3]uint64, bool) {
	match := stableVersionPattern.FindStringSubmatch(value)
	if match == nil {
		return [3]uint64{}, false
	}
	var parsed [3]uint64
	for i := range parsed {
		n, err := strconv.ParseUint(match[i+1], 10, 64)
		if err != nil {
			return [3]uint64{}, false
		}
		parsed[i] = n
	}
	return parsed, true
}
