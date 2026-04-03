// Copyright © 2024 sealos.
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

package registry

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRegistryReTag(t *testing.T) {
	const (
		username = "admin"
		password = "passw0rd"
		source   = "example.test/default/devbox-sample:old-tag"
		target   = "example.test/default/devbox-sample:new-tag"
	)

	var gotPutBody string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if user, pass, ok := r.BasicAuth(); !ok || user != username || pass != password {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}

		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v2/default/devbox-sample/manifests/old-tag":
			w.Header().Set("Content-Type", "application/vnd.docker.distribution.manifest.v2+json")
			_, _ = w.Write([]byte(`{"schemaVersion":2}`))
		case r.Method == http.MethodPut && r.URL.Path == "/v2/default/devbox-sample/manifests/new-tag":
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Fatalf("read request body: %v", err)
			}
			gotPutBody = string(body)
			w.WriteHeader(http.StatusCreated)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	registry := &Registry{
		BasicAuth: BasicAuth{
			Username: username,
			Password: password,
		},
	}

	sourceImage := strings.Replace(source, "example.test", strings.TrimPrefix(server.URL, "http://"), 1)
	targetImage := strings.Replace(target, "example.test", strings.TrimPrefix(server.URL, "http://"), 1)

	if err := registry.ReTag(sourceImage, targetImage); err != nil {
		t.Fatalf("ReTag() error = %v, want nil", err)
	}

	if gotPutBody != `{"schemaVersion":2}` {
		t.Fatalf("ReTag() pushed manifest = %q, want %q", gotPutBody, `{"schemaVersion":2}`)
	}
}

func TestRegistryPullManifestNotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	registry := &Registry{}
	image := strings.TrimPrefix(server.URL, "http://") + "/default/devbox-sample:missing"

	_, err := registry.pullManifest(image)
	if err != ErrManifestNotFound {
		t.Fatalf("pullManifest() error = %v, want %v", err, ErrManifestNotFound)
	}
}
