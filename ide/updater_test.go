package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gilvan-Borges/HiveMind/internal/updater"
)

func TestAppCheckAndDownloadUpdateVerifiedAndCorrupt(t *testing.T) {
	payload := []byte("synthetic-hivemind-installer-bytes")
	sum := sha256.Sum256(payload)
	hexSum := hex.EncodeToString(sum[:])

	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/manifest.json":
			_ = json.NewEncoder(w).Encode(updater.Release{
				Version:   "0.9.0",
				Commit:    "abc1234",
				BuildDate: "2026-11-21T00:00:00Z",
				Artifacts: map[string]updater.Artifact{
					"installer": {
						Name:   "HiveMind-Setup.exe",
						SHA256: hexSum,
						Size:   int64(len(payload)),
					},
				},
			})
		case "/HiveMind-Setup.exe":
			_, _ = w.Write(payload)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	app := NewApp("", true)
	app.updaterBaseURL = srv.URL
	app.updaterHTTP = srv.Client()
	app.updaterVersion = "0.2.0"

	res, err := app.CheckForUpdates()
	if err != nil {
		t.Fatalf("CheckForUpdates failed: %v", err)
	}
	if !res.Available || res.LatestVersion != "0.9.0" || res.SHA256 != hexSum {
		t.Fatalf("unexpected UpdateCheckResult: %+v", res)
	}

	localCache := t.TempDir()
	t.Setenv("LOCALAPPDATA", localCache)
	t.Setenv("XDG_CACHE_HOME", localCache)

	dlPath, err := app.DownloadUpdate(res.LatestVersion, res.ArtifactName, res.DownloadURL, res.SHA256, res.Size)
	if err != nil {
		t.Fatalf("DownloadUpdate valid failed: %v", err)
	}
	defer os.Remove(dlPath)

	gotBytes, err := os.ReadFile(dlPath)
	if err != nil || string(gotBytes) != string(payload) {
		t.Fatalf("downloaded file mismatch: err=%v len=%d", err, len(gotBytes))
	}
	if !strings.HasSuffix(filepath.ToSlash(dlPath), "/HiveMind/updates/HiveMind-Setup.exe") {
		t.Fatalf("unexpected download path: %s", dlPath)
	}

	// Mismatched SHA-256 must fail and leave no corrupt installer behind.
	_ = os.Remove(dlPath)
	if _, err := app.DownloadUpdate(res.LatestVersion, res.ArtifactName, res.DownloadURL, strings.Repeat("0", 64), res.Size); err == nil {
		t.Fatal("expected DownloadUpdate to reject corrupt SHA-256")
	}
	if _, statErr := os.Stat(dlPath); !os.IsNotExist(statErr) {
		t.Fatalf("expected corrupt download not to remain at %s", dlPath)
	}

	// Same/newer local version returns Available=false cleanly with nil error.
	app.updaterVersion = "0.9.0"
	upToDate, err := app.CheckForUpdates()
	if err != nil {
		t.Fatalf("CheckForUpdates up-to-date errored: %v", err)
	}
	if upToDate.Available {
		t.Fatalf("expected Available=false when already on 0.9.0, got %+v", upToDate)
	}
}
