// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package harnessconfigsync provides shared logic for downloading and
// staging harness-configs from a Scion Hub into the local filesystem
// (~/.scion/harness-configs/<name>/). It is the runtime broker's
// counterpart to the operator-facing `scion harness-config pull` CLI:
// callers do not need to know about signed URLs, hash verification, or
// two-phase download semantics.
//
// The package is intentionally minimal — see cmd/harness_config.go for
// the richer CLI workflows (list, diff, etc.).
package harnessconfigsync

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/GoogleCloudPlatform/scion/pkg/config"
	"github.com/GoogleCloudPlatform/scion/pkg/hubclient"
	"github.com/GoogleCloudPlatform/scion/pkg/transfer"
)

// ErrNotOnHub indicates the named harness-config doesn't exist on the
// Hub. Callers can use errors.Is to distinguish a real "not present"
// signal from transient network errors.
var ErrNotOnHub = fmt.Errorf("harness-config not found on hub")

// EnsureLocal makes sure a harness-config with the given name is installed
// locally at ~/.scion/harness-configs/<name>/ AND is up to date with the Hub.
//
// Behavior:
//   - Not on disk, present on Hub  -> download + install.
//   - On disk, matches Hub content -> no-op (return existing path).
//   - On disk, STALE vs Hub        -> re-download in place (refresh).
//   - On disk, Hub unreachable     -> return existing path (degraded; a local
//     copy is better than failing the dispatch).
//   - On disk, not on Hub          -> return existing path (local-only or
//     built-in seeded config is authoritative).
//   - Not on disk, Hub unreachable -> propagate error (retriable).
//   - Not on disk, not on Hub      -> ErrNotOnHub.
//
// The staleness check is what makes `scion harness-config push` actually reach
// brokers. The previous fast path returned any on-disk copy unconditionally, so
// a broker served a stale harness-config forever after a push (it never
// re-pulled). Freshness is determined by the same per-file hash comparison the
// `harness-config` CLI uses on push (CollectFiles vs the Hub download manifest).
//
// Returns the absolute on-disk path to the installed directory.
func EnsureLocal(ctx context.Context, hubClient hubclient.Client, name string) (string, error) {
	if name == "" {
		return "", fmt.Errorf("harnessconfigsync.EnsureLocal: empty name")
	}
	if hubClient == nil {
		return "", fmt.Errorf("harnessconfigsync.EnsureLocal: nil hub client")
	}

	// Is it already on disk somewhere reachable by FindHarnessConfigDir?
	// We pass an empty grovePath because the broker dispatch path doesn't
	// know about user-grove dirs — a global install is what we want anyway.
	localPath := ""
	if hcDir, err := config.FindHarnessConfigDir(name, ""); err == nil {
		localPath = hcDir.Path
	}

	// Resolve hub-side metadata. List + filter rather than Get-by-name
	// because Get requires the opaque hub ID; the broker dispatch carries
	// only the human name.
	resp, err := hubClient.HarnessConfigs().List(ctx, &hubclient.ListHarnessConfigsOptions{
		Name:   name,
		Status: "active",
	})
	if err != nil {
		// Hub unreachable. Prefer an existing local copy over failing the
		// dispatch; otherwise propagate (the caller treats it as retriable).
		if localPath != "" {
			return localPath, nil
		}
		return "", fmt.Errorf("hub list: %w", err)
	}

	var match *hubclient.HarnessConfig
	for i := range resp.HarnessConfigs {
		hc := &resp.HarnessConfigs[i]
		if hc.Name == name || hc.Slug == name {
			match = hc
			break
		}
	}
	if match == nil {
		// Not on the hub. A local copy (local-only or built-in seeded) is
		// authoritative in that case; keep it.
		if localPath != "" {
			return localPath, nil
		}
		return "", fmt.Errorf("%w: %q", ErrNotOnHub, name)
	}

	// Already on disk: refresh only if the content drifted from the Hub.
	if localPath != "" {
		fresh, ferr := localMatchesHub(ctx, hubClient, match, localPath)
		if ferr != nil || fresh {
			// Either it's up to date, or we couldn't determine freshness
			// (transient) — in both cases prefer the existing local copy
			// over a needless or risky re-download.
			return localPath, nil
		}
		// Stale: fall through and re-download into the same directory.
	}

	// Resolve install destination. Refresh in place when we already have a
	// copy; otherwise use the global harness-configs dir (the same shape
	// `scion harness-config pull` writes to so FindHarnessConfigDir sees it).
	destDir := localPath
	if destDir == "" {
		globalDir, err := config.GetGlobalDir()
		if err != nil {
			return "", fmt.Errorf("resolve global dir: %w", err)
		}
		destDir = filepath.Join(globalDir, "harness-configs", match.Name)
	}

	if err := downloadAndInstall(ctx, hubClient, match, destDir); err != nil {
		// If we were merely refreshing an existing copy, keep serving the
		// stale-but-usable one rather than failing the dispatch.
		if localPath != "" {
			return localPath, nil
		}
		return "", err
	}
	return destDir, nil
}

// localMatchesHub reports whether the on-disk harness-config at localDir has the
// same file set and per-file hashes as the Hub's active version. It mirrors the
// change-detection the `scion harness-config` CLI performs on push (CollectFiles
// local hashes vs the Hub download manifest).
func localMatchesHub(ctx context.Context, hubClient hubclient.Client, hc *hubclient.HarnessConfig, localDir string) (bool, error) {
	localFiles, err := hubclient.CollectFiles(localDir, nil)
	if err != nil {
		return false, err
	}

	dl, err := hubClient.HarnessConfigs().RequestDownloadURLs(ctx, hc.ID)
	if err != nil {
		return false, err
	}

	remote := make(map[string]string, len(dl.Files))
	for _, f := range dl.Files {
		remote[f.Path] = f.Hash
	}
	if len(remote) != len(localFiles) {
		return false, nil
	}
	for _, lf := range localFiles {
		rh, ok := remote[lf.Path]
		if !ok || rh != lf.Hash {
			return false, nil
		}
	}
	return true, nil
}

// downloadAndInstall performs the two-phase download:
//  1. fetch every file's bytes into memory and verify per-file hashes
//  2. only then write to disk
//
// This avoids leaving a partially-installed harness-config on disk if
// any download or hash check fails mid-flight.
func downloadAndInstall(ctx context.Context, hubClient hubclient.Client, hc *hubclient.HarnessConfig, destDir string) error {
	downloadResp, err := hubClient.HarnessConfigs().RequestDownloadURLs(ctx, hc.ID)
	if err != nil {
		return fmt.Errorf("request download URLs: %w", err)
	}
	if len(downloadResp.Files) == 0 {
		return fmt.Errorf("hub reports zero files for harness-config %q", hc.Name)
	}

	useHubAPI := anyLocalDownloadURLs(downloadResp.Files)

	type pendingFile struct {
		relPath string
		content []byte
	}
	pending := make([]pendingFile, 0, len(downloadResp.Files))

	for _, file := range downloadResp.Files {
		content, dlErr := downloadOne(ctx, hubClient, hc.ID, file, useHubAPI)
		if dlErr != nil {
			return fmt.Errorf("download %s: %w", file.Path, dlErr)
		}
		if file.Hash != "" {
			if got := transfer.HashBytes(content); got != file.Hash {
				return fmt.Errorf(
					"hash mismatch for %s: hub announced %s, got %s",
					file.Path, file.Hash, got,
				)
			}
		}
		pending = append(pending, pendingFile{relPath: file.Path, content: content})
	}

	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", destDir, err)
	}
	for _, f := range pending {
		filePath := filepath.Join(destDir, filepath.FromSlash(f.relPath))
		if err := os.MkdirAll(filepath.Dir(filePath), 0o755); err != nil {
			return fmt.Errorf("mkdir parent of %s: %w", filePath, err)
		}
		if err := os.WriteFile(filePath, f.content, 0o644); err != nil {
			return fmt.Errorf("write %s: %w", filePath, err)
		}
	}
	return nil
}

func downloadOne(ctx context.Context, hubClient hubclient.Client, id string, file hubclient.DownloadURLInfo, useHubAPI bool) ([]byte, error) {
	if useHubAPI {
		return hubClient.HarnessConfigs().ReadFile(ctx, id, file.Path)
	}
	return hubClient.HarnessConfigs().DownloadFile(ctx, file.URL)
}

// anyLocalDownloadURLs returns true if any file URL is a file:// URI,
// which is the convention the Hub uses to signal that signed-URL
// storage isn't available (single-host dev hubs, hub-on-the-same-box,
// etc.) and clients should read through the Hub HTTP API instead.
func anyLocalDownloadURLs(files []hubclient.DownloadURLInfo) bool {
	for _, f := range files {
		if strings.HasPrefix(f.URL, "file://") {
			return true
		}
	}
	return false
}
