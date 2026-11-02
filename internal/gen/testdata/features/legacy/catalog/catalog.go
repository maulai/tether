// Package catalog has the same name as features/catalog, so the generated
// registration needs an import alias; it is also outside the generator's
// package patterns, so its constants come from type information alone.
package catalog

// Grade is the quality grade of an item.
type Grade string

const (
	GradeA Grade = "a"
	GradeB Grade = "b"
)
