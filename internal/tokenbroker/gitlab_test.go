package tokenbroker

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestCreateProjectTokenSendsExpectedRequestAndParsesResponse(t *testing.T) {
	var sawRequest bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawRequest = true

		if r.Method != http.MethodPost {
			t.Errorf("method = %q, want %q", r.Method, http.MethodPost)
		}
		if r.URL.Path != "/api/v4/projects/123/access_tokens" {
			t.Errorf("path = %q, want %q", r.URL.Path, "/api/v4/projects/123/access_tokens")
		}
		if got := r.Header.Get("PRIVATE-TOKEN"); got != "control-pat" {
			t.Errorf("PRIVATE-TOKEN = %q, want %q", got, "control-pat")
		}
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}

		if got := r.Form.Get("name"); got != "agentctl-XL-123" {
			t.Errorf("name = %q, want %q", got, "agentctl-XL-123")
		}
		wantScopes := []string{"read_repository", "write_repository"}
		if got := r.Form["scopes[]"]; !reflect.DeepEqual(got, wantScopes) {
			t.Errorf("scopes[] = %v, want %v", got, wantScopes)
		}
		if got := r.Form.Get("access_level"); got != "30" {
			t.Errorf("access_level = %q, want %q", got, "30")
		}
		if got := r.Form.Get("expires_at"); got != "2026-06-12" {
			t.Errorf("expires_at = %q, want %q", got, "2026-06-12")
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":98765,"token":"glpat-created-token","name":"agentctl-XL-123"}`))
	}))
	defer server.Close()

	client := NewGitLabClient(server.URL, "control-pat", server.Client())
	expiresAt := time.Date(2026, 6, 12, 23, 59, 59, 0, time.FixedZone("ICT", 7*60*60))

	created, err := client.CreateProjectToken(context.Background(), "123", "agentctl-XL-123", expiresAt)
	if err != nil {
		t.Fatal(err)
	}
	if !sawRequest {
		t.Fatal("server did not receive create request")
	}
	if created.ID != "98765" {
		t.Fatalf("created token id = %q, want %q", created.ID, "98765")
	}
	if created.Token != "glpat-created-token" {
		t.Fatalf("created token value = %q, want %q", created.Token, "glpat-created-token")
	}
	if created.Name != "agentctl-XL-123" {
		t.Fatalf("created token name = %q, want %q", created.Name, "agentctl-XL-123")
	}
}

func TestCreateProjectTokenReturnsErrorForNon2xxWithoutTokenValue(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"id":1,"token":"glpat-secret-leak","name":"agentctl-XL-123"}`))
	}))
	defer server.Close()

	client := NewGitLabClient(server.URL, "control-pat", server.Client())

	_, err := client.CreateProjectToken(context.Background(), "123", "agentctl-XL-123", time.Now())
	if err == nil {
		t.Fatal("error = nil, want non-2xx error")
	}
	if strings.Contains(err.Error(), "glpat-secret-leak") {
		t.Fatalf("error includes token value: %v", err)
	}
}

func TestRevokeProjectTokenSendsExpectedRequest(t *testing.T) {
	var sawRequest bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawRequest = true

		if r.Method != http.MethodDelete {
			t.Errorf("method = %q, want %q", r.Method, http.MethodDelete)
		}
		if r.URL.Path != "/api/v4/projects/123/access_tokens/98765" {
			t.Errorf("path = %q, want %q", r.URL.Path, "/api/v4/projects/123/access_tokens/98765")
		}
		if got := r.Header.Get("PRIVATE-TOKEN"); got != "control-pat" {
			t.Errorf("PRIVATE-TOKEN = %q, want %q", got, "control-pat")
		}

		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	client := NewGitLabClient(server.URL, "control-pat", server.Client())

	err := client.RevokeProjectToken(context.Background(), "123", "98765")
	if err != nil {
		t.Fatal(err)
	}
	if !sawRequest {
		t.Fatal("server did not receive revoke request")
	}
}

func TestRevokeProjectTokenReturnsErrorForNotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	client := NewGitLabClient(server.URL, "control-pat", server.Client())

	err := client.RevokeProjectToken(context.Background(), "123", "98765")
	if err == nil {
		t.Fatal("error = nil, want non-2xx error")
	}
}
