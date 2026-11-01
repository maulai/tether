package tether

import (
	"fmt"
	"reflect"
	"regexp"
	"sync"

	"github.com/maulai/tether/internal/jsregex"
	"github.com/maulai/tether/internal/vtag"
)

// The built-in formats spell out their character classes instead of using
// \s: JavaScript's \s matches more than Go's, and both sides must agree.
var (
	emailRe = regexp.MustCompile(`^[^@\t\n\f\r ]+@[^@\t\n\f\r ]+\.[^@\t\n\f\r ]+$`)
	urlRe   = regexp.MustCompile(`^[Hh][Tt][Tt][Pp][Ss]?://[^\t\n\f\r /]+[^\t\n\f\r ]*$`)
)

// badText reports whether s contains a character the always-on text rule
// rejects: control characters other than tab, line feed and carriage
// return, bidirectional overrides and isolates, and the Unicode line and
// paragraph separators. Each of these has a concrete harm (NUL breaks
// PostgreSQL text columns, overrides disguise file names); everything else,
// including the joiners emoji and many scripts need, is ordinary text.
func badText(s string) bool {
	for _, r := range s {
		switch {
		case r == '\t' || r == '\n' || r == '\r':
		case r < 0x20, r >= 0x7f && r <= 0x9f:
			return true
		case r == 0x2028 || r == 0x2029:
			return true
		case r >= 0x202a && r <= 0x202e, r >= 0x2066 && r <= 0x2069:
			return true
		}
	}
	return false
}

// RegisterEnum makes the values of an enum type known to the validator.
// The generated tether_gen.go files call it; projects do not.
func RegisterEnum[T ~string](values ...T) {
	set := make(map[string]bool, len(values))
	for _, v := range values {
		set[string(v)] = true
	}
	enumMu.Lock()
	enums[reflect.TypeFor[T]()] = set
	enumMu.Unlock()
}

var (
	enumMu sync.RWMutex
	enums  = map[reflect.Type]map[string]bool{}
)

func enumValues(t reflect.Type) map[string]bool {
	enumMu.RLock()
	defer enumMu.RUnlock()
	return enums[t]
}

type projectRule struct {
	re      *regexp.Regexp
	check   func(string) bool
	message string
}

var (
	ruleMu       sync.RWMutex
	projectRules = map[string]projectRule{}
)

// Pattern registers a project rule that a string must match as a whole.
// It runs on the server and, translated to JavaScript, in the browser.
// Call it from an init function with constant arguments, so the generator
// can read them. It panics on an invalid name or pattern, at start-up.
func Pattern(name, expr, message string) {
	if _, err := jsregex.Translate(expr); err != nil {
		panic(fmt.Sprintf("tether: pattern %s: %v", name, err))
	}
	re, err := regexp.Compile(`^(?:` + expr + `)$`)
	if err != nil {
		panic(fmt.Sprintf("tether: pattern %s: %v", name, err))
	}
	registerRule(name, projectRule{re: re, message: message})
}

// Check registers a project rule implemented in Go. It runs on the server
// only; the browser lets the value through and the server reports it. Use
// it for what a pattern cannot express, such as check digits.
func Check(name string, fn func(string) bool, message string) {
	if fn == nil {
		panic(fmt.Sprintf("tether: check %s: nil function", name))
	}
	registerRule(name, projectRule{check: fn, message: message})
}

func registerRule(name string, r projectRule) {
	if !vtag.IsName(name) || vtag.Builtin[name] || name == "dive" {
		panic(fmt.Sprintf("tether: %q cannot name a rule", name))
	}
	if r.message == "" {
		panic(fmt.Sprintf("tether: rule %s needs a message", name))
	}
	ruleMu.Lock()
	defer ruleMu.Unlock()
	if _, ok := projectRules[name]; ok {
		panic(fmt.Sprintf("tether: rule %s registered twice", name))
	}
	projectRules[name] = r
}

func projectRuleExists(name string) bool {
	ruleMu.RLock()
	defer ruleMu.RUnlock()
	_, ok := projectRules[name]
	return ok
}

func lookupRule(name string) (projectRule, bool) {
	ruleMu.RLock()
	defer ruleMu.RUnlock()
	r, ok := projectRules[name]
	return r, ok
}
