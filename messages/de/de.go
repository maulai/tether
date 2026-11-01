// Package de holds German texts for tether's message codes:
//
//	func init() { tether.Messages(de.Messages) }
package de

// Messages maps every built-in message code to German text.
var Messages = map[string]string{
	"required":    "Pflichtfeld.",
	"requiredif":  "Pflichtfeld.",
	"forbiddenif": "Muss leer bleiben.",
	"type":        "Ungültiger Wert.",
	"integer":     "Bitte eine ganze Zahl eingeben.",
	"unknown":     "Unbekanntes Feld.",
	"text":        "Enthält unsichtbare Steuerzeichen.",
	"minLength":   "Mindestens {min} Zeichen.",
	"maxLength":   "Höchstens {max} Zeichen.",
	"min":         "Mindestens {min}.",
	"max":         "Höchstens {max}.",
	"gt":          "Muss größer als {gt} sein.",
	"lt":          "Muss kleiner als {lt} sein.",
	"minItems":    "Mindestens {min} Einträge.",
	"maxItems":    "Höchstens {max} Einträge.",
	"enum":        "Bitte einen der angebotenen Werte wählen.",
	"oneof":       "Erlaubt sind: {values}.",
	"email":       "Keine gültige E-Mail-Adresse.",
	"url":         "Bitte eine Adresse mit http:// oder https:// angeben.",
	"invalid":     "Bitte die markierten Felder prüfen.",
	"malformed":   "Die Anfrage konnte nicht gelesen werden.",
	"tooLarge":    "Die Anfrage ist zu groß.",
	"internal":    "Es ist ein Fehler aufgetreten.",
	"network":     "Der Server ist nicht erreichbar.",
	"range":       "Der angeforderte Bereich ist nicht verfügbar.",
}
