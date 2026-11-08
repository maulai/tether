// Package msgs holds the built-in message texts. The server and the
// generator share it: the generator copies the texts into the TypeScript
// runtime, so the browser reports exactly what the server reports.
package msgs

import "strings"

// Default is the built-in English text for every message code.
var Default = map[string]string{
	"required":    "Required.",
	"requiredif":  "Required.",
	"forbiddenif": "Must be empty.",
	"type":        "Invalid value.",
	"integer":     "Must be a whole number.",
	"unknown":     "Unknown field.",
	"missing":     "Missing. A PUT sends every field; null or an empty value clears it.",
	"text":        "Contains invisible control characters.",
	"minLength":   "Must be at least {min} characters.",
	"maxLength":   "Must be at most {max} characters.",
	"min":         "Must be at least {min}.",
	"max":         "Must be at most {max}.",
	"gt":          "Must be greater than {gt}.",
	"lt":          "Must be less than {lt}.",
	"minItems":    "Must contain at least {min} entries.",
	"maxItems":    "Must contain at most {max} entries.",
	"enum":        "Must be one of the offered values.",
	"oneof":       "Must be one of: {values}.",
	"email":       "Must be a valid email address.",
	"url":         "Must be a web address starting with http:// or https://.",
	"invalid":     "Please check the marked fields.",
	"malformed":   "The request body could not be read.",
	"tooLarge":    "The request is too large.",
	"internal":    "Something went wrong.",
	"network":     "The server cannot be reached.",
	"range":       "The requested range is not available.",
	"notFound":    "Not found.",
}

// Format fills the placeholders of text, {min} and the like, with args.
func Format(text string, args map[string]string) string {
	for k, v := range args {
		text = strings.ReplaceAll(text, "{"+k+"}", v)
	}
	return text
}
