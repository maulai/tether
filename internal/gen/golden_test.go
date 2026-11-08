package gen

import (
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "rewrite the golden files")

func root(t *testing.T) string {
	t.Helper()
	dir, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

// golden compares what Generate computes with the files on disk, or
// rewrites them with -update.
func golden(t *testing.T, o Options) {
	t.Helper()
	files, err := Generate(o)
	if err != nil {
		t.Fatal(err)
	}
	if *update {
		if err := Write(files); err != nil {
			t.Fatal(err)
		}
		return
	}
	for _, s := range Stale(files) {
		t.Errorf("%s differs from the generator's output; run go test ./internal/gen -update and review the diff", s)
	}
}

func TestGoldenFeatures(t *testing.T) {
	golden(t, Options{
		Dir:      root(t),
		Patterns: []string{"./internal/gen/testdata/features", "./internal/gen/testdata/features/catalog"},
		Out:      "internal/gen/testdata/features/want",
		OpenAPI:  "internal/gen/testdata/features/want/openapi.json",
		Title:    "Features",
		Version:  "1.0.0",
		Tool:     "v9.9.9",
	})
}

// TestGoldenQualify runs over packages that share type names, with -qualify.
func TestGoldenQualify(t *testing.T) {
	golden(t, Options{
		Dir:      root(t),
		Patterns: []string{"./internal/gen/testdata/qualify/..."},
		Out:      "internal/gen/testdata/qualify/want",
		OpenAPI:  "internal/gen/testdata/qualify/want/openapi.json",
		Title:    "Qualify",
		Version:  "1.0.0",
		Tool:     "v9.9.9",
		Qualify:  true,
	})
}

// TestQualifyNeeded checks that a collision between types of two packages
// points to -qualify, and that -qualify cannot separate two packages of the
// same name.
func TestQualifyNeeded(t *testing.T) {
	dir := root(t)
	_, err := Generate(Options{Dir: dir, Patterns: []string{"./internal/gen/testdata/qualify/..."}, Out: "unused"})
	if err == nil || !strings.Contains(err.Error(), `both become "Status" in TypeScript; rename one, or run tether with -qualify`) {
		t.Errorf("without -qualify: %v", err)
	}
	_, err = Generate(Options{Dir: dir, Patterns: []string{"./internal/gen/testdata/clash"}, Out: "unused", Qualify: true})
	if err == nil || !strings.Contains(err.Error(), `both become "TaxRate" in TypeScript; rename one`) || strings.Contains(err.Error(), "-qualify") {
		t.Errorf("two packages named tax: %v", err)
	}
}

// TestGoldenExample keeps the example's generated files in step with the
// generator, with the options of its go:generate line.
func TestGoldenExample(t *testing.T) {
	golden(t, Options{
		Dir:     filepath.Join(root(t), "example", "tasks"),
		Out:     "web/src/api",
		Title:   "Tasks",
		Version: "1.0.0",
	})
}

// TestRemovesOldRegistrations checks that a tether_gen.go which tether
// wrote into an enum package in an earlier version is removed, and that it
// does not break reading the program even when it names constants that no
// longer exist.
func TestRemovesOldRegistrations(t *testing.T) {
	dir := root(t)
	stale := filepath.Join(dir, "internal/gen/testdata/features/catalog", genFile)
	old := genPrefix + "; DO NOT EDIT.\n\npackage catalog\n\nimport \"github.com/maulai/tether\"\n\nfunc init() { tether.RegisterEnum(ColorGone) }\n"
	if err := os.WriteFile(stale, []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	defer os.Remove(stale)
	files, err := Generate(Options{
		Dir:      dir,
		Patterns: []string{"./internal/gen/testdata/features", "./internal/gen/testdata/features/catalog"},
		Out:      "internal/gen/testdata/features/want",
		OpenAPI:  "internal/gen/testdata/features/want/openapi.json",
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if f.Path == stale && f.Data == nil {
			return
		}
	}
	t.Errorf("%s is not marked for removal", stale)
}

func TestMistakes(t *testing.T) {
	dir := root(t)
	_, err := Generate(Options{Dir: dir, Patterns: []string{"./internal/gen/testdata/mistakes/..."}, Out: "unused"})
	if err == nil {
		t.Fatal("no error")
	}
	got := strings.ReplaceAll(err.Error(), dir+string(filepath.Separator), "") + "\n"
	want := filepath.Join("testdata", "mistakes", "want", "errors.txt")
	if *update {
		os.MkdirAll(filepath.Dir(want), 0o755)
		os.WriteFile(want, []byte(got), 0o644)
		return
	}
	data, _ := os.ReadFile(want)
	if got != string(data) {
		t.Errorf("errors differ; run go test ./internal/gen -update and review the diff\ngot:\n%s", got)
	}
}

// TestTypeScriptCompiles type-checks the generated TypeScript with the
// strictest compiler settings, so projects with any configuration accept
// it. It needs tsc; CI provides it, locally the test is skipped without it.
func TestTypeScriptCompiles(t *testing.T) {
	tsc := os.Getenv("TETHER_TSC")
	if tsc == "" {
		var err error
		if tsc, err = exec.LookPath("tsc"); err != nil {
			t.Skip("tsc not found; set TETHER_TSC")
		}
	}
	dir := root(t)
	files := []string{
		filepath.Join(dir, "internal/gen/testdata/features/want/index.ts"),
		filepath.Join(dir, "internal/gen/testdata/qualify/want/index.ts"),
		filepath.Join(dir, "example/tasks/web/src/api/index.ts"),
	}
	config := `{
  "compilerOptions": {
    "target": "ES2022", "module": "ESNext", "moduleResolution": "Bundler", "lib": ["ES2022", "DOM"],
    "strict": true, "noUncheckedIndexedAccess": true, "exactOptionalPropertyTypes": true,
    "noImplicitOverride": true, "noUnusedLocals": true, "noUnusedParameters": true, "noImplicitReturns": true,
    "noFallthroughCasesInSwitch": true, "verbatimModuleSyntax": true, "erasableSyntaxOnly": true,
    "isolatedModules": true, "noEmit": true, "skipLibCheck": true
  },
  "files": ["` + strings.Join(files, `", "`) + `"]
}`
	cfg := filepath.Join(t.TempDir(), "tsconfig.json")
	if err := os.WriteFile(cfg, []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(tsc, "-p", cfg).CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
}
