package main

import (
	"strings"
	"unicode"
)

// findSentenceBoundary ports the splitting logic from the main server to the TTS engine.
// It uses a two-stage approach:
// Stage 1: Aggressive splitting to minimize Time-to-First-Audio (TTFA).
// Stage 2: Relaxed splitting (prefer paragraphs) once audio is in-flight to improve prosody.
func findSentenceBoundary(text string, minLength int, hardLimit int) int {
	// Stage 1 (minLength = 0): Aggressive splitting to minimize time-to-first-audio
	if minLength == 0 {
		if idx := strings.Index(text, "\n"); idx != -1 {
			return idx
		}
	}

	if len(text) < minLength {
		// Even if shorter than minLength, respect the hard limit
		if hardLimit > 0 && len(text) >= hardLimit {
			return len(text) - 1
		}
		return -1
	}

	for i := minLength; i < len(text); i++ {
		c := text[i]
		if c == '.' || c == '!' || c == '?' || c == ':' || c == '\n' {
			// Don't split if it's the very last character (more context might come)
			if i+1 == len(text) {
				continue
			}

			// Only split if followed by whitespace (except newlines, those are implicit boundaries)
			if c != '\n' && !unicode.IsSpace(rune(text[i+1])) {
				continue
			}

			if c == '.' {
				// 1. Avoid decimals (e.g. "v1.0") - check if preceded by digit and followed by digit
				// 2. Numbered list check: "[LINE_START][NUMBER]. "
				wordStart := i
				for wordStart > 0 && !unicode.IsSpace(rune(text[wordStart-1])) {
					wordStart--
				}
				word := text[wordStart:i]

				if isNumeric(word) {
					if wordStart == 0 || text[wordStart-1] == '\n' {
						continue // Don't split on "1. " at line start
					}
				}

				// 3. Avoid common abbreviations
				if isCommonAbbreviation(strings.ToLower(word)) {
					continue
				}
			}

			return i
		}
	}

	// Hard limit fallback
	if hardLimit > 0 && len(text) >= hardLimit {
		return len(text) - 1
	}

	return -1
}


func isNumeric(s string) bool {
	for _, r := range s {
		if !unicode.IsDigit(r) {
			return false
		}
	}
	return s != ""
}

func isCommonAbbreviation(word string) bool {
	switch word {
	case "mr", "mrs", "dr", "vs", "eg", "ie", "vol", "v", "st", "prof", "inc", "co", "v1", "v2", "v3", "v4", "v5":
		return true
	}
	return false
}
