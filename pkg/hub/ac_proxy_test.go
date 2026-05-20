// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package hub

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeACClient is an in-memory ACClient for unit tests. The scope
// tree is built from a map[parent][]child. Empty parent is the root.
type fakeACClient struct {
	mu       sync.Mutex
	tree     map[string][]string // parent -> children
	puts     []string            // record of PutMeta calls (scope only)
	listErrs int                 // increment to make ListChildren fail
	putErrs  int                 // increment to make PutMeta fail
	calls    atomic.Int64
}

func newFakeAC(tree map[string][]string) *fakeACClient {
	if tree == nil {
		tree = map[string][]string{}
	}
	return &fakeACClient{tree: tree}
}

func (f *fakeACClient) ListChildren(ctx context.Context, scope string) ([]string, error) {
	f.calls.Add(1)
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.listErrs > 0 {
		f.listErrs--
		return nil, fmt.Errorf("synthetic ListChildren failure")
	}
	out := append([]string(nil), f.tree[scope]...)
	return out, nil
}

func (f *fakeACClient) PutMeta(ctx context.Context, scope, title, kind, description string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.putErrs > 0 {
		f.putErrs--
		return fmt.Errorf("synthetic PutMeta failure")
	}
	f.puts = append(f.puts, scope)
	// Add to tree so subsequent ListChildren sees it.
	idx := strings.LastIndex(scope, "/")
	var parent, child string
	if idx == -1 {
		parent = ""
		child = scope
	} else {
		parent = scope[:idx]
		child = scope[idx+1:]
	}
	// Don't double-add.
	for _, existing := range f.tree[parent] {
		if existing == child {
			return nil
		}
	}
	f.tree[parent] = append(f.tree[parent], child)
	return nil
}

// TestACProxy_ListScopes_Walk verifies the recursive walk returns
// every scope path in the tree, sorted, no duplicates.
func TestACProxy_ListScopes_Walk(t *testing.T) {
	tree := map[string][]string{
		"":                          {"amplify", "byo-agents"},
		"amplify":                   {"altered-carbon", "ori"},
		"amplify/ori":               {"harness-serve", "v4"},
		"amplify/ori/harness-serve": {"cloudcode"},
		"byo-agents":                {"teambuilding"},
	}
	p := NewACProxy(newFakeAC(tree), 0)
	mux := http.NewServeMux()
	p.Register(mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/v1/ac/scopes")
	if err != nil {
		t.Fatalf("GET failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}
	var got listScopesResponse
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	want := []string{
		"amplify",
		"amplify/altered-carbon",
		"amplify/ori",
		"amplify/ori/harness-serve",
		"amplify/ori/harness-serve/cloudcode",
		"amplify/ori/v4",
		"byo-agents",
		"byo-agents/teambuilding",
	}
	if len(got.Scopes) != len(want) {
		t.Fatalf("want %d scopes, got %d: %v", len(want), len(got.Scopes), got.Scopes)
	}
	for i, s := range want {
		if got.Scopes[i] != s {
			t.Errorf("scope[%d]: want %q, got %q", i, s, got.Scopes[i])
		}
	}
	if got.Cached {
		t.Errorf("first call should not be cached")
	}
}

// TestACProxy_ListScopes_Cache verifies repeated calls within TTL
// don't fan out to AC.
func TestACProxy_ListScopes_Cache(t *testing.T) {
	fake := newFakeAC(map[string][]string{
		"":        {"amplify"},
		"amplify": {"foo"},
	})
	p := NewACProxy(fake, 1*time.Second)
	mux := http.NewServeMux()
	p.Register(mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	// First call: 3 ListChildren (root, amplify, amplify/foo).
	resp1, _ := http.Get(srv.URL + "/api/v1/ac/scopes")
	resp1.Body.Close()
	callsAfter1 := fake.calls.Load()
	if callsAfter1 == 0 {
		t.Fatalf("expected non-zero calls after first request")
	}

	// Second call within TTL: should serve from cache, no new
	// ListChildren calls.
	resp2, _ := http.Get(srv.URL + "/api/v1/ac/scopes")
	defer resp2.Body.Close()
	var got listScopesResponse
	_ = json.NewDecoder(resp2.Body).Decode(&got)
	if !got.Cached {
		t.Errorf("second call should be cached")
	}
	if fake.calls.Load() != callsAfter1 {
		t.Errorf("expected no new AC calls; got %d -> %d", callsAfter1, fake.calls.Load())
	}
}

// TestACProxy_ListScopes_NoCache_QueryParam bypasses the cache.
func TestACProxy_ListScopes_NoCache_QueryParam(t *testing.T) {
	fake := newFakeAC(map[string][]string{"": {"amplify"}})
	p := NewACProxy(fake, 1*time.Minute)
	mux := http.NewServeMux()
	p.Register(mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	r1, _ := http.Get(srv.URL + "/api/v1/ac/scopes")
	r1.Body.Close()
	before := fake.calls.Load()
	r2, _ := http.Get(srv.URL + "/api/v1/ac/scopes?no_cache=1")
	r2.Body.Close()
	if fake.calls.Load() <= before {
		t.Errorf("no_cache=1 should bypass cache; calls did not increment")
	}
}

// TestACProxy_ListScopes_Prefix walks only the subtree.
func TestACProxy_ListScopes_Prefix(t *testing.T) {
	tree := map[string][]string{
		"":            {"amplify", "byo-agents"},
		"amplify":     {"ori"},
		"amplify/ori": {"v4"},
		"byo-agents":  {"teambuilding"},
	}
	p := NewACProxy(newFakeAC(tree), 0)
	mux := http.NewServeMux()
	p.Register(mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, _ := http.Get(srv.URL + "/api/v1/ac/scopes?prefix=amplify")
	defer resp.Body.Close()
	var got listScopesResponse
	_ = json.NewDecoder(resp.Body).Decode(&got)
	want := []string{"amplify", "amplify/ori", "amplify/ori/v4"}
	if len(got.Scopes) != len(want) {
		t.Fatalf("want %v, got %v", want, got.Scopes)
	}
	for i := range want {
		if got.Scopes[i] != want[i] {
			t.Errorf("scope[%d]: want %q got %q", i, want[i], got.Scopes[i])
		}
	}
}

// TestACProxy_ListScopes_ACError surfaces 502.
func TestACProxy_ListScopes_ACError(t *testing.T) {
	fake := newFakeAC(nil)
	fake.listErrs = 1
	p := NewACProxy(fake, 0)
	mux := http.NewServeMux()
	p.Register(mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, _ := http.Get(srv.URL + "/api/v1/ac/scopes")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadGateway {
		t.Errorf("want 502, got %d", resp.StatusCode)
	}
}

// TestACProxy_CreateScope_OK records the put + clears the cache.
func TestACProxy_CreateScope_OK(t *testing.T) {
	fake := newFakeAC(map[string][]string{"": {"amplify"}})
	p := NewACProxy(fake, 1*time.Minute)
	mux := http.NewServeMux()
	p.Register(mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	// Prime the cache.
	r1, _ := http.Get(srv.URL + "/api/v1/ac/scopes")
	r1.Body.Close()

	body := strings.NewReader(`{"scope":"amplify/new","title":"My new project","kind":"project"}`)
	resp, err := http.Post(srv.URL+"/api/v1/ac/scopes", "application/json", body)
	if err != nil {
		t.Fatalf("POST failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("want 201, got %d", resp.StatusCode)
	}
	var got createScopeResponse
	_ = json.NewDecoder(resp.Body).Decode(&got)
	if got.Scope != "amplify/new" {
		t.Errorf("want scope amplify/new, got %q", got.Scope)
	}
	if len(fake.puts) != 1 || fake.puts[0] != "amplify/new" {
		t.Errorf("want puts=[amplify/new], got %v", fake.puts)
	}

	// Cache should have been invalidated; next list re-walks and
	// the new scope shows up.
	r2, _ := http.Get(srv.URL + "/api/v1/ac/scopes")
	defer r2.Body.Close()
	var listed listScopesResponse
	_ = json.NewDecoder(r2.Body).Decode(&listed)
	if listed.Cached {
		t.Errorf("list after create should not be cached")
	}
	found := false
	for _, s := range listed.Scopes {
		if s == "amplify/new" {
			found = true
		}
	}
	if !found {
		t.Errorf("new scope not in re-listed scopes: %v", listed.Scopes)
	}
}

// TestACProxy_CreateScope_Validation rejects bad inputs.
func TestACProxy_CreateScope_Validation(t *testing.T) {
	p := NewACProxy(newFakeAC(nil), 0)
	mux := http.NewServeMux()
	p.Register(mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	cases := []struct {
		name string
		body string
	}{
		{"empty scope", `{"scope":""}`},
		{"slash-only scope", `{"scope":"/"}`},
		{"whitespace", `{"scope":"foo bar"}`},
		{"double slash", `{"scope":"foo//bar"}`},
		{"percent encoded", `{"scope":"foo%2Fbar"}`},
		{"malformed json", `{not json`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			resp, _ := http.Post(srv.URL+"/api/v1/ac/scopes", "application/json", strings.NewReader(c.body))
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusBadRequest {
				t.Errorf("want 400, got %d", resp.StatusCode)
			}
		})
	}
}

// TestACProxy_CreateScope_ACError surfaces 502.
func TestACProxy_CreateScope_ACError(t *testing.T) {
	fake := newFakeAC(nil)
	fake.putErrs = 1
	p := NewACProxy(fake, 0)
	mux := http.NewServeMux()
	p.Register(mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, _ := http.Post(srv.URL+"/api/v1/ac/scopes",
		"application/json", strings.NewReader(`{"scope":"amplify/x"}`))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadGateway {
		t.Errorf("want 502, got %d", resp.StatusCode)
	}
}

// TestHTTPACClient_Roundtrip exercises the real HTTP client against a
// stub HTTP server that mimics the AC route shape.
func TestHTTPACClient_Roundtrip(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		switch {
		case r.Method == "GET" && strings.HasPrefix(r.URL.Path, "/v1/children/"):
			w.Header().Set("Content-Type", "application/json")
			scope := strings.TrimPrefix(r.URL.Path, "/v1/children/")
			body := childrenResponse{Children: []string{"alpha", "beta"}, Scope: scope}
			_ = json.NewEncoder(w).Encode(body)
		case r.Method == "PUT" && strings.HasPrefix(r.URL.Path, "/v1/meta/"):
			if got := r.Header.Get("Authorization"); got != "Bearer testtoken" {
				t.Errorf("want Bearer testtoken auth, got %q", got)
			}
			w.WriteHeader(http.StatusOK)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	c := NewHTTPACClient(srv.URL, "testtoken")
	kids, err := c.ListChildren(context.Background(), "amplify")
	if err != nil {
		t.Fatalf("ListChildren: %v", err)
	}
	if len(kids) != 2 || kids[0] != "alpha" {
		t.Errorf("unexpected children: %v", kids)
	}
	if err := c.PutMeta(context.Background(), "amplify/new", "T", "project", "D"); err != nil {
		t.Fatalf("PutMeta: %v", err)
	}
	if calls < 2 {
		t.Errorf("expected at least 2 server calls, got %d", calls)
	}
}
