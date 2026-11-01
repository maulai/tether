package jsregex

import "testing"

func TestTranslate(t *testing.T) {
	cases := map[string]string{
		`#[0-9a-fA-F]{6}`: `#[0-9A-Fa-f]{6}`,
		`a.b`:             `a[^\n]b`,
		`\d+\s?`:          `[0-9]+[\u{9}-\u{A}\u{C}-\u{D} ]?`, // Go's \s, never JavaScript's
		`(?i)k`:           `[Kk\u{212A}]`,
		`(ab|cd)*`:        `(?:(?:ab|cd))*`,
		`x{2,}y{1,3}?`:    `x{2,}y{1,3}?`,
		`a/b\.c`:          `a\/b\.c`,
		`[^a]`:            `[\u{0}-` + "`" + `b-\u{10FFFF}]`,
		`é`:               `\u{E9}`,
	}
	for in, want := range cases {
		got, err := Translate(in)
		if err != nil {
			t.Errorf("%s: %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("%s: got %s, want %s", in, got, want)
		}
	}
}

func TestRejects(t *testing.T) {
	for _, in := range []string{`(?m)^a$`, `a(`} {
		if _, err := Translate(in); err == nil {
			t.Errorf("%s: no error", in)
		}
	}
}
