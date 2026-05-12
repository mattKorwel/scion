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

package apiclient

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"os"
)

// HubTLS environment variables. Both are optional; absence yields nil
// (i.e. use Go's standard system trust store + verification).
const (
	// EnvHubCAFile points at a PEM-encoded CA certificate (or bundle)
	// that scion clients (CLI, broker, hub-to-broker) should trust as
	// an additional root when verifying the hub's TLS certificate.
	// Use this when the hub is fronted by a TLS terminator (caddy,
	// nginx) using a self-signed or internally-issued certificate.
	//
	// The file is loaded at process start. Rotating the cert requires
	// restarting the process (or a future SIGHUP-based reload).
	EnvHubCAFile = "SCION_HUB_CA_FILE"

	// EnvHubInsecureSkipVerify, when set to a truthy value (1/true/yes),
	// disables TLS certificate verification for hub-bound connections.
	// Intended ONLY for local development or initial bring-up; production
	// deployments should use SCION_HUB_CA_FILE with a real CA bundle.
	EnvHubInsecureSkipVerify = "SCION_HUB_INSECURE_SKIP_VERIFY"
)

var errHubCANoCerts = errors.New("no certificates found in CA file")

// HubTLSConfig returns a *tls.Config to use when dialing the hub.
//
// Behavior:
//   - If $SCION_HUB_CA_FILE is set: load the file, append to a fresh
//     pool, return a config with RootCAs set. Errors if the file is
//     unreadable or contains no PEM certificates.
//   - If $SCION_HUB_INSECURE_SKIP_VERIFY is truthy: return a config
//     with InsecureSkipVerify=true. Logs a warning is the caller's
//     responsibility — this function is silent.
//   - Otherwise: return (nil, nil) so callers fall through to Go's
//     defaults (system trust store, verification on).
//
// Both env vars together: the CA file wins (RootCAs set, verification
// remains on). InsecureSkipVerify is a fallback for when no CA is
// available, not an additive setting.
func HubTLSConfig() (*tls.Config, error) {
	caFile := os.Getenv(EnvHubCAFile)
	if caFile != "" {
		return loadHubCAConfig(caFile)
	}
	if isTruthy(os.Getenv(EnvHubInsecureSkipVerify)) {
		return &tls.Config{InsecureSkipVerify: true}, nil
	}
	return nil, nil
}

// loadHubCAConfig reads a PEM CA bundle and returns a *tls.Config that
// trusts those CAs in addition to nothing else (NOT additive to the
// system pool — explicit additional-trust would require x509.SystemCertPool).
// Caller-controlled trust scope is the point.
func loadHubCAConfig(caFile string) (*tls.Config, error) {
	pemBytes, err := os.ReadFile(caFile)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", caFile, err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(pemBytes) {
		return nil, fmt.Errorf("%s: %w", caFile, errHubCANoCerts)
	}
	return &tls.Config{RootCAs: roots}, nil
}

// isTruthy parses common truthy strings (case-insensitive). Empty
// returns false. Used for the SkipVerify env var.
func isTruthy(s string) bool {
	switch s {
	case "", "0", "false", "False", "FALSE", "no", "No", "NO":
		return false
	}
	return true
}
