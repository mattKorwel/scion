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

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// stubIdentity is a minimal Identity implementation for tests that only
// need to check round-trip preservation through the ticket store.
type stubIdentity struct{ id string }

func (s *stubIdentity) ID() string   { return s.id }
func (s *stubIdentity) Type() string { return "user" }

// TestValidatePTYTicket_RoundTrip verifies the happy-path:
// mint a ticket for an agent, validate it for that agent, get the
// original identity back.
func TestValidatePTYTicket_RoundTrip(t *testing.T) {
	s := &Server{}
	ident := &stubIdentity{id: "user-1"}

	s.ptyTicketsMu.Lock()
	s.ptyTickets = map[string]ptyTicketEntry{
		"tkt-abc": {identity: ident, agentID: "agent-1", expiresAt: time.Now().Add(time.Minute)},
	}
	s.ptyTicketsMu.Unlock()

	got := s.validatePTYTicketForAgent(context.Background(), "tkt-abc", "agent-1")
	if got == nil {
		t.Fatal("validatePTYTicketForAgent: got nil identity, want stubIdentity")
	}
	assert.Equal(t, "user-1", got.ID())
}

// TestValidatePTYTicket_SingleUse verifies that a ticket is consumed
// on first call and a second call returns nil (so a leaked ticket
// can't be replayed).
func TestValidatePTYTicket_SingleUse(t *testing.T) {
	s := &Server{}
	s.ptyTicketsMu.Lock()
	s.ptyTickets = map[string]ptyTicketEntry{
		"tkt-once": {identity: &stubIdentity{id: "u"}, agentID: "a", expiresAt: time.Now().Add(time.Minute)},
	}
	s.ptyTicketsMu.Unlock()

	if got := s.validatePTYTicketForAgent(context.Background(), "tkt-once", "a"); got == nil {
		t.Fatal("first validate should succeed")
	}
	if got := s.validatePTYTicketForAgent(context.Background(), "tkt-once", "a"); got != nil {
		t.Fatalf("second validate should be nil (single-use), got %v", got.ID())
	}
}

// TestValidatePTYTicket_AgentMismatchConsumesAndRejects verifies that
// presenting a ticket against a DIFFERENT agent than it was minted
// for fails AND still consumes the ticket. The consume-on-mismatch
// behavior prevents an attacker who steals a single ticket from
// trying it against every agent they can enumerate.
func TestValidatePTYTicket_AgentMismatchConsumesAndRejects(t *testing.T) {
	s := &Server{}
	s.ptyTicketsMu.Lock()
	s.ptyTickets = map[string]ptyTicketEntry{
		"tkt-bound": {identity: &stubIdentity{id: "u"}, agentID: "agent-alpha", expiresAt: time.Now().Add(time.Minute)},
	}
	s.ptyTicketsMu.Unlock()

	if got := s.validatePTYTicketForAgent(context.Background(), "tkt-bound", "agent-beta"); got != nil {
		t.Fatalf("agent mismatch should reject, got identity %v", got.ID())
	}
	// Ticket should be gone — even the correct agent can't redeem it now.
	if got := s.validatePTYTicketForAgent(context.Background(), "tkt-bound", "agent-alpha"); got != nil {
		t.Fatal("ticket should be consumed after mismatch")
	}
}

// TestValidatePTYTicket_Expired verifies that an expired ticket is
// rejected even when present in the map, AND is still consumed so
// the GC sweep doesn't have to chase it.
func TestValidatePTYTicket_Expired(t *testing.T) {
	s := &Server{}
	s.ptyTicketsMu.Lock()
	s.ptyTickets = map[string]ptyTicketEntry{
		"tkt-stale": {identity: &stubIdentity{id: "u"}, agentID: "a", expiresAt: time.Now().Add(-time.Second)},
	}
	s.ptyTicketsMu.Unlock()

	if got := s.validatePTYTicketForAgent(context.Background(), "tkt-stale", "a"); got != nil {
		t.Fatalf("expired ticket should reject, got %v", got.ID())
	}
	// Consumed even though it was expired.
	s.ptyTicketsMu.Lock()
	_, present := s.ptyTickets["tkt-stale"]
	s.ptyTicketsMu.Unlock()
	assert.False(t, present, "expired ticket should be removed from the store on lookup")
}

// TestValidatePTYTicket_Unknown verifies that an unknown ticket
// returns nil and doesn't panic on a nil ptyTickets map (the zero
// value of Server has no ticket map).
func TestValidatePTYTicket_Unknown(t *testing.T) {
	s := &Server{}
	if got := s.validatePTYTicketForAgent(context.Background(), "tkt-nope", "a"); got != nil {
		t.Fatalf("unknown ticket should return nil, got %v", got.ID())
	}
	if got := s.validatePTYTicketForAgent(context.Background(), "", "a"); got != nil {
		t.Fatalf("empty ticket should return nil, got %v", got.ID())
	}
}
