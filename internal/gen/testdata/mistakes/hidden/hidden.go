// Package hidden declares an enum whose constant another package cannot name.
package hidden

type Level string

const (
	LevelHigh Level = "high"
	levelLow  Level = "low"
)
