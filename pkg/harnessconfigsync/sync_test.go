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

package harnessconfigsync

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/GoogleCloudPlatform/scion/pkg/hubclient"
	"github.com/GoogleCloudPlatform/scion/pkg/transfer"
)

// --- stubs ------------------------------------------------------------

type stubService struct {
	listFunc     func(ctx context.Context, opts *hubclient.ListHarnessConfigsOptions) (*hubclient.ListHarnessConfigsResponse, error)
	getFunc      func(ctx context.Context, id string) (*hubclient.HarnessConfig, error)
	urlsFunc     func(ctx context.Context, id string) (*hubclient.DownloadResponse, error)
	dlFunc       func(ctx context.Context, url string) ([]byte, error)
	readFileFunc func(ctx context.Context, id, path string) ([]byte, error)
}

func (s *stubService) List(ctx context.Context, opts *hubclient.ListHarnessConfigsOptions) (*hubclient.ListHarnessConfigsResponse, error) {
	if s.listFunc != nil {
		return s.listFunc(ctx, opts)
	}
	return &hubclient.ListHarnessConfigsResponse{}, nil
}
func (s *stubService) Get(ctx context.Context, id string) (*hubclient.HarnessConfig, error) {
	if s.getFunc != nil {
		return s.getFunc(ctx, id)
	}
	return nil, nil
}
func (s *stubService) Create(ctx context.Context, req *hubclient.CreateHarnessConfigRequest) (*hubclient.CreateHarnessConfigResponse, error) {
	return nil, nil
}
func (s *stubService) Update(ctx context.Context, id string, req *hubclient.UpdateHarnessConfigRequest) (*hubclient.HarnessConfig, error) {
	return nil, nil
}
func (s *stubService) Delete(ctx context.Context, id string) error { return nil }
func (s *stubService) RequestUploadURLs(ctx context.Context, id string, files []hubclient.FileUploadRequest) (*hubclient.UploadResponse, error) {
	return nil, nil
}
func (s *stubService) Finalize(ctx context.Context, id string, manifest *hubclient.HarnessConfigManifest) (*hubclient.HarnessConfig, error) {
	return nil, nil
}
func (s *stubService) RequestDownloadURLs(ctx context.Context, id string) (*hubclient.DownloadResponse, error) {
	if s.urlsFunc != nil {
		return s.urlsFunc(ctx, id)
	}
	return &hubclient.DownloadResponse{}, nil
}
func (s *stubService) UploadFile(ctx context.Context, url string, method string, headers map[string]string, content io.Reader) error {
	return nil
}
func (s *stubService) UploadFilesMultipart(ctx context.Context, id string, files []hubclient.FileInfo) error {
	return nil
}
func (s *stubService) DownloadFile(ctx context.Context, url string) ([]byte, error) {
	if s.dlFunc != nil {
		return s.dlFunc(ctx, url)
	}
	return nil, errors.New("DownloadFile not stubbed")
}
func (s *stubService) ReadFile(ctx context.Context, id, path string) ([]byte, error) {
	if s.readFileFunc != nil {
		return s.readFileFunc(ctx, id, path)
	}
	return nil, errors.New("ReadFile not stubbed")
}

type stubHubClient struct {
	hubclient.Client
	svc *stubService
}

func (c *stubHubClient) HarnessConfigs() hubclient.HarnessConfigService { return c.svc }

// --- tests ------------------------------------------------------------

func TestEnsureLocal_NotOnHub(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	svc := &stubService{
		listFunc: func(ctx context.Context, opts *hubclient.ListHarnessConfigsOptions) (*hubclient.ListHarnessConfigsResponse, error) {
			return &hubclient.ListHarnessConfigsResponse{HarnessConfigs: nil}, nil
		},
	}
	_, err := EnsureLocal(context.Background(), &stubHubClient{svc: svc}, "no-such-thing")
	if !errors.Is(err, ErrNotOnHub) {
		t.Fatalf("expected ErrNotOnHub, got: %v", err)
	}
}

func TestEnsureLocal_DownloadsAndInstalls(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	body := []byte("harness: shell-oneshot\nimage: scion-base:latest\n")
	hash := transfer.HashBytes(body)

	svc := &stubService{
		listFunc: func(ctx context.Context, opts *hubclient.ListHarnessConfigsOptions) (*hubclient.ListHarnessConfigsResponse, error) {
			return &hubclient.ListHarnessConfigsResponse{
				HarnessConfigs: []hubclient.HarnessConfig{
					{ID: "hc-1", Name: "shell-oneshot", Status: "active"},
				},
			}, nil
		},
		urlsFunc: func(ctx context.Context, id string) (*hubclient.DownloadResponse, error) {
			return &hubclient.DownloadResponse{
				Files: []hubclient.DownloadURLInfo{
					{Path: "config.yaml", URL: "https://signed.example/config.yaml", Hash: hash},
				},
				Expires: time.Now().Add(time.Hour),
			}, nil
		},
		dlFunc: func(ctx context.Context, url string) ([]byte, error) {
			return body, nil
		},
	}
	dest, err := EnsureLocal(context.Background(), &stubHubClient{svc: svc}, "shell-oneshot")
	if err != nil {
		t.Fatalf("EnsureLocal: %v", err)
	}

	want := filepath.Join(home, ".scion", "harness-configs", "shell-oneshot")
	if dest != want {
		t.Fatalf("destination = %q, want %q", dest, want)
	}
	got, err := os.ReadFile(filepath.Join(dest, "config.yaml"))
	if err != nil {
		t.Fatalf("read installed file: %v", err)
	}
	if string(got) != string(body) {
		t.Fatalf("installed content = %q, want %q", got, body)
	}
}

func TestEnsureLocal_HashMismatchAborts(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	body := []byte("config: real\n")
	wrongHash := "sha256:not-the-real-hash"

	svc := &stubService{
		listFunc: func(ctx context.Context, opts *hubclient.ListHarnessConfigsOptions) (*hubclient.ListHarnessConfigsResponse, error) {
			return &hubclient.ListHarnessConfigsResponse{
				HarnessConfigs: []hubclient.HarnessConfig{
					{ID: "hc-2", Name: "bad-hash", Status: "active"},
				},
			}, nil
		},
		urlsFunc: func(ctx context.Context, id string) (*hubclient.DownloadResponse, error) {
			return &hubclient.DownloadResponse{
				Files: []hubclient.DownloadURLInfo{
					{Path: "config.yaml", URL: "https://signed.example/config.yaml", Hash: wrongHash},
				},
			}, nil
		},
		dlFunc: func(ctx context.Context, url string) ([]byte, error) {
			return body, nil
		},
	}
	_, err := EnsureLocal(context.Background(), &stubHubClient{svc: svc}, "bad-hash")
	if err == nil {
		t.Fatal("expected hash-mismatch error, got nil")
	}

	dest := filepath.Join(home, ".scion", "harness-configs", "bad-hash")
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Fatalf("expected dest to NOT exist after hash mismatch, stat err = %v", err)
	}
}
