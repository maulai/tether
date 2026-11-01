// Package jsregex translates a Go regular expression into JavaScript, so a
// pattern checked by the server is checked identically in the browser.
//
// It works on the parse tree, not on the text: Go expands \d, \s, \w,
// Unicode classes and case folding into explicit ranges, and those ranges
// are written out for JavaScript. Shorthands are never passed through,
// because they mean different things in the two engines (JavaScript's \s
// also matches U+00A0, Go's does not; JavaScript's . does not match \r).
// The output is meant for new RegExp(src, "u").
package jsregex

import (
	"fmt"
	"regexp/syntax"
	"slices"
	"strconv"
	"strings"
	"unicode"
)

// Translate returns the JavaScript source of expr. It fails for constructs
// with no exact JavaScript equivalent.
func Translate(expr string) (string, error) {
	re, err := syntax.Parse(expr, syntax.Perl)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	if err := write(&b, re); err != nil {
		return "", err
	}
	return b.String(), nil
}

func write(b *strings.Builder, re *syntax.Regexp) error {
	switch re.Op {
	case syntax.OpNoMatch:
		b.WriteString(`[^\u{0}-\u{10FFFF}]`)
	case syntax.OpEmptyMatch:
		b.WriteString("(?:)")
	case syntax.OpLiteral:
		for _, r := range re.Rune {
			if re.Flags&syntax.FoldCase != 0 {
				writeFolded(b, r)
			} else {
				b.WriteString(escape(r))
			}
		}
	case syntax.OpCharClass:
		writeClass(b, re.Rune)
	case syntax.OpAnyCharNotNL:
		b.WriteString(`[^\n]`)
	case syntax.OpAnyChar:
		b.WriteString(`[\u{0}-\u{10FFFF}]`)
	case syntax.OpBeginLine, syntax.OpEndLine:
		return fmt.Errorf("multiline anchors (?m) are not supported")
	case syntax.OpBeginText:
		b.WriteString("^")
	case syntax.OpEndText:
		b.WriteString("$")
	case syntax.OpWordBoundary:
		b.WriteString(`\b`)
	case syntax.OpNoWordBoundary:
		b.WriteString(`\B`)
	case syntax.OpCapture:
		b.WriteString("(?:")
		if err := write(b, re.Sub[0]); err != nil {
			return err
		}
		b.WriteString(")")
	case syntax.OpStar, syntax.OpPlus, syntax.OpQuest, syntax.OpRepeat:
		if err := writeAtom(b, re.Sub[0]); err != nil {
			return err
		}
		switch re.Op {
		case syntax.OpStar:
			b.WriteString("*")
		case syntax.OpPlus:
			b.WriteString("+")
		case syntax.OpQuest:
			b.WriteString("?")
		default:
			switch {
			case re.Max == -1:
				fmt.Fprintf(b, "{%d,}", re.Min)
			case re.Min == re.Max:
				fmt.Fprintf(b, "{%d}", re.Min)
			default:
				fmt.Fprintf(b, "{%d,%d}", re.Min, re.Max)
			}
		}
		if re.Flags&syntax.NonGreedy != 0 {
			b.WriteString("?")
		}
	case syntax.OpConcat:
		for _, sub := range re.Sub {
			if err := write(b, sub); err != nil {
				return err
			}
		}
	case syntax.OpAlternate:
		b.WriteString("(?:")
		for i, sub := range re.Sub {
			if i > 0 {
				b.WriteString("|")
			}
			if err := write(b, sub); err != nil {
				return err
			}
		}
		b.WriteString(")")
	default:
		return fmt.Errorf("unsupported construct %v", re.Op)
	}
	return nil
}

// writeAtom writes re so a following quantifier applies to all of it.
func writeAtom(b *strings.Builder, re *syntax.Regexp) error {
	single := re.Op == syntax.OpCharClass || re.Op == syntax.OpAnyChar || re.Op == syntax.OpAnyCharNotNL ||
		re.Op == syntax.OpCapture || re.Op == syntax.OpAlternate ||
		re.Op == syntax.OpLiteral && len(re.Rune) == 1
	if single {
		return write(b, re)
	}
	b.WriteString("(?:")
	if err := write(b, re); err != nil {
		return err
	}
	b.WriteString(")")
	return nil
}

// writeFolded writes a case insensitive literal rune as the class of all
// runes it folds to, the same orbit Go's matcher uses.
func writeFolded(b *strings.Builder, r rune) {
	orbit := []rune{r}
	for f := unicode.SimpleFold(r); f != r; f = unicode.SimpleFold(f) {
		orbit = append(orbit, f)
	}
	if len(orbit) == 1 {
		b.WriteString(escape(r))
		return
	}
	slices.Sort(orbit)
	ranges := make([]rune, 0, 2*len(orbit))
	for _, o := range orbit {
		ranges = append(ranges, o, o)
	}
	writeClass(b, ranges)
}

func writeClass(b *strings.Builder, ranges []rune) {
	if len(ranges) == 0 {
		b.WriteString(`[^\u{0}-\u{10FFFF}]`)
		return
	}
	b.WriteString("[")
	for i := 0; i+1 < len(ranges); i += 2 {
		lo, hi := ranges[i], ranges[i+1]
		b.WriteString(escapeInClass(lo))
		if hi != lo {
			b.WriteString("-")
			b.WriteString(escapeInClass(hi))
		}
	}
	b.WriteString("]")
}

// escape writes a rune for use outside a class. In unicode mode JavaScript
// rejects escapes of ordinary characters, so only syntax characters get a
// backslash; everything outside printable ASCII is written as a code point.
func escape(r rune) string {
	if strings.ContainsRune(`^$\.*+?()[]{}|/`, r) {
		return `\` + string(r)
	}
	if r >= 0x20 && r < 0x7f {
		return string(r)
	}
	return codePoint(r)
}

func escapeInClass(r rune) string {
	if strings.ContainsRune(`\]-^[/`, r) {
		return `\` + string(r)
	}
	if r >= 0x20 && r < 0x7f {
		return string(r)
	}
	return codePoint(r)
}

func codePoint(r rune) string {
	return `\u{` + strings.ToUpper(strconv.FormatInt(int64(r), 16)) + `}`
}
