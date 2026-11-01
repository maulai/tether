package gen

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/maulai/tether/internal/msgs"
)

// TestRuntimeConformance runs the shared vectors against the TypeScript
// runtime the generator writes, with Node.js. Without Node the test is
// skipped, unless TETHER_REQUIRE_NODE is set, as it is in CI.
func TestRuntimeConformance(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		if os.Getenv("TETHER_REQUIRE_NODE") != "" {
			t.Fatal("node not found")
		}
		t.Skip("node not found")
	}
	vectorsPath, _ := filepath.Abs("../../testdata/conformance/rules.json")
	data, err := os.ReadFile(vectorsPath)
	if err != nil {
		t.Fatal(err)
	}
	var v struct {
		Patterns map[string]struct{ JS string } `json:"patterns"`
		Checks   []string                       `json:"checks"`
	}
	if err := json.Unmarshal(data, &v); err != nil {
		t.Fatal(err)
	}
	patterns := map[string]string{}
	for name, p := range v.Patterns {
		patterns[name] = p.JS
	}

	dir := t.TempDir()
	runner, err := os.ReadFile("testdata/conformance.ts")
	if err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(dir, "conformance.ts"), runner)
	write(t, filepath.Join(dir, "runtime.ts"), renderRuntime("// test", msgs.Default, patterns, v.Checks))

	out, err := exec.Command(node, filepath.Join(dir, "conformance.ts"), vectorsPath).CombinedOutput()
	t.Logf("%s", out)
	if err != nil {
		t.Fatal(err)
	}
}

func write(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}
