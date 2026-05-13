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
	"errors"
	"os"
	"time"

	"github.com/GoogleCloudPlatform/scion/pkg/api"
	"github.com/GoogleCloudPlatform/scion/pkg/brain"
	"github.com/GoogleCloudPlatform/scion/pkg/util"
)

// brain_hooks.go — best-effort lifecycle hooks that publish agent
// state to an alteredCarbon brain server.
//
// All hooks are no-ops when AC isn't configured (no AC_SERVER_URL
// env var on the broker host, or the configured brain returns
// brain.ErrDisabled). Errors during a brain call are logged at
// debug level and swallowed; the agent lifecycle never fails because
// AC was unreachable.
//
// Three hooks:
//
//	notifyBrainAgentStarted — called from Start() after the container
//	                           comes up, with the resolved AgentInfo.
//	                           Writes a pointer to AC.
//	notifyBrainAgentStopped — called from Stop()/Delete() after the
//	                           container exits. Marks the AC pointer
//	                           ended.
//	resolveBrainScopeFromOpts — pulls the scope from opts.Env or
//	                            falls back to host AC_DEFAULT_SCOPE.

// resolveBrainFromEnv constructs a Brain from the broker's host
// environment. Returns nil when AC isn't configured. Caller treats
// nil as "skip brain hook" without distinguishing further; details
// are in the debug log.
func resolveBrainFromEnv() *brain.Brain {
	url := os.Getenv("AC_SERVER_URL")
	if url == "" {
		return nil
	}
	b, err := brain.New(brain.Config{
		URL:   url,
		Token: os.Getenv("AC_AUTH_TOKEN"),
	})
	if err != nil {
		// brain.ErrDisabled is the only expected error here (empty
		// URL); we already short-circuited that above. Anything else
		// is unexpected; log and bail.
		if !errors.Is(err, brain.ErrDisabled) {
			util.Debugf("brain: New: %v", err)
		}
		return nil
	}
	return b
}

// resolveBrainScopeFromOpts returns the AC scope this agent is
// dispatched against, or "" if AC integration isn't requested. Reads
// opts.Env first (set by `scion start --scope` in cmd/common.go);
// falls back to the broker host's $AC_DEFAULT_SCOPE.
func resolveBrainScopeFromOpts(opts api.StartOptions) string {
	if opts.Env != nil {
		if v := opts.Env["AC_DEFAULT_SCOPE"]; v != "" {
			return v
		}
	}
	return os.Getenv("AC_DEFAULT_SCOPE")
}

// notifyBrainAgentStarted writes a pointer to AC marking this agent
// as live at scope. Best-effort; failures are debug-logged.
//
// The Worker field gets the broker host's hostname so `ac status`
// renders something operator-meaningful. SessionID gets the agent's
// scion-side ID so subsequent `ac scope show <scope>` correlates
// the brain pointer with the running container.
func notifyBrainAgentStarted(ctx context.Context, opts api.StartOptions, info *api.AgentInfo) {
	scope := resolveBrainScopeFromOpts(opts)
	if scope == "" {
		return
	}
	b := resolveBrainFromEnv()
	if b == nil {
		return
	}
	defer func() { _ = b.Close() }()

	host, _ := os.Hostname()
	if h := os.Getenv("AC_BROKER"); h != "" {
		host = h
	}
	p := brain.Pointer{
		Worker:    host,
		StartedAt: time.Now().UTC(),
		SessionID: info.ID,
		// Cwd is intentionally empty — scion's container model means
		// "where work is happening" is the container, not a path on
		// the broker host. Operators reach a running agent via
		// `scion attach`, not by ssh-ing to a directory.
	}
	if err := b.WritePointer(ctx, scope, p); err != nil {
		util.Debugf("brain: WritePointer(%q): %v", scope, err)
	}
}

// notifyBrainAgentStopped marks the AC pointer at scope as ended.
// Best-effort; failures are debug-logged. No-op if AC integration
// wasn't configured for this agent.
//
// scope is resolved from the AgentInfo's labels (see
// scion.acScope label set by the runtime when --scope was used) or,
// if absent, from the broker host's $AC_DEFAULT_SCOPE.
func notifyBrainAgentStopped(ctx context.Context, scope string) {
	if scope == "" {
		scope = os.Getenv("AC_DEFAULT_SCOPE")
	}
	if scope == "" {
		return
	}
	b := resolveBrainFromEnv()
	if b == nil {
		return
	}
	defer func() { _ = b.Close() }()

	if err := b.EndPointer(ctx, scope); err != nil {
		util.Debugf("brain: EndPointer(%q): %v", scope, err)
	}
}
