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

package harness

import (
	"embed"

	"github.com/GoogleCloudPlatform/scion/pkg/config"
	cloudcodeembeds "github.com/GoogleCloudPlatform/scion/pkg/harness/cloudcode"
	"gopkg.in/yaml.v3"
)

// CloudCode is the built-in harness for Google's CloudCode CLI agent.
//
// Unlike the hand-written built-ins (gemini/claude/codex/opencode), CloudCode
// is fully declarative: all of its behavior — command construction, auth,
// capabilities — is expressed in the embedded embeds/config.yaml and driven by
// the shared DeclarativeGenericHarness. This struct exists only to:
//
//  1. give the cloudcode config.yaml a version-controlled, ships-with-the-binary
//     home so `scion init`/`server` seed it and `harness-config reset cloudcode`
//     can restore it, and
//  2. report cloudcode's real capability matrix (via the parsed entry) on the
//     Hub capability path instead of falling through to Generic.
//
// At dispatch time harness.Resolve still reads the on-disk config.yaml directly,
// so New() and Resolve() produce identical behavior sourced from the one YAML.
type CloudCode struct {
	*DeclarativeGenericHarness
}

// newCloudCode parses the embedded config.yaml and wraps it in the declarative
// harness. If the embed fails to parse (should never happen for a checked-in
// file), it falls back to a minimal entry so the harness still has a valid name.
func newCloudCode() *CloudCode {
	var entry config.HarnessConfigEntry
	if data, err := cloudcodeembeds.EmbedsFS.ReadFile("embeds/config.yaml"); err == nil {
		_ = yaml.Unmarshal(data, &entry)
	}
	if entry.Harness == "" {
		entry.Harness = "cloudcode"
	}
	return &CloudCode{DeclarativeGenericHarness: NewDeclarativeGenericHarness(entry)}
}

// Name overrides the embedded delegate so the harness is always identified as
// "cloudcode" even if the embedded YAML is somehow missing its harness field.
func (c *CloudCode) Name() string { return "cloudcode" }

// GetEmbedDir returns the embeds subdirectory name used for template seeding.
func (c *CloudCode) GetEmbedDir() string { return "cloudcode" }

// GetHarnessEmbedsFS exposes the embedded default harness-config files so that
// config.SeedHarnessConfig (init/server seeding, harness-config reset) can write
// them to ~/.scion/harness-configs/cloudcode/.
func (c *CloudCode) GetHarnessEmbedsFS() (embed.FS, string) {
	return cloudcodeembeds.EmbedsFS, "embeds"
}
