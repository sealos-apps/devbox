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

func TestClientTagImage(t *testing.T) {
	const (
		username = "admin"
		password = "passw0rd"
		image    = "default/devbox-sample"
		oldTag   = "old-tag"
		newTag   = "new-tag"
	)

	var gotPutBody string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if user, pass, ok := r.BasicAuth(); !ok || user != username || pass != password {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}

		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v2/"+image+"/manifests/"+oldTag:
			w.Header().Set("Content-Type", "application/vnd.docker.distribution.manifest.v2+json")
			_, _ = w.Write([]byte(`{"schemaVersion":2}`))
		case r.Method == http.MethodPut && r.URL.Path == "/v2/"+image+"/manifests/"+newTag:
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

	client := &Client{
		Username: username,
		Password: password,
	}

	hostName := strings.TrimPrefix(server.URL, "http://")
	if err := client.TagImage(hostName, image, oldTag, newTag); err != nil {
		t.Fatalf("TagImage() error = %v, want nil", err)
	}

	if gotPutBody != `{"schemaVersion":2}` {
		t.Fatalf("TagImage() pushed manifest = %q, want %q", gotPutBody, `{"schemaVersion":2}`)
	}
}

func TestClientPullManifestNotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	client := &Client{}
	hostName := strings.TrimPrefix(server.URL, "http://")

	_, err := client.pullManifest("", "", hostName, "default/devbox-sample", "missing")
	if err != ErrorManifestNotFound {
		t.Fatalf("pullManifest() error = %v, want %v", err, ErrorManifestNotFound)
	}
}
