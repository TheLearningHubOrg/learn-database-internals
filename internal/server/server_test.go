package server

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/TheLearningHubOrg/learn-database-internals/internal/kv"
)

func newServer(t *testing.T) *httptest.Server {
	t.Helper()
	store, err := kv.Open(t.TempDir(), kv.Options{})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(New(store))
	t.Cleanup(func() { srv.Close(); store.Close() })
	return srv
}

func do(t *testing.T, method, url, body string) (int, string) {
	t.Helper()
	req, _ := http.NewRequest(method, url, strings.NewReader(body))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func TestHealth(t *testing.T) {
	srv := newServer(t)
	code, body := do(t, "GET", srv.URL+"/health", "")
	if code != 200 || !strings.Contains(body, `"ok"`) {
		t.Fatalf("%d %s", code, body)
	}
}

func TestCRUD(t *testing.T) {
	srv := newServer(t)
	if code, _ := do(t, "PUT", srv.URL+"/kv/city", "Pune"); code != http.StatusNoContent {
		t.Fatalf("PUT = %d", code)
	}
	if code, body := do(t, "GET", srv.URL+"/kv/city", ""); code != 200 || body != "Pune" {
		t.Fatalf("GET = %d %q", code, body)
	}
	do(t, "DELETE", srv.URL+"/kv/city", "")
	if code, _ := do(t, "GET", srv.URL+"/kv/city", ""); code != 404 {
		t.Fatalf("GET after DELETE = %d", code)
	}
	if code, _ := do(t, "POST", srv.URL+"/kv/city", "x"); code != http.StatusMethodNotAllowed {
		t.Fatalf("POST = %d, want 405", code)
	}
}

func TestScan(t *testing.T) {
	srv := newServer(t)
	for _, k := range []string{"b", "a", "c", "d"} {
		do(t, "PUT", srv.URL+"/kv/"+k, strings.ToUpper(k))
	}
	_, body := do(t, "GET", srv.URL+"/scan?start=b&end=d", "")
	var got []struct{ Key, Value string }
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Key != "b" || got[1].Value != "C" {
		t.Fatalf("scan = %s", body)
	}
}
