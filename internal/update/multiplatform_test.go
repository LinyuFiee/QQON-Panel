package update

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"
)

func multiplatformFixture(version string) (githubRelease, []byte, []byte, []byte) {
	release, _, metadata, sums := fixture(version, 1)
	var m manifest
	_ = json.Unmarshal(metadata, &m)
	m.Format = 3
	binary := []byte("fake PE executable")
	archive := []byte("fake Windows zip")
	binaryHash, archiveHash := sha256.Sum256(binary), sha256.Sum256(archive)
	m.Binaries = append(m.Binaries, binaryRecord{OS: "windows", Arch: "amd64", Name: "panel.exe", Size: int64(len(binary)), SHA256: hex.EncodeToString(binaryHash[:]),
		Archive: archiveRecord{Name: "panel-windows-amd64.zip", Size: int64(len(archive)), SHA256: hex.EncodeToString(archiveHash[:])}})
	metadata, _ = json.Marshal(m)
	metadataHash := sha256.Sum256(metadata)
	lines := strings.Split(strings.TrimSpace(string(sums)), "\n")
	for i, line := range lines {
		if strings.HasSuffix(line, "  release.json") {
			lines[i] = hex.EncodeToString(metadataHash[:]) + "  release.json"
		}
	}
	lines = append(lines, hex.EncodeToString(archiveHash[:])+"  panel-windows-amd64.zip", strings.Repeat("3", 64)+"  install.ps1", strings.Repeat("4", 64)+"  update.ps1")
	sums = []byte(strings.Join(lines, "\n") + "\n")
	for _, name := range []string{"panel-windows-amd64.zip", "install.ps1", "update.ps1"} {
		size := int64(50)
		if strings.HasSuffix(name, ".zip") {
			size = int64(len(archive))
		}
		release.Assets = append(release.Assets, struct {
			Name string `json:"name"`
			URL  string `json:"browser_download_url"`
			Size int64  `json:"size"`
		}{name, canonicalAsset(m.Repository, m.Tag, name), size})
	}
	for i := range release.Assets {
		switch release.Assets[i].Name {
		case "release.json":
			release.Assets[i].Size = int64(len(metadata))
		case "SHA256SUMS":
			release.Assets[i].Size = int64(len(sums))
		}
	}
	api, _ := json.Marshal(release)
	return release, api, metadata, sums
}

func TestWindowsRequiresMatchingReleaseAndCommand(t *testing.T) {
	s := testService(t, "example/panel")
	s.current.OS = "windows"
	_, api, metadata, sums := multiplatformFixture("0.5.0")
	var calls atomic.Int32
	fakeRelease(t, s, api, metadata, sums, &calls)
	status, code := request(t, s, "POST")
	if code != 200 || status.State != "available" || !status.Latest.AssetsReady || strings.Contains(status.Command, "sudo") || !strings.Contains(status.Command, "update.ps1") || !strings.HasSuffix(status.Command, "-Version v0.5.0") {
		t.Fatalf("unexpected Windows update: %+v", status)
	}
}

func TestWindowsDoesNotClaimLegacyLinuxReleaseReady(t *testing.T) {
	s := testService(t, "example/panel")
	s.current.OS = "windows"
	_, api, metadata, sums := fixture("0.4.0", 1)
	var calls atomic.Int32
	fakeRelease(t, s, api, metadata, sums, &calls)
	status, _ := request(t, s, "POST")
	if status.State != "incompatible" || status.UpdateAvailable || status.Latest.AssetsReady {
		t.Fatalf("Linux-only release offered to Windows: %+v", status)
	}
}

func TestFormatThreeRequiresAllWindowsAssetsAndUniquePlatforms(t *testing.T) {
	for _, change := range []string{"missing_script", "wrong_name", "duplicate_platform", "wrong_archive"} {
		t.Run(change, func(t *testing.T) {
			release, _, metadata, sums := multiplatformFixture("0.5.0")
			if change == "missing_script" {
				for i, asset := range release.Assets {
					if asset.Name == "install.ps1" {
						release.Assets = append(release.Assets[:i], release.Assets[i+1:]...)
						break
					}
				}
				if validateAssets("example/panel", release) == nil {
					t.Fatal("incomplete Windows scripts accepted")
				}
				return
			}
			var m manifest
			_ = json.Unmarshal(metadata, &m)
			switch change {
			case "wrong_name":
				m.Binaries[2].Name = "panel"
			case "duplicate_platform":
				m.Binaries[2] = m.Binaries[0]
			case "wrong_archive":
				m.Binaries[2].Archive.Name = "panel-linux-amd64.tar.gz"
			}
			metadata, _ = json.Marshal(m)
			hash := sha256.Sum256(metadata)
			lines := strings.Split(string(sums), "\n")
			for i, line := range lines {
				if strings.HasSuffix(line, "  release.json") {
					lines[i] = hex.EncodeToString(hash[:]) + "  release.json"
				}
			}
			sums = []byte(strings.Join(lines, "\n"))
			if validateManifest(metadata, sums, "example/panel", "0.5.0", 1, release) == nil {
				t.Fatal("invalid platform record accepted")
			}
		})
	}
}
