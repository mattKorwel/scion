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
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestHubTLSConfig_NoEnv(t *testing.T) {
	t.Setenv(EnvHubCAFile, "")
	t.Setenv(EnvHubInsecureSkipVerify, "")
	cfg, err := HubTLSConfig()
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if cfg != nil {
		t.Errorf("expected nil tls.Config when no env set, got %+v", cfg)
	}
}

func TestHubTLSConfig_InsecureSkipVerify(t *testing.T) {
	t.Setenv(EnvHubCAFile, "")
	t.Setenv(EnvHubInsecureSkipVerify, "true")
	cfg, err := HubTLSConfig()
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if cfg == nil || !cfg.InsecureSkipVerify {
		t.Errorf("expected InsecureSkipVerify=true, got %+v", cfg)
	}
}

func TestHubTLSConfig_CAFileWins(t *testing.T) {
	dir := t.TempDir()
	caFile := filepath.Join(dir, "ca.pem")
	writeTestCA(t, caFile)

	t.Setenv(EnvHubCAFile, caFile)
	t.Setenv(EnvHubInsecureSkipVerify, "true") // should be ignored when CA set

	cfg, err := HubTLSConfig()
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if cfg == nil {
		t.Fatal("expected non-nil config")
	}
	if cfg.RootCAs == nil {
		t.Error("expected RootCAs to be set")
	}
	if cfg.InsecureSkipVerify {
		t.Error("expected InsecureSkipVerify=false when CA file is set (CA wins)")
	}
}

func TestHubTLSConfig_CAFileMissing(t *testing.T) {
	t.Setenv(EnvHubCAFile, "/nonexistent/path/to/ca.pem")
	t.Setenv(EnvHubInsecureSkipVerify, "")
	_, err := HubTLSConfig()
	if err == nil {
		t.Error("expected error for missing CA file")
	}
}

func TestHubTLSConfig_CAFileEmpty(t *testing.T) {
	dir := t.TempDir()
	caFile := filepath.Join(dir, "empty.pem")
	if err := os.WriteFile(caFile, []byte("not a valid pem"), 0644); err != nil {
		t.Fatal(err)
	}
	t.Setenv(EnvHubCAFile, caFile)
	t.Setenv(EnvHubInsecureSkipVerify, "")
	_, err := HubTLSConfig()
	if err == nil {
		t.Error("expected error for invalid CA file content")
	}
}

func TestIsTruthy(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"", false},
		{"0", false},
		{"false", false},
		{"FALSE", false},
		{"no", false},
		{"NO", false},
		{"1", true},
		{"true", true},
		{"True", true},
		{"yes", true},
		{"anything-else", true}, // lenient
	}
	for _, tc := range cases {
		if got := isTruthy(tc.in); got != tc.want {
			t.Errorf("isTruthy(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

// writeTestCA generates a minimal self-signed CA cert and writes it
// PEM-encoded to path. Used in tests.
func writeTestCA(t *testing.T, path string) {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("genkey: %v", err)
	}
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "scion-test-ca"},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &priv.PublicKey, priv)
	if err != nil {
		t.Fatalf("create cert: %v", err)
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	if err := os.WriteFile(path, pemBytes, 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
}
