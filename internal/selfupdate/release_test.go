package selfupdate

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestIsNewer(t *testing.T) {
	tests := []struct {
		current string
		latest  string
		want    bool
	}{
		{"v1.2.3", "v1.2.4", true},
		{"1.2.3", "v2.0.0", true},
		{"v1.2.3", "v1.2.3", false},
		{"v2.0.0", "v1.9.9", false},
		{"dev", "v1.0.0", true},
		{"v0.0.1-8-ga26cc12-dirty", "v0.0.1", true},
		{"v1.0.0", "v1.0.1-rc1", false},
		{"v1.0.1-rc1", "v1.0.0", false},
	}
	for _, tt := range tests {
		t.Run(tt.current+"_"+tt.latest, func(t *testing.T) {
			if got := IsNewer(tt.current, tt.latest); got != tt.want {
				t.Fatalf("IsNewer(%q, %q) = %v, want %v", tt.current, tt.latest, got, tt.want)
			}
		})
	}
}

func TestFetchLatestSelectsExactAssets(t *testing.T) {
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		json.NewEncoder(w).Encode(githubRelease{
			TagName: "v1.2.3",
			HTMLURL: "https://example.test/release",
			Assets: []githubAsset{
				{Name: "zimaos-monitor-v1.2.3-linux-amd64.tar.gz", BrowserDownloadURL: server.URL + "/archive"},
				{Name: "zimaos-monitor-v1.2.3-linux-amd64.tar.gz.sha256", BrowserDownloadURL: server.URL + "/checksum"},
				{Name: "zimaos-monitor-v1.2.3-linux-arm64.tar.gz", BrowserDownloadURL: server.URL + "/wrong"},
			},
		})
	}))
	defer server.Close()

	release, err := FetchLatest(context.Background(), server.Client(), server.URL, "test")
	if err != nil {
		t.Fatal(err)
	}
	if release.Version != "v1.2.3" || release.AssetURL != server.URL+"/archive" || release.ChecksumURL != server.URL+"/checksum" {
		t.Fatalf("unexpected release: %+v", release)
	}
}

func TestFetchLatestReportsReleaseWithMissingChecksum(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		json.NewEncoder(w).Encode(githubRelease{
			TagName: "v1.2.3",
			Assets: []githubAsset{{
				Name:               "zimaos-monitor-v1.2.3-linux-amd64.tar.gz",
				BrowserDownloadURL: "https://example.test/archive",
				Digest:             "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			}},
		})
	}))
	defer server.Close()
	release, err := FetchLatest(context.Background(), server.Client(), server.URL, "test")
	if err != nil {
		t.Fatal(err)
	}
	if release.Version != "v1.2.3" || release.AssetURL == "" || release.ChecksumURL != "" ||
		release.SHA256 != "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" {
		t.Fatalf("unexpected release: %+v", release)
	}
}
