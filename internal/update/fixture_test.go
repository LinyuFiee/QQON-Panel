package update

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// The release packaging test invokes this only with its new temporary fixture.
// It reads metadata, never runs binaries, and never accesses GitHub.
func TestPublisherFixtureCompatibility(t *testing.T) {
	directory := os.Getenv("PANEL_RELEASE_FIXTURE_DIR")
	if directory == "" {
		t.Skip("optional publisher/installer cross-language fixture")
	}
	metadata, err := os.ReadFile(filepath.Join(directory, "release.json"))
	if err != nil {
		t.Fatal(err)
	}
	sums, err := os.ReadFile(filepath.Join(directory, "SHA256SUMS"))
	if err != nil {
		t.Fatal(err)
	}
	var m manifest
	if err := json.Unmarshal(metadata, &m); err != nil {
		t.Fatal(err)
	}
	if m.Repository != "fixture-owner/fixture-panel" {
		t.Fatal("this test accepts only the explicit synthetic fixture repository")
	}
	release := githubRelease{Tag: m.Tag}
	for name := range assetLimits {
		info, err := os.Stat(filepath.Join(directory, name))
		if err != nil {
			t.Fatal(err)
		}
		release.Assets = append(release.Assets, struct {
			Name string `json:"name"`
			URL  string `json:"browser_download_url"`
			Size int64  `json:"size"`
		}{name, canonicalAsset(m.Repository, m.Tag, name), info.Size()})
	}
	if err := validateAssets(m.Repository, release); err != nil {
		t.Fatal(err)
	}
	if err := validateManifest(metadata, sums, m.Repository, m.Version, 1, release); err != nil {
		t.Fatal(err)
	}
}
