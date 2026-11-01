package tether

import (
	"sync"

	"github.com/maulai/tether/internal/msgs"
)

var (
	msgMu       sync.RWMutex
	msgOverride map[string]string
)

// Messages replaces message texts. The tables are merged from left to right
// over the built-in English one; a key may also name a project rule. The
// package github.com/maulai/tether/messages/de holds a German table.
//
// Call it once, from an init function, with map literals or package level
// variables initialised with map literals: the generator reads the same
// arguments to give the browser the same texts. A second call panics,
// because two places deciding the wording would contradict each other.
func Messages(tables ...map[string]string) {
	msgMu.Lock()
	defer msgMu.Unlock()
	if msgOverride != nil {
		panic("tether: Messages called twice")
	}
	msgOverride = map[string]string{}
	for _, t := range tables {
		for k, v := range t {
			msgOverride[k] = v
		}
	}
}

// message returns the text for code with its placeholders filled in.
func message(code string, args map[string]string) string {
	msgMu.RLock()
	text, ok := msgOverride[code]
	msgMu.RUnlock()
	if !ok {
		if r, isRule := lookupRule(code); isRule {
			text, ok = r.message, true
		}
	}
	if !ok {
		if text, ok = msgs.Default[code]; !ok {
			text = code
		}
	}
	return msgs.Format(text, args)
}
