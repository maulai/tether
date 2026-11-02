package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestClient runs the generated TypeScript client against this server with
// Node.js. It shows the whole chain: Go handler, generated client, and the
// same validation on both sides.
func TestClient(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		if os.Getenv("TETHER_REQUIRE_NODE") != "" {
			t.Fatal("node not found")
		}
		t.Skip("node not found")
	}
	srv := httptest.NewServer(routes())
	defer srv.Close()

	dir := t.TempDir()
	copyFile := func(src, dst string, edit func(string) string) {
		data, err := os.ReadFile(src)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, dst), []byte(edit(string(data))), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	same := func(s string) string { return s }
	// Node resolves imports by file name, bundlers do not need the extension.
	copyFile("web/src/api/index.ts", "index.ts", func(s string) string {
		return strings.Replace(s, `from "./runtime"`, `from "./runtime.ts"`, 1)
	})
	copyFile("web/src/api/runtime.ts", "runtime.ts", same)
	copyFile("testdata/client.ts", "client.ts", same)

	out, err := exec.Command(node, filepath.Join(dir, "client.ts"), srv.URL).CombinedOutput()
	t.Logf("%s", out)
	if err != nil {
		t.Fatal(err)
	}
}

// TestRecoverPanics shows a panic answered like any other unexpected error,
// in the format the client reads.
func TestRecoverPanics(t *testing.T) {
	h := recoverPanics(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { panic("boom") }))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if rec.Code != 500 || rec.Header().Get("Content-Type") != "application/problem+json" || strings.Contains(rec.Body.String(), "boom") {
		t.Errorf("status %d, headers %v, body %s", rec.Code, rec.Header(), rec.Body)
	}
}
