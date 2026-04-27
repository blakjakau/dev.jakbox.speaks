package tts

import (
	"regexp"
	"strings"
)

// Sanitise prepares text for TTS by handling session-specific replacements like user names and persona phonetics.
// Final text cleaning (URL stripping, emoji removal, etc.) is handled by the downstream TTS microservices.
func Sanitise(text string, userName string, personaName string, phoneticName string) string {
	// 1. User Name specific cleanup (case-insensitive)
	if userName != "" {
		nameRe := regexp.MustCompile("(?i),\\s*" + regexp.QuoteMeta(userName) + "\\b")
		text = nameRe.ReplaceAllString(text, " "+userName)
	}

	// 2. Persona Phonetic Replacement (case-insensitive)
	if phoneticName != "" && personaName != "" {
		// Replace the persona's name with its phonetic pronunciation if provided
		pNameRe := regexp.MustCompile("(?i)\\b" + regexp.QuoteMeta(personaName) + "\\b")
		text = pNameRe.ReplaceAllString(text, phoneticName)
	}

	return strings.TrimSpace(text)
}
