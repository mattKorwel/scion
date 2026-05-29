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

package log

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// TestLogFileCreatedWithRequestedMode_BypassesUmask pins the chmod
// step that ensures /home/scion/agent.log ends up writable by the
// scion user even when sciontool creates it as root with an
// umask-022 process state.
//
// Without the chmod, OpenFile(.., 0666) ANDed with umask 022 gives
// the file 0644, and a uid drop from root to scion later in the
// container's lifecycle blocks every subsequent agent.log write
// with a "permission denied" warning (the exact symptom the user
// reported in the web terminal).
func TestLogFileCreatedWithRequestedMode_BypassesUmask(t *testing.T) {
	// Force a restrictive umask so we can prove the chmod is doing
	// the work, not the test environment's permissive default.
	old := syscall.Umask(0o022)
	defer syscall.Umask(old)

	tmp := t.TempDir()
	// Point the log subsystem at a fresh file.
	mu.Lock()
	logPath = filepath.Join(tmp, "agent.log")
	initialized = true
	mu.Unlock()

	// Reset shared state so other tests in the package can rerun
	// Init without inheriting our path.
	defer func() {
		mu.Lock()
		logPath = ""
		initialized = false
		mu.Unlock()
	}()

	TaggedInfo("test", "create the file")

	info, err := os.Stat(logPath)
	if err != nil {
		t.Fatalf("stat agent.log: %v", err)
	}
	// Mask off type bits; we only care about the perm bits.
	got := info.Mode().Perm()
	if got != 0o666 {
		t.Fatalf("agent.log perms = %o, want 0666 (umask should have been bypassed)", got)
	}
}
