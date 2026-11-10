package tether

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

type target struct {
	Purpose string    `json:"purpose"`
	Limit   float64   `json:"limit"`
	When    time.Time `json:"when"`
}

// A rule that reads another field, or compares entries, is checked when
// the plan is made, with the words the generator uses, instead of failing
// on the first request that reaches it.
func TestRuleMistakes(t *testing.T) {
	cases := []struct {
		v    any
		want string
	}{
		{struct {
			Tags []string `json:"tags" v:"unique=sku"`
		}{}, "unique reads sku, but each element is a string"},
		{struct {
			Days []time.Time `json:"days" v:"unique"`
		}{}, "each element is a time"},
		{struct {
			Blobs [][]byte `json:"blobs" v:"unique"`
		}{}, "each element is a []byte"},
		{struct {
			Lines []target `json:"lines" v:"unique=nope"`
		}{}, `unique refers to unknown field "nope"`},
		{struct {
			Stamps []target `json:"stamps" v:"unique=when"`
		}{}, "when is a time"},
		{struct {
			Kind   string            `json:"kind"`
			Labels map[string]string `json:"labels" v:"dive,requiredif=kind:x"`
		}{}, "requiredif reads another field of the struct; after dive there is none"},
		{struct {
			From  []time.Time `json:"from"`
			Until []time.Time `json:"until" v:"dive,minfield=from"`
		}{}, "minfield reads another field of the struct; after dive there is none"},
		{struct {
			Low   int       `json:"low"`
			Until time.Time `json:"until" v:"minfield=low"`
		}{}, "minfield compares a time with low, an integer"},
		{struct {
			Target target `json:"target"`
			Far    string `json:"far" v:"requiredif=target.nope:x"`
		}{}, `requiredif refers to unknown field "target.nope"`},
		{struct {
			Targets []target `json:"targets"`
			Deep    string   `json:"deep" v:"requiredif=targets.purpose:x"`
		}{}, "requiredif reads targets.purpose, but targets is a slice"},
		{struct {
			Kind string `json:"kind"`
			Leaf string `json:"leaf" v:"requiredif=kind.x:y"`
		}{}, "but kind is a string"},
		{struct {
			Target target  `json:"target"`
			Fine   float64 `json:"fine" v:"forbiddenif=target.purpose:loan,ltfield=target.limit"`
		}{}, ""},
	}
	for i, c := range cases {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			defer func() {
				r := recover()
				switch {
				case c.want == "" && r != nil:
					t.Errorf("panic %v", r)
				case c.want != "" && (r == nil || !strings.Contains(fmt.Sprint(r), c.want)):
					t.Errorf("panic %v, want %q", r, c.want)
				}
			}()
			Validate(c.v)
		})
	}
}

type node struct {
	Name  string `json:"name"`
	Child *node  `json:"child"`
	Note  string `json:"note" v:"requiredif=child.name:x"`
}

// A path into a recursive type resolves once the plan is complete.
func TestPathIntoRecursiveType(t *testing.T) {
	err := Validate(node{Child: &node{Name: "x"}})
	if got := Issues(err); len(got) != 1 || got[0].Field != "note" || got[0].Rule != "requiredif" {
		t.Errorf("issues %+v", got)
	}
}
