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
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCloudCode_Identity(t *testing.T) {
	c := newCloudCode()
	assert.Equal(t, "cloudcode", c.Name())
	assert.Equal(t, "cloudcode", c.GetEmbedDir())

	// The embedded config.yaml must be exposed for seeding / reset.
	fsys, base := c.GetHarnessEmbedsFS()
	data, err := fsys.ReadFile(base + "/config.yaml")
	assert.NoError(t, err)
	assert.NotEmpty(t, data)
}

// TestCloudCode_CommandIsInteractiveTUI is the regression guard for the bug
// where the cloudcode harness ran the one-shot `cloudcode run` and exited
// immediately when given no task, taking the container down with it. The
// command must launch the bare interactive TUI and deliver any task via
// --prompt (which keeps the session interactive), mirroring gemini.
func TestCloudCode_CommandIsInteractiveTUI(t *testing.T) {
	c := newCloudCode()

	// No task: bare interactive TUI — crucially NOT "cloudcode run".
	assert.Equal(t, []string{"cloudcode"}, c.GetCommand("", false, nil))

	// With a task: seeded via --prompt, still interactive.
	assert.Equal(t, []string{"cloudcode", "--prompt", "investigate X"},
		c.GetCommand("investigate X", false, nil))

	// Resume continues the last session.
	assert.Equal(t, []string{"cloudcode", "--continue"}, c.GetCommand("", true, nil))
}

// TestCloudCode_CapabilitiesFromEmbed confirms the embedded capability matrix
// is parsed and reported (instead of falling through to the Generic default),
// which is what the Hub capability path relies on.
func TestCloudCode_CapabilitiesFromEmbed(t *testing.T) {
	c := newCloudCode()
	caps := c.AdvancedCapabilities()
	assert.Equal(t, "cloudcode", caps.Harness)
}
