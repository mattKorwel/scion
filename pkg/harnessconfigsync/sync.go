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

// EnsureLocal makes sure a harness-config with the given name is
// installed locally at ~/.scion/harness-configs/<name>/. If it's
// already there, the function is a no-op. If not, it fetches the
// active version from the Hub, verifies each file's hash, and writes
// the bundle atomically (two-phase: fetch + verify everything before
// writing anything).
//
// Returns the absolute on-disk path to the installed directory.
//
// Network errors propagate; the caller is expected to treat them as
// retriable. ErrNotOnHub is returned only when the Hub explicitly
// reports no match (an authoritative "no").
func EnsureLocal(ctx context.Context, hubClient hubclient.Client, name string) (string, error) {
	if name == "" {
		return "", fmt.Errorf("harnessconfigsync.EnsureLocal: empty name")
	}
	if hubClient == nil {
		return "", fmt.Errorf("harnessconfigsync.EnsureLocal: nil hub client")
	}

	// Fast path: already on disk somewhere reachable by FindHarnessConfigDir.
	// We pass an empty grovePath because the broker dispatch path doesn't
	// know about user-grove dirs — a global install is what we want anyway.
	if hcDir, err := config.FindHarnessConfigDir(name, ""); err == nil {
		return hcDir.Path, nil
	}

	// Resolve hub-side metadata. List + filter rather than Get-by-name
	// because Get requires the opaque hub ID; the broker dispatch carries
	// only the human name.
	resp, err := hubClient.HarnessConfigs().List(ctx, &hubclient.ListHarnessConfigsOptions{
		Name:   name,
		Status: "active",
	})
	if err != nil {
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
		return "", fmt.Errorf("%w: %q", ErrNotOnHub, name)
	}

	// Resolve install destination: same shape that `scion harness-config
	// pull` writes to so the next FindHarnessConfigDir call sees it.
	globalDir, err := config.GetGlobalDir()
	if err != nil {
		return "", fmt.Errorf("resolve global dir: %w", err)
	}
	destDir := filepath.Join(globalDir, "harness-configs", match.Name)

	if err := downloadAndInstall(ctx, hubClient, match, destDir); err != nil {
		return "", err
	}
	return destDir, nil
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
