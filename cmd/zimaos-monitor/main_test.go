package main

import (
	"encoding/json"
	"reflect"
	"testing"
)

func decodeUpdateJSON(t *testing.T, value any) map[string]any {
	t.Helper()
	payload, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatal(err)
	}
	return decoded
}

func assertUpdateJSON(t *testing.T, got map[string]any, want map[string]any) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("update JSON = %#v, want %#v", got, want)
	}
}

func TestUpdateStateCompatibilityBaseline(t *testing.T) {
	t.Run("zimaos", func(t *testing.T) {
		got := decodeUpdateJSON(t, zimaosInfo{
			InstalledVersion: "1.7.0",
			LatestVersion:    "1.7.1",
			ReleaseURL:       "https://example.invalid/zimaos",
			Title:            "presentation ignored by compatibility baseline",
		})
		delete(got, "title")
		assertUpdateJSON(t, got, map[string]any{
			"installed_version": "1.7.0",
			"latest_version":    "1.7.1",
			"release_url":       "https://example.invalid/zimaos",
		})
	})

	t.Run("monitor optional values", func(t *testing.T) {
		got := decodeUpdateJSON(t, monitorUpdateInfo{
			InstalledVersion: "v0.0.2",
			Title:            "zimaos-monitor",
		})
		assertUpdateJSON(t, got, map[string]any{
			"installed_version": "v0.0.2",
			"title":             "zimaos-monitor",
		})
	})
}

func TestUpdateStatesAlwaysIdentifySoftwareTarget(t *testing.T) {
	tests := []struct {
		name  string
		value any
		title string
	}{
		{
			name:  "zimaos current only",
			value: newZimaOSUpdateInfo("1.7.0", "", ""),
			title: "ZimaOS Operating System",
		},
		{
			name:  "zimaos equal versions",
			value: newZimaOSUpdateInfo("1.7.0", "1.7.0", ""),
			title: "ZimaOS Operating System",
		},
		{
			name:  "monitor v prefix",
			value: newMonitorUpdateInfo("v0.0.2", "v0.0.3", ""),
			title: "zimaos-monitor",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fields := decodeUpdateJSON(t, tt.value)
			if fields["title"] != tt.title {
				t.Fatalf("title = %#v, want %q", fields["title"], tt.title)
			}
		})
	}
}

func TestUpdateStateConstructionIsStableAcrossRefreshAndRestart(t *testing.T) {
	tests := []struct {
		name  string
		build func() any
		want  map[string]any
	}{
		{
			name: "zimaos",
			build: func() any {
				return newZimaOSUpdateInfo("1.7.0", "1.7.1", "https://example.invalid/zimaos")
			},
			want: map[string]any{
				"installed_version": "1.7.0",
				"latest_version":    "1.7.1",
				"release_url":       "https://example.invalid/zimaos",
				"title":             "ZimaOS Operating System",
			},
		},
		{
			name: "monitor",
			build: func() any {
				return newMonitorUpdateInfo("v0.0.2", "v0.0.3", "https://example.invalid/monitor")
			},
			want: map[string]any{
				"installed_version": "v0.0.2",
				"latest_version":    "v0.0.3",
				"release_url":       "https://example.invalid/monitor",
				"title":             "zimaos-monitor",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for range 3 {
				assertUpdateJSON(t, decodeUpdateJSON(t, tt.build()), tt.want)
			}
			assertUpdateJSON(t, decodeUpdateJSON(t, tt.build()), tt.want)
		})
	}
}
