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

package agent

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/GoogleCloudPlatform/scion/pkg/api"
)

// TestResolveBrainScopeFromOptsPrefersOptsEnv verifies the scope
// resolution order. opts.Env beats host AC_DEFAULT_SCOPE so a
// per-agent --scope on `scion start` always wins over the operator's
// shell.
func TestResolveBrainScopeFromOptsPrefersOptsEnv(t *testing.T) {
	t.Setenv("AC_DEFAULT_SCOPE", "from-host")

	// Empty opts.Env: falls through to host env.
	if got := resolveBrainScopeFromOpts(api.StartOptions{}); got != "from-host" {
		t.Fatalf("empty opts: got %q want from-host", got)
	}

	// Explicit opts.Env wins.
	opts := api.StartOptions{Env: map[string]string{"AC_DEFAULT_SCOPE": "from-opts"}}
	if got := resolveBrainScopeFromOpts(opts); got != "from-opts" {
		t.Fatalf("explicit opts: got %q want from-opts", got)
	}
}

// TestResolveBrainScopeFromOptsEmptyByDefault verifies that without
// any AC config, the helper returns "" so callers short-circuit
// cleanly.
func TestResolveBrainScopeFromOptsEmptyByDefault(t *testing.T) {
	t.Setenv("AC_DEFAULT_SCOPE", "")
	if got := resolveBrainScopeFromOpts(api.StartOptions{}); got != "" {
		t.Fatalf("empty all: got %q want empty", got)
	}
}

// TestResolveBrainFromEnvNilWithoutURL verifies that AC integration
// stays disabled when AC_SERVER_URL isn't set, even if other AC_
// vars are present.
func TestResolveBrainFromEnvNilWithoutURL(t *testing.T) {
	t.Setenv("AC_SERVER_URL", "")
	t.Setenv("AC_AUTH_TOKEN", "irrelevant")
	if b := resolveBrainFromEnv(); b != nil {
		t.Fatalf("got non-nil brain from empty AC_SERVER_URL")
	}
}

// TestNotifyBrainAgentStartedWritesPointer wires the lifecycle hook
// against an in-process AC stand-in. Verifies the pointer PUT lands
// at the correct URL with the expected fields populated.
func TestNotifyBrainAgentStartedWritesPointer(t *testing.T) {
	var (
		hits      atomic.Int32
		gotPath   atomic.Value // string
		gotBody   atomic.Value // string
		gotMethod atomic.Value // string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		gotMethod.Store(r.Method)
		gotPath.Store(r.URL.Path)
		buf := make([]byte, 4096)
		n, _ := r.Body.Read(buf)
		gotBody.Store(string(buf[:n]))
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	t.Setenv("AC_SERVER_URL", srv.URL)
	t.Setenv("AC_AUTH_TOKEN", "")
	t.Setenv("AC_BROKER", "test-broker")

	opts := api.StartOptions{
		Env: map[string]string{"AC_DEFAULT_SCOPE": "amplify/myproj"},
	}
	info := &api.AgentInfo{ID: "agent-xyz"}

	notifyBrainAgentStarted(context.Background(), opts, info)

	if hits.Load() != 1 {
		t.Fatalf("hits: got %d want 1", hits.Load())
	}
	if m, _ := gotMethod.Load().(string); m != http.MethodPut {
		t.Fatalf("method: got %q want PUT", m)
	}
	path, _ := gotPath.Load().(string)
	if !strings.Contains(path, "/pointer/") || !strings.Contains(path, "amplify/myproj") {
		t.Fatalf("path: got %q want /pointer/.../amplify/myproj", path)
	}
	body, _ := gotBody.Load().(string)
	if !strings.Contains(body, "agent-xyz") {
		t.Fatalf("body: got %q want contains agent-xyz", body)
	}
	if !strings.Contains(body, "test-broker") {
		t.Fatalf("body: got %q want contains worker test-broker", body)
	}
}

// TestNotifyBrainAgentStartedNoOpWithoutScope verifies the hook is a
// pure no-op when no scope is configured. The fake server should
// receive zero requests.
func TestNotifyBrainAgentStartedNoOpWithoutScope(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	t.Setenv("AC_SERVER_URL", srv.URL)
	t.Setenv("AC_DEFAULT_SCOPE", "") // no host fallback either

	notifyBrainAgentStarted(context.Background(), api.StartOptions{}, &api.AgentInfo{ID: "x"})

	if hits.Load() != 0 {
		t.Fatalf("hits: got %d want 0", hits.Load())
	}
}

// TestNotifyBrainAgentStartedNoOpWithoutServerURL verifies the hook
// is a pure no-op when AC_SERVER_URL isn't set, even if a scope is.
func TestNotifyBrainAgentStartedNoOpWithoutServerURL(t *testing.T) {
	t.Setenv("AC_SERVER_URL", "")
	t.Setenv("AC_DEFAULT_SCOPE", "amplify/myproj")
	// No fake server; if the hook tries to dial it'll just fail
	// quietly. No panic = pass.
	notifyBrainAgentStarted(context.Background(), api.StartOptions{}, &api.AgentInfo{ID: "x"})
}
