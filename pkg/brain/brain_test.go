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

package brain

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestResolveServerURL_EnvWins pins that AC_SERVER_URL takes
// precedence over the on-disk config file. Operators sometimes
// override per-shell (e.g. point at a staging brain) and the env
// must win every time.
func TestResolveServerURL_EnvWins(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	// Plant a config file with a different URL than the env var.
	cfgDir := filepath.Join(home, ".config", "altered-carbon")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatalf("mkdir cfg dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, "server.json"),
		[]byte(`{"url":"http://file-source.example/"}`), 0o644); err != nil {
		t.Fatalf("write cfg: %v", err)
	}
	t.Setenv("AC_SERVER_URL", "http://env-source.example/")

	if got := ResolveServerURL(); got != "http://env-source.example/" {
		t.Fatalf("ResolveServerURL: got %q want env URL", got)
	}
}

// TestResolveServerURL_FileFallback covers the common operator case:
// they've run `ac auth set --url ...` once, AC_SERVER_URL is unset,
// and scion start should still pick up the URL automatically.
func TestResolveServerURL_FileFallback(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("AC_SERVER_URL", "")
	cfgDir := filepath.Join(home, ".config", "altered-carbon")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatalf("mkdir cfg dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, "server.json"),
		[]byte(`{"url":"http://from-file.example/"}`), 0o644); err != nil {
		t.Fatalf("write cfg: %v", err)
	}

	if got := ResolveServerURL(); got != "http://from-file.example/" {
		t.Fatalf("ResolveServerURL: got %q want file URL", got)
	}
}

// TestResolveServerURL_EmptyWhenNothingConfigured ensures we return
// "" (not an error, not a panic) when AC isn't configured at all.
// Callers downstream rely on this to mean "AC is disabled here".
func TestResolveServerURL_EmptyWhenNothingConfigured(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("AC_SERVER_URL", "")
	if got := ResolveServerURL(); got != "" {
		t.Fatalf("ResolveServerURL: got %q want \"\"", got)
	}
}

// TestResolveServerURL_MalformedFileIsSilent guards against partial
// writes / corrupt config files turning every scion start into an
// error. Bad JSON should behave the same as a missing file: "".
func TestResolveServerURL_MalformedFileIsSilent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("AC_SERVER_URL", "")
	cfgDir := filepath.Join(home, ".config", "altered-carbon")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatalf("mkdir cfg dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, "server.json"),
		[]byte(`{not json`), 0o644); err != nil {
		t.Fatalf("write cfg: %v", err)
	}
	if got := ResolveServerURL(); got != "" {
		t.Fatalf("ResolveServerURL: got %q want \"\" on bad JSON", got)
	}
}

// TestNewWithEmptyURLReturnsErrDisabled verifies the disabled-brain
// path. Callers depend on this so they can do `b, err := New(cfg);
// if err != nil { /* skip AC */ }` without case-checking.
func TestNewWithEmptyURLReturnsErrDisabled(t *testing.T) {
	b, err := New(Config{})
	if !errors.Is(err, ErrDisabled) {
		t.Fatalf("New(empty): got err=%v want ErrDisabled", err)
	}
	if b != nil {
		t.Fatalf("New(empty): got b=%v want nil", b)
	}
}

// TestDisabledBrainMethodsReturnErrDisabled verifies every public
// method short-circuits cleanly when the receiver is nil. Critical
// for the "AC is unconfigured" case in scion lifecycle hooks where
// the caller may legitimately get a nil *Brain.
func TestDisabledBrainMethodsReturnErrDisabled(t *testing.T) {
	var b *Brain // nil
	ctx := context.Background()

	cases := map[string]func() error{
		"Health":         func() error { return b.Health(ctx) },
		"WritePointer":   func() error { return b.WritePointer(ctx, "test", Pointer{}) },
		"EndPointer":     func() error { return b.EndPointer(ctx, "test") },
		"Heartbeat":      func() error { return b.Heartbeat(ctx, "test") },
		"AppendLearning": func() error { return b.AppendLearning(ctx, "test", "x", []byte("y")) },
		"ReadPlan": func() error {
			_, err := b.ReadPlan(ctx, "test", false)
			return err
		},
		"ReadLearning": func() error {
			_, err := b.ReadLearning(ctx, "test", true)
			return err
		},
	}

	for name, fn := range cases {
		t.Run(name, func(t *testing.T) {
			if err := fn(); !errors.Is(err, ErrDisabled) {
				t.Fatalf("got err=%v want ErrDisabled", err)
			}
		})
	}
}

// TestEnabledReportsCorrectly distinguishes nil receiver from a
// configured Brain. Used in template helpers that conditionally
// render content based on AC presence.
func TestEnabledReportsCorrectly(t *testing.T) {
	var nilBrain *Brain
	if nilBrain.Enabled() {
		t.Fatalf("nil brain reports Enabled=true")
	}

	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()
	b, err := New(Config{URL: srv.URL})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer b.Close()
	if !b.Enabled() {
		t.Fatalf("configured brain reports Enabled=false")
	}
}

// TestHeartbeatHitsHeartbeatEndpoint verifies the wire path. The
// AC heartbeat endpoint is POST /v1/heartbeat/{scope...}.
func TestHeartbeatHitsHeartbeatEndpoint(t *testing.T) {
	var got struct {
		method string
		path   string
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.method = r.Method
		got.path = r.URL.Path
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	b, err := New(Config{URL: srv.URL, HeartbeatTimeout: 2 * time.Second})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer b.Close()

	if err := b.Heartbeat(context.Background(), "amplify/myproj"); err != nil {
		t.Fatalf("Heartbeat: %v", err)
	}
	if got.method != http.MethodPost {
		t.Fatalf("method: got %q want POST", got.method)
	}
	if !strings.HasPrefix(got.path, "/v1/heartbeat/") {
		t.Fatalf("path: got %q want prefix /v1/heartbeat/", got.path)
	}
	if !strings.HasSuffix(got.path, "amplify/myproj") {
		t.Fatalf("path: got %q want suffix amplify/myproj", got.path)
	}
}

// TestWritePointerSendsPutWithJSONBody verifies the lifecycle wire
// path. AC stores pointers at PUT /v1/item/pointer/_default/{scope}.
func TestWritePointerSendsPutWithJSONBody(t *testing.T) {
	var got struct {
		method string
		path   string
		body   string
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.method = r.Method
		got.path = r.URL.Path
		buf := make([]byte, 4096)
		n, _ := r.Body.Read(buf)
		got.body = string(buf[:n])
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	b, _ := New(Config{URL: srv.URL})
	defer b.Close()

	err := b.WritePointer(context.Background(), "amplify/myproj", Pointer{
		Worker:    "test-host",
		StartedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		SessionID: "ses_xyz",
	})
	if err != nil {
		t.Fatalf("WritePointer: %v", err)
	}
	if got.method != http.MethodPut {
		t.Fatalf("method: got %q want PUT", got.method)
	}
	if !strings.Contains(got.path, "/pointer/") {
		t.Fatalf("path: got %q want contains /pointer/", got.path)
	}
	if !strings.Contains(got.body, "test-host") {
		t.Fatalf("body: got %q want contains worker test-host", got.body)
	}
	if !strings.Contains(got.body, "ses_xyz") {
		t.Fatalf("body: got %q want contains session_id ses_xyz", got.body)
	}
}

// TestResolveScopeRejectsBadPaths exercises the path validation
// that the AC brain package enforces. Bad scopes should fail before
// any HTTP call; good scopes should round-trip cleanly.
func TestResolveScopeRejectsBadPaths(t *testing.T) {
	cases := []struct {
		path    string
		wantErr bool
	}{
		{"amplify", false},
		{"amplify/myproj", false},
		{"amplify/my-proj/sub.scope", false},
		{"/amplify", false},         // leading slash trimmed by AC
		{"amplify/", false},         // trailing slash trimmed by AC
		{"", true},                  // empty
		{"amplify//myproj", true},   // empty segment
		{"amplify/_reserved", true}, // underscore-prefix reserved
		{"Amplify", true},           // uppercase rejected
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			_, err := ResolveScope(tc.path)
			if (err != nil) != tc.wantErr {
				t.Fatalf("ResolveScope(%q): err=%v wantErr=%v", tc.path, err, tc.wantErr)
			}
		})
	}
}
