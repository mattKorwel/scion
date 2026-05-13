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

// Package brain integrates scion with an alteredCarbon
// (https://github.com/mattkorwel/alteredCarbon) brain server.
//
// alteredCarbon is a small, opinionated, git-backed shared brain for
// AI agents. It stores scopes, plans, learnings, pointers (where work
// is happening), and session metadata via an HTTP API. scion can use
// it as a pull-side context store so any agent dispatched on any
// runtime broker can read inherited plans/learnings on first turn and
// after compaction, and so the operator can see "what's going on
// across the fleet" via `ac status`.
//
// This package is a thin facade over alteredCarbon's HTTP client. It
// exists so scion code never imports the AC client types directly —
// all calls go through a Brain handle that takes scion's context and
// returns scion-friendly errors. If alteredCarbon is unconfigured or
// unreachable, every method returns ErrDisabled and callers should
// proceed as if no brain were present.
//
// Wire-up:
//
//	b, err := brain.New(brain.Config{
//	    URL:   "http://localhost:8787",
//	    Token: os.Getenv("AC_AUTH_TOKEN"),
//	})
//	if err != nil { /* AC disabled; fall through */ }
//	defer b.Close()
//
//	// On agent start
//	b.WritePointer(ctx, "amplify/myproj", brain.Pointer{
//	    Worker:    "broker-host-1",
//	    StartedAt: time.Now().UTC(),
//	    SessionID: agentID,
//	})
//
//	// On heartbeat tick
//	b.Heartbeat(ctx, "amplify/myproj")
//
//	// On agent stop
//	b.EndPointer(ctx, "amplify/myproj")
package brain

import (
	"context"
	"errors"
	"fmt"
	"time"

	acapi "github.com/mattkorwel/alteredCarbon/pkg/api"
	acbrain "github.com/mattkorwel/alteredCarbon/pkg/brain"
)

// ErrDisabled is returned by every Brain method when the integration
// is not configured (Config.URL empty) or when the operator passed a
// nil *Brain. Callers should treat this as "AC isn't here; proceed
// without it" — never abort or surface to the user.
var ErrDisabled = errors.New("brain: alteredCarbon integration not configured")

// Config configures a Brain. Either populate explicitly or use
// FromEnv() to pull from AC_SERVER_URL / AC_AUTH_TOKEN.
type Config struct {
	// URL is the alteredCarbon server base URL (e.g.
	// "http://localhost:8787" for a co-located brain, or a corp
	// FQDN if the brain is remote). Empty disables the integration.
	URL string

	// Token is the bearer token sent on every request. Optional;
	// if the brain is configured without auth, leave empty.
	Token string

	// HeartbeatTimeout caps how long a single heartbeat call may
	// take. Default 5 seconds. Heartbeats are best-effort and
	// should not block the broker's tick loop.
	HeartbeatTimeout time.Duration

	// CallTimeout caps how long any non-heartbeat call may take.
	// Default 30 seconds. Read/Write/Pointer operations.
	CallTimeout time.Duration
}

// Pointer is the writer-side projection of an AC pointer record.
// scion populates Worker / SessionID / Cwd as appropriate; brain
// fills schema_version + created_at on the AC side.
type Pointer struct {
	Worker    string
	StartedAt time.Time
	Cwd       string
	SessionID string
}

// Brain is the integration handle. Methods are safe to call
// concurrently; the underlying HTTP client uses Go's standard
// connection pooling.
//
// The zero value is a disabled brain — every method returns
// ErrDisabled. Use New() for a configured brain.
type Brain struct {
	cfg    Config
	client *acapi.Client
}

// New constructs a Brain from cfg. If cfg.URL is empty, returns
// (nil, ErrDisabled) — the caller should keep going without AC.
//
// Defaults applied:
//   - HeartbeatTimeout: 5s
//   - CallTimeout: 30s
func New(cfg Config) (*Brain, error) {
	if cfg.URL == "" {
		return nil, ErrDisabled
	}
	if cfg.HeartbeatTimeout == 0 {
		cfg.HeartbeatTimeout = 5 * time.Second
	}
	if cfg.CallTimeout == 0 {
		cfg.CallTimeout = 30 * time.Second
	}
	return &Brain{
		cfg:    cfg,
		client: acapi.NewClient(cfg.URL, cfg.Token),
	}, nil
}

// Close releases resources. Currently a no-op (HTTP client uses
// Go's default pool); kept for forward-compatibility.
func (b *Brain) Close() error { return nil }

// Enabled reports whether b is a usable brain. Useful for guards
// in template helpers and lifecycle hooks.
func (b *Brain) Enabled() bool { return b != nil && b.client != nil }

// Health pings the AC server. Returns ErrDisabled when b is nil.
func (b *Brain) Health(ctx context.Context) error {
	if !b.Enabled() {
		return ErrDisabled
	}
	ctx, cancel := context.WithTimeout(ctx, b.cfg.CallTimeout)
	defer cancel()
	return b.client.Health(ctx)
}

// ResolveScope parses the slash-separated scope path. Returned
// errors include path-validation failures (uppercase letters,
// reserved underscore prefix, etc.).
func ResolveScope(path string) (acbrain.Scope, error) {
	return acbrain.ParseScope(path)
}

// ----- Pointer + heartbeat (scion lifecycle hooks) -----

// WritePointer commits a pointer for scope. Replaces any existing
// pointer at that scope. EndedAt is left nil (the agent is starting,
// not ending).
func (b *Brain) WritePointer(ctx context.Context, scope string, p Pointer) error {
	if !b.Enabled() {
		return ErrDisabled
	}
	sc, err := acbrain.ParseScope(scope)
	if err != nil {
		return fmt.Errorf("brain: parse scope %q: %w", scope, err)
	}
	startedAt := p.StartedAt
	if startedAt.IsZero() {
		startedAt = time.Now().UTC()
	}
	rec := acbrain.Pointer{
		SchemaVersion: acbrain.SchemaVersion,
		Worker:        p.Worker,
		StartedAt:     startedAt,
		Cwd:           p.Cwd,
		SessionID:     p.SessionID,
	}
	ctx, cancel := context.WithTimeout(ctx, b.cfg.CallTimeout)
	defer cancel()
	return b.client.PutPointer(ctx, sc, rec)
}

// EndPointer marks the pointer at scope as ended (sets EndedAt to
// now). Subsequent reads see Liveness=ended in `ac status`.
//
// Reads the existing pointer first to preserve worker/session/cwd
// fields. If no pointer exists, returns nil (nothing to end).
func (b *Brain) EndPointer(ctx context.Context, scope string) error {
	if !b.Enabled() {
		return ErrDisabled
	}
	sc, err := acbrain.ParseScope(scope)
	if err != nil {
		return fmt.Errorf("brain: parse scope %q: %w", scope, err)
	}
	ctx, cancel := context.WithTimeout(ctx, b.cfg.CallTimeout)
	defer cancel()
	cur, err := b.client.GetPointer(ctx, sc)
	if err != nil {
		if acapi.IsNotFound(err) {
			return nil
		}
		return err
	}
	if cur.EndedAt != nil {
		return nil
	}
	now := time.Now().UTC()
	cur.EndedAt = &now
	return b.client.PutPointer(ctx, sc, cur)
}

// Heartbeat refreshes the in-memory liveness mark for scope on the
// AC server. Best-effort: errors are returned but should not block
// the caller's tick loop.
//
// AC's heartbeat is in-memory and ephemeral — no git write happens.
// 30 seconds is the typical broker tick.
func (b *Brain) Heartbeat(ctx context.Context, scope string) error {
	if !b.Enabled() {
		return ErrDisabled
	}
	sc, err := acbrain.ParseScope(scope)
	if err != nil {
		return fmt.Errorf("brain: parse scope %q: %w", scope, err)
	}
	ctx, cancel := context.WithTimeout(ctx, b.cfg.HeartbeatTimeout)
	defer cancel()
	return b.client.PointerHeartbeat(ctx, sc)
}

// ----- Read helpers (used by template helpers + agent prompt build) -----

// ReadPlan returns the plan content at scope. When inherit is true,
// walks ancestor scopes and returns concatenated bytes (with
// per-segment markers preserved by AC's inherit-walk).
func (b *Brain) ReadPlan(ctx context.Context, scope string, inherit bool) ([]byte, error) {
	return b.readContent(ctx, scope, acbrain.TypePlan, acbrain.DefaultItemName, inherit)
}

// ReadLearning returns the curated learning content at scope. When
// inherit is true, walks ancestors.
func (b *Brain) ReadLearning(ctx context.Context, scope string, inherit bool) ([]byte, error) {
	return b.readContent(ctx, scope, acbrain.TypeLearning, acbrain.DefaultItemName, inherit)
}

func (b *Brain) readContent(ctx context.Context, scope, typeName, itemName string, inherit bool) ([]byte, error) {
	if !b.Enabled() {
		return nil, ErrDisabled
	}
	sc, err := acbrain.ParseScope(scope)
	if err != nil {
		return nil, fmt.Errorf("brain: parse scope %q: %w", scope, err)
	}
	ctx, cancel := context.WithTimeout(ctx, b.cfg.CallTimeout)
	defer cancel()
	if !inherit {
		return b.client.GetItem(ctx, sc, typeName, itemName)
	}
	segs, err := b.client.ReadInherited(ctx, sc, typeName, itemName)
	if err != nil {
		return nil, err
	}
	return concatInheritSegments(segs), nil
}

// concatInheritSegments mirrors the rendering ac plan/learning verbs
// use for --inherit output: per-segment "from:" header followed by
// the segment body, blank line between segments. Keeps callers from
// re-implementing the convention.
func concatInheritSegments(segs []acbrain.InheritSegment) []byte {
	if len(segs) == 0 {
		return nil
	}
	const headerPrefix = "> from: `"
	const headerSuffix = "`\n\n"
	const sep = "\n\n"
	// Pre-compute size to avoid reallocations.
	size := 0
	for _, s := range segs {
		size += len(headerPrefix) + len(s.Scope) + len(headerSuffix) + len(s.Content) + len(sep)
	}
	buf := make([]byte, 0, size)
	for _, s := range segs {
		buf = append(buf, headerPrefix...)
		buf = append(buf, s.Scope...)
		buf = append(buf, headerSuffix...)
		buf = append(buf, s.Content...)
		buf = append(buf, sep...)
	}
	return buf
}

// AppendLearning adds a new timestamped learning item under scope.
// Useful for scion lifecycle events that want to leave a trail in
// the brain (e.g. "agent X started against scope Y at time Z").
//
// itemName should be a short slug; nowSlug() is a reasonable
// default. content is opaque markdown bytes.
func (b *Brain) AppendLearning(ctx context.Context, scope, itemName string, content []byte) error {
	if !b.Enabled() {
		return ErrDisabled
	}
	sc, err := acbrain.ParseScope(scope)
	if err != nil {
		return fmt.Errorf("brain: parse scope %q: %w", scope, err)
	}
	ctx, cancel := context.WithTimeout(ctx, b.cfg.CallTimeout)
	defer cancel()
	return b.client.PutItem(ctx, sc, acbrain.TypeLearning, itemName, content)
}
