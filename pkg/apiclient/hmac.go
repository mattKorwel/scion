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

// Package apiclient provides shared HTTP client utilities for Scion API clients.
package apiclient

import (
	"bytes"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// HMAC authentication headers (must match pkg/hub/hostauth.go).
const (
	HeaderBrokerID      = "X-Scion-Broker-ID"
	HeaderTimestamp     = "X-Scion-Timestamp"
	HeaderNonce         = "X-Scion-Nonce"
	HeaderSignature     = "X-Scion-Signature"
	HeaderSignedHeaders = "X-Scion-Signed-Headers"
)

// HMACAuth implements HMAC-based authentication for Runtime Brokers.
// This authenticator signs requests using the same algorithm as pkg/hub/hostauth.go.
type HMACAuth struct {
	BrokerID  string
	SecretKey []byte
	// PathPrefix is stripped from the request URL path before HMAC
	// signing. Set this when the broker reaches the hub through a
	// reverse-proxy mount at a non-root URL (e.g.
	// SCION_HUB_ENDPOINT=http://localhost:18181/scion-hub): the request
	// URL carries "/scion-hub/api/v1/..." but the hub's signature
	// verification sees "/api/v1/..." after the mount-proxy strips the
	// prefix. Leave empty for the common case (broker speaks directly
	// to the hub).
	PathPrefix string
}

// ApplyAuth adds HMAC authentication headers to the request.
func (a *HMACAuth) ApplyAuth(req *http.Request) error {
	if a.BrokerID == "" || len(a.SecretKey) == 0 {
		return fmt.Errorf("HMAC auth requires brokerID and secretKey")
	}

	// 1. Generate timestamp (Unix epoch seconds as string)
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)

	// 2. Generate nonce (16 bytes, URL-safe base64)
	nonceBytes := make([]byte, 16)
	if _, err := rand.Read(nonceBytes); err != nil {
		return fmt.Errorf("failed to generate nonce: %w", err)
	}
	nonce := base64.URLEncoding.EncodeToString(nonceBytes)

	// 3. Set headers before building canonical string
	req.Header.Set(HeaderBrokerID, a.BrokerID)
	req.Header.Set(HeaderTimestamp, timestamp)
	req.Header.Set(HeaderNonce, nonce)

	// 4. Build canonical string (must match hub/hostauth.go exactly).
	// Sign the request as the hub will see it: with the reverse-proxy
	// mount prefix stripped (if any).
	canonical := BuildCanonicalStringWithPrefix(req, timestamp, nonce, a.PathPrefix)

	// 5. Compute HMAC-SHA256
	sig := ComputeHMAC(a.SecretKey, canonical)
	req.Header.Set(HeaderSignature, base64.StdEncoding.EncodeToString(sig))

	return nil
}

// Refresh indicates that refresh is not supported for HMAC auth.
func (a *HMACAuth) Refresh() (bool, error) { return false, nil }

// BuildCanonicalString builds the canonical string for HMAC signing.
// Format: METHOD\nPATH\nQUERY\nTIMESTAMP\nNONCE\nSIGNED_HEADERS\nBODY_HASH
// This function must produce identical output to hub/hostauth.go:buildCanonicalString.
func BuildCanonicalString(r *http.Request, timestamp, nonce string) []byte {
	return BuildCanonicalStringWithPrefix(r, timestamp, nonce, "")
}

// BuildCanonicalStringWithPrefix is like BuildCanonicalString but strips
// pathPrefix from r.URL.Path before including it in the canonical
// string. Used when the broker reaches the hub through a reverse-proxy
// mount so the signed path matches what the hub sees after the proxy
// strips the mount prefix.
func BuildCanonicalStringWithPrefix(r *http.Request, timestamp, nonce, pathPrefix string) []byte {
	var buf bytes.Buffer

	// HTTP method
	buf.WriteString(r.Method)
	buf.WriteByte('\n')

	// Request path (with optional mount prefix stripped)
	path := r.URL.Path
	if pathPrefix != "" && strings.HasPrefix(path, pathPrefix) {
		path = strings.TrimPrefix(path, pathPrefix)
		if path == "" {
			path = "/"
		}
	}
	buf.WriteString(path)
	buf.WriteByte('\n')

	// Query string (raw, unsorted - matches hub behavior)
	buf.WriteString(r.URL.RawQuery)
	buf.WriteByte('\n')

	// Timestamp
	buf.WriteString(timestamp)
	buf.WriteByte('\n')

	// Nonce
	buf.WriteString(nonce)
	buf.WriteByte('\n')

	// Signed headers (if specified)
	signedHeaders := r.Header.Get(HeaderSignedHeaders)
	if signedHeaders != "" {
		// Headers are listed as semicolon-separated names
		headerNames := strings.Split(signedHeaders, ";")
		for _, name := range headerNames {
			name = strings.TrimSpace(name)
			value := r.Header.Get(name)
			buf.WriteString(strings.ToLower(name))
			buf.WriteByte(':')
			buf.WriteString(strings.TrimSpace(value))
			buf.WriteByte('\n')
		}
	}

	// Body hash (SHA-256 of request body)
	if r.Body != nil && r.ContentLength > 0 {
		// We need to read and restore the body
		bodyBytes, err := io.ReadAll(r.Body)
		if err == nil {
			r.Body = io.NopCloser(bytes.NewReader(bodyBytes))
			bodyHash := sha256.Sum256(bodyBytes)
			buf.WriteString(base64.StdEncoding.EncodeToString(bodyHash[:]))
		}
	}

	return buf.Bytes()
}

// ComputeHMAC computes HMAC-SHA256.
func ComputeHMAC(secret, data []byte) []byte {
	h := hmac.New(sha256.New, secret)
	h.Write(data)
	return h.Sum(nil)
}

// VerifyHMAC verifies an HMAC-SHA256 signature.
func VerifyHMAC(secret, data, signature []byte) bool {
	expected := ComputeHMAC(secret, data)
	return hmac.Equal(expected, signature)
}
