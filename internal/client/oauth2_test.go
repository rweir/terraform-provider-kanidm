package client

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func testClient(handler func(*http.Request) string) *Client {
	return NewClient("https://kanidm.example.test", "token", WithHTTPClient(&http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(bytes.NewBufferString(handler(req))),
				Request:    req,
			}, nil
		}),
	}))
}

func TestGetOAuth2ClientParsesSupplementalScopeMaps(t *testing.T) {
	client := testClient(func(r *http.Request) string {
		if r.Method != http.MethodGet {
			t.Fatalf("method = %s, want GET", r.Method)
		}
		if r.URL.Path != "/v1/oauth2/forgejo" {
			t.Fatalf("path = %s, want /v1/oauth2/forgejo", r.URL.Path)
		}

		return `{
			"attrs": {
				"class": ["oauth2_resource_server", "oauth2_resource_server_basic"],
				"name": ["forgejo"],
				"displayname": ["Forgejo"],
				"oauth2_rs_origin_landing": ["https://dev.example.test/"],
				"oauth2_rs_origin": ["https://dev.example.test/user/oauth2/kanidm/callback"],
				"oauth2_rs_scope_map": ["forgejo_users@example.test: {\"openid\", \"email\", \"profile\", \"groups\"}"],
				"oauth2_rs_sup_scope_map": ["forgejo_users@example.test: {\"ssh_publickeys\"}"]
			}
		}`
	})

	oauth2Client, err := client.GetOAuth2Client(context.Background(), "forgejo")
	if err != nil {
		t.Fatalf("GetOAuth2Client returned error: %v", err)
	}

	want := map[string][]string{"forgejo_users": {"ssh_publickeys"}}
	if !reflect.DeepEqual(oauth2Client.SupScopeMaps, want) {
		t.Fatalf("SupScopeMaps = %#v, want %#v", oauth2Client.SupScopeMaps, want)
	}
}

func TestSetOAuth2SupplementalScopeMapUsesSupScopeMapEndpoint(t *testing.T) {
	var sawRequest bool
	client := testClient(func(r *http.Request) string {
		sawRequest = true
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s, want POST", r.Method)
		}
		if r.URL.Path != "/v1/oauth2/forgejo/_sup_scopemap/forgejo_users" {
			t.Fatalf("path = %s, want supplemental scope-map endpoint", r.URL.Path)
		}

		var got []string
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		want := []string{"ssh_publickeys"}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("body = %#v, want %#v", got, want)
		}
		return `{}`
	})

	if err := client.SetOAuth2SupplementalScopeMap(context.Background(), "forgejo", "forgejo_users", []string{"ssh_publickeys"}); err != nil {
		t.Fatalf("SetOAuth2SupplementalScopeMap returned error: %v", err)
	}
	if !sawRequest {
		t.Fatal("server did not receive request")
	}
}

func TestDeleteOAuth2SupplementalScopeMapUsesSupScopeMapEndpoint(t *testing.T) {
	var sawRequest bool
	client := testClient(func(r *http.Request) string {
		sawRequest = true
		if r.Method != http.MethodDelete {
			t.Fatalf("method = %s, want DELETE", r.Method)
		}
		if r.URL.Path != "/v1/oauth2/forgejo/_sup_scopemap/forgejo_users" {
			t.Fatalf("path = %s, want supplemental scope-map endpoint", r.URL.Path)
		}
		return `{}`
	})

	if err := client.DeleteOAuth2SupplementalScopeMap(context.Background(), "forgejo", "forgejo_users"); err != nil {
		t.Fatalf("DeleteOAuth2SupplementalScopeMap returned error: %v", err)
	}
	if !sawRequest {
		t.Fatal("server did not receive request")
	}
}
