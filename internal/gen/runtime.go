package gen

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/maulai/tether/internal/msgs"
)

// runtimeTS is the TypeScript runtime as it lives in the repository: valid
// TypeScript with the built-in tables, so it can be tested as it is. The
// generator replaces the marked regions with the project's header and
// tables.
//
//go:embed runtime.ts
var runtimeTS string

// messageTable is the runtime's message table, in the order the server
// looks a text up: the built-in texts, then each Pattern's own message,
// then the project's Messages, which override both.
func messageTable(patterns map[string]pattern, overrides map[string]string) map[string]string {
	table := map[string]string{}
	for k, v := range msgs.Default {
		table[k] = v
	}
	for name, pt := range patterns {
		table[name] = pt.message
	}
	for k, v := range overrides {
		table[k] = v
	}
	return table
}

// renderRuntime returns runtime.ts for a project: the codes of its
// problems, its message table, its patterns translated to JavaScript, and
// the names of its server-only checks.
func renderRuntime(header string, messages map[string]string, patterns map[string]string, checks []string, codes []string) []byte {
	var t strings.Builder
	if len(codes) == 0 {
		t.WriteString("export type ProblemCode = never;\n")
	} else {
		quoted := make([]string, len(codes))
		for i, c := range codes {
			quoted[i] = tsString(c)
		}
		fmt.Fprintf(&t, "export type ProblemCode = %s;\n", strings.Join(quoted, " | "))
	}
	t.WriteString("const messages: Record<string, string> = {\n")
	for _, k := range sortedKeys(messages) {
		fmt.Fprintf(&t, "  %s: %s,\n", tsKey(k), tsString(messages[k]))
	}
	t.WriteString("};\n")
	if len(patterns) == 0 {
		t.WriteString("const patterns: Record<string, RegExp> = {};\n")
	} else {
		t.WriteString("const patterns: Record<string, RegExp> = {\n")
		for _, k := range sortedKeys(patterns) {
			fmt.Fprintf(&t, "  %s: /^(?:%s)$/u,\n", tsKey(k), patterns[k])
		}
		t.WriteString("};\n")
	}
	sorted := append([]string(nil), checks...)
	sort.Strings(sorted)
	quoted := make([]string, len(sorted))
	for i, c := range sorted {
		quoted[i] = tsString(c)
	}
	fmt.Fprintf(&t, "const serverOnly: ReadonlySet<string> = new Set<string>([%s]);\n", strings.Join(quoted, ", "))

	out := replaceRegion(runtimeTS, "header", header+"\n")
	out = replaceRegion(out, "tables", t.String())
	return []byte(out)
}

// replaceRegion swaps the lines between "// tether:<name>" and the next
// "// tether:end", markers included.
func replaceRegion(src, name, with string) string {
	begin := "// tether:" + name + "\n"
	i := strings.Index(src, begin)
	if i < 0 {
		panic("runtime.ts: missing region " + name)
	}
	j := strings.Index(src[i:], "// tether:end\n")
	if j < 0 {
		panic("runtime.ts: unterminated region " + name)
	}
	return src[:i] + with + src[i+j+len("// tether:end\n"):]
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// tsString quotes s as a TypeScript string literal. JSON's quoting is valid
// JavaScript, and encoding/json escapes U+2028 and U+2029, which old
// engines do not accept raw.
func tsString(s string) string {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	enc.Encode(s)
	return strings.TrimSuffix(b.String(), "\n")
}

// tsKey writes an object key, quoted only when it is not an identifier.
func tsKey(s string) string {
	if isIdent(s) {
		return s
	}
	return tsString(s)
}

func isIdent(s string) bool {
	if s == "" {
		return false
	}
	for i, c := range s {
		ok := c == '_' || c == '$' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || i > 0 && c >= '0' && c <= '9'
		if !ok {
			return false
		}
	}
	return true
}
