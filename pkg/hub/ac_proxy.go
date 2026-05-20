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

package hub

// AC scope proxy.
//
// The scion web UI lets operators bind agents to alteredCarbon (AC)
// scopes. The AC brain server (today: ori-server on port 8787, soon:
// ac server) exposes /v1/* HTTP routes. The browser can't reach it
// directly (corp DNS + UberProxy SSO), so the hub proxies a tiny
// subset on its own /api/v1/ac/* surface:
//
//   GET  /api/v1/ac/scopes            -> flat list of all scope paths,
//                                       optionally rooted under ?prefix=
//   POST /api/v1/ac/scopes            -> create a scope (idempotent;
//                                       parent autovivification by AC)
//
// What this is NOT:
//
//   - A general AC reverse-proxy. We expose ONLY the endpoints the
//     UI uses. Plan/learning reads and writes belong on the agent
//     side (the in-container `ac` CLI), not the operator dashboard.
//   - An auth boundary. We pass through the operator's web session
//     identity by attaching the configured AC_AUTH_TOKEN (a hub-
//     scoped bearer) to the upstream call. If/when AC grows per-
//     user auth, this proxy needs to learn how to derive per-session
//     tokens. For now the hub speaks AC as a single trusted client.
//
// Failure modes are surfaced as 502 (AC unreachable) or the
// upstream status code (passed through for 4xx/5xx with the body
// in a {"error":"..."} envelope to match the rest of the hub API
// shape).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"
)

// ACClient is a minimal HTTP client for the AC brain. It exists so
// the proxy is testable (swap the client for a stub) and so the hub
// doesn't carry a hard dependency on AC's Go types.
type ACClient interface {
	// ListChildren returns the immediate children of scope. Empty
	// scope means the top-level (returns "amplify", "byo-agents",
	// etc.). Order is whatever AC returns; the caller sorts.
	ListChildren(ctx context.Context, scope string) ([]string, error)
	// PutMeta creates or updates a scope. title may be empty; kind
	// defaults to "work-item" on the AC side when empty. Idempotent:
	// existing scopes get their metadata merged.
	PutMeta(ctx context.Context, scope, title, kind, description string) error
}

// httpACClient is the production implementation of ACClient. Holds
// a single http.Client with a sane timeout. The bearer token is
// optional — local ori-server in dev-auth mode accepts unauthenticated
// requests on the loopback bind; production AC will require it.
type httpACClient struct {
	baseURL    string
	authToken  string
	httpClient *http.Client
}

// NewHTTPACClient builds an HTTP client pointed at baseURL (e.g.
// "http://mjk-agent-server.c.googlers.com:8787"). authToken may be
// empty.
func NewHTTPACClient(baseURL, authToken string) ACClient {
	return &httpACClient{
		baseURL:   strings.TrimRight(baseURL, "/"),
		authToken: authToken,
		httpClient: &http.Client{
			Timeout: 5 * time.Second,
		},
	}
}

// childrenResponse mirrors the AC /v1/children/{scope...} JSON shape.
// Captured here so we don't have to import AC's package.
type childrenResponse struct {
	Children []string `json:"children"`
	Scope    string   `json:"scope"`
}

// metaRequest is the body shape AC accepts on PUT /v1/meta/{scope...}.
// Field names match AC's ScopeMeta JSON tags.
type metaRequest struct {
	Title       string `json:"title,omitempty"`
	Kind        string `json:"kind,omitempty"`
	Description string `json:"description,omitempty"`
}

func (c *httpACClient) ListChildren(ctx context.Context, scope string) ([]string, error) {
	if c.baseURL == "" {
		return nil, errors.New("AC base URL not configured")
	}
	// /v1/children/ for top-level (empty scope) is valid; AC returns
	// the root list. /v1/children/amplify returns amplify's kids.
	endpoint := c.baseURL + "/v1/children/" + scope
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	if c.authToken != "" {
		req.Header.Set("Authorization", "Bearer "+c.authToken)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("AC request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("AC returned %d: %s", resp.StatusCode, string(body))
	}
	var cr childrenResponse
	if err := json.NewDecoder(resp.Body).Decode(&cr); err != nil {
		return nil, fmt.Errorf("decode AC response: %w", err)
	}
	return cr.Children, nil
}

func (c *httpACClient) PutMeta(ctx context.Context, scope, title, kind, description string) error {
	if c.baseURL == "" {
		return errors.New("AC base URL not configured")
	}
	if scope == "" {
		return errors.New("scope cannot be empty")
	}
	body, err := json.Marshal(metaRequest{
		Title:       title,
		Kind:        kind,
		Description: description,
	})
	if err != nil {
		return fmt.Errorf("marshal meta: %w", err)
	}
	endpoint := c.baseURL + "/v1/meta/" + scope
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, endpoint, strings.NewReader(string(body)))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.authToken != "" {
		req.Header.Set("Authorization", "Bearer "+c.authToken)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("AC request: %w", err)
	}
	defer resp.Body.Close()
	// AC returns 200 OK or 201 Created on success. We don't
	// distinguish — both mean the scope exists now.
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
	return fmt.Errorf("AC returned %d: %s", resp.StatusCode, string(respBody))
}

// ACProxy is the HTTP handler bundle for /api/v1/ac/*.
type ACProxy struct {
	client ACClient

	// cache holds the recursive scope walk result for cacheTTL.
	// Browser polls /api/v1/ac/scopes on agent-create page load,
	// so multiple operators / tabs hitting it within seconds
	// shouldn't fan out N×O(scopes) traffic to AC. Single cache
	// for all callers — scope listings are not per-operator-private.
	cacheMu      sync.RWMutex
	cacheScopes  []string
	cacheExpires time.Time
	cacheTTL     time.Duration
}

// NewACProxy builds a proxy backed by client. The recursive scope
// walk is cached for cacheTTL; callers can pass a small TTL like
// 30s. Pass 0 to disable caching (every request walks AC fresh).
func NewACProxy(client ACClient, cacheTTL time.Duration) *ACProxy {
	return &ACProxy{client: client, cacheTTL: cacheTTL}
}

// Register mounts the proxy routes on mux. Call before mounting
// the generic /api/v1/ hub handler so the longer-prefix match
// wins on Go's ServeMux.
func (p *ACProxy) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/ac/scopes", p.handleListScopes)
	mux.HandleFunc("POST /api/v1/ac/scopes", p.handleCreateScope)
}

// listScopesResponse is the JSON shape the UI consumes.
type listScopesResponse struct {
	// Scopes is the flat sorted list of every scope path AC knows
	// about, e.g. ["amplify", "amplify/altered-carbon", ...].
	Scopes []string `json:"scopes"`
	// Cached is true when the response was served from the proxy's
	// in-memory cache. Debug surface only.
	Cached bool `json:"cached"`
}

// acProxyWriteJSON / acProxyWriteError are local helpers (the package
// already has writeJSON/writeError on other paths with different
// signatures, so we avoid the name collision by prefixing).
func acProxyWriteJSON(w http.ResponseWriter, status int, body interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func acProxyWriteError(w http.ResponseWriter, status int, code, msg string) {
	acProxyWriteJSON(w, status, map[string]interface{}{
		"error": map[string]string{
			"code":    code,
			"message": msg,
		},
	})
}

// handleListScopes returns every scope path AC knows about, recursively.
//
// Optional query params:
//
//	prefix=amplify    walk only that subtree (returns "amplify",
//	                  "amplify/altered-carbon", ...)
//	no_cache=1        force a fresh walk; bypasses the in-memory cache
//
// The recursive walk is bounded by 200 scopes. AC trees can grow
// unboundedly in principle but the UI surface this serves doesn't
// scale to thousands anyway. If we ever hit the limit we'll add
// paging.
func (p *ACProxy) handleListScopes(w http.ResponseWriter, r *http.Request) {
	prefix := r.URL.Query().Get("prefix")
	noCache := r.URL.Query().Get("no_cache") != ""

	// Cache only works for the full walk (no prefix). Filtering by
	// prefix is rare and shouldn't churn the cache for the common
	// path.
	if prefix == "" && !noCache {
		p.cacheMu.RLock()
		if time.Now().Before(p.cacheExpires) && p.cacheScopes != nil {
			cached := p.cacheScopes
			p.cacheMu.RUnlock()
			acProxyWriteJSON(w, http.StatusOK, listScopesResponse{Scopes: cached, Cached: true})
			return
		}
		p.cacheMu.RUnlock()
	}

	scopes, err := p.walkScopes(r.Context(), prefix, 200)
	if err != nil {
		acProxyWriteError(w, http.StatusBadGateway, "ac_unreachable",
			fmt.Sprintf("could not list scopes from AC: %v", err))
		return
	}
	sort.Strings(scopes)

	if prefix == "" && p.cacheTTL > 0 {
		p.cacheMu.Lock()
		p.cacheScopes = scopes
		p.cacheExpires = time.Now().Add(p.cacheTTL)
		p.cacheMu.Unlock()
	}

	acProxyWriteJSON(w, http.StatusOK, listScopesResponse{Scopes: scopes, Cached: false})
}

// walkScopes does a BFS starting at root. AC's /v1/children returns
// only direct children, so we issue O(N) requests where N is the
// number of internal nodes in the tree. The AC server is local (or
// at least fronted by the same operator's gosso-proxy) so each call
// is sub-ms; 30 scopes ≈ 30 calls ≈ < 1s total. The result is cached
// at the proxy layer to keep this off the hot path.
//
// limit is a safety cap on the total number of returned scopes;
// reached → the partial result is returned with no error. Operators
// see "fewer scopes than expected" instead of a hang.
func (p *ACProxy) walkScopes(ctx context.Context, root string, limit int) ([]string, error) {
	var result []string
	queue := []string{root}
	visited := make(map[string]bool)
	for len(queue) > 0 && len(result) < limit {
		cur := queue[0]
		queue = queue[1:]
		if cur != "" {
			if visited[cur] {
				continue
			}
			visited[cur] = true
			result = append(result, cur)
		}
		children, err := p.client.ListChildren(ctx, cur)
		if err != nil {
			// First failure aborts. If we'd already accumulated
			// some scopes we still want the error surfaced — the
			// UI showing a partial tree silently is worse than
			// showing none.
			return nil, fmt.Errorf("list children of %q: %w", cur, err)
		}
		for _, child := range children {
			var path string
			if cur == "" {
				path = child
			} else {
				path = cur + "/" + child
			}
			if !visited[path] {
				queue = append(queue, path)
			}
		}
	}
	return result, nil
}

// createScopeRequest is the body shape the UI sends.
type createScopeRequest struct {
	// Scope is the dotted-slash path, e.g. "amplify/foo/bar". Required.
	Scope string `json:"scope"`
	// Title is the human display title. Optional. Defaults to the
	// last path segment on the AC side when empty.
	Title string `json:"title,omitempty"`
	// Kind is the scope kind (project, work-item, etc.). Optional.
	// Defaults to "work-item" on the AC side when empty.
	Kind string `json:"kind,omitempty"`
	// Description is freeform notes. Optional.
	Description string `json:"description,omitempty"`
}

// createScopeResponse echoes the created scope. We don't return the
// full AC ScopeMeta because the UI doesn't need it; just confirmation
// of what was created.
type createScopeResponse struct {
	Scope string `json:"scope"`
}

func (p *ACProxy) handleCreateScope(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	body, err := io.ReadAll(io.LimitReader(r.Body, 64*1024))
	if err != nil {
		acProxyWriteError(w, http.StatusBadRequest, "read_body", err.Error())
		return
	}
	var req createScopeRequest
	if err := json.Unmarshal(body, &req); err != nil {
		acProxyWriteError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	// Lightly validate the path. AC's brain stricter; we let the
	// upstream error speak for itself but bail early on obviously-
	// bad input.
	scope := strings.Trim(req.Scope, "/")
	if scope == "" {
		acProxyWriteError(w, http.StatusBadRequest, "missing_scope", "scope field is required")
		return
	}
	if strings.Contains(scope, "//") || strings.ContainsAny(scope, " \t\n") {
		acProxyWriteError(w, http.StatusBadRequest, "invalid_scope",
			"scope path must be slash-separated segments with no whitespace")
		return
	}
	// Reject percent-encoding shenanigans up front; AC's brain does
	// its own checks but failing here gives a clearer error.
	if decoded, err := url.PathUnescape(scope); err != nil || decoded != scope {
		acProxyWriteError(w, http.StatusBadRequest, "invalid_scope",
			"scope path must not contain percent-encoded characters")
		return
	}

	if err := p.client.PutMeta(r.Context(), scope, req.Title, req.Kind, req.Description); err != nil {
		acProxyWriteError(w, http.StatusBadGateway, "ac_unreachable",
			fmt.Sprintf("could not create scope in AC: %v", err))
		return
	}

	// Invalidate the cache so the next list call sees the new
	// scope.  Cheaper than recomputing it here; the next list
	// will re-walk lazily.
	p.cacheMu.Lock()
	p.cacheExpires = time.Time{}
	p.cacheScopes = nil
	p.cacheMu.Unlock()

	acProxyWriteJSON(w, http.StatusCreated, createScopeResponse{Scope: scope})
}
