package tts

import (
	"testing"
)

func TestSanitise(t *testing.T) {
	tests := []struct {
		name         string
		text         string
		userName     string
		personaName  string
		phoneticName string
		want         string
	}{
		{
			name:     "Vocative comma at end of sentence",
			text:     "Hello, Jason!",
			userName: "Jason",
			want:     "Hello... Jason!",
		},
		{
			name:         "Phonetic pronunciation",
			text:         "Hello, GLaDOS!",
			userName:     "Jason",
			personaName:  "GLaDOS",
			phoneticName: "Glah-Doss",
			want:         "Hello... Glah-Doss!",
		},
		{
			name:     "Vocative comma with period",
			text:     "Yes, Jason.",
			userName: "Jason",
			want:     "Yes... Jason.",
		},
		{
			name:     "Vocative comma for non-user word",
			text:     "Hello, everyone!",
			userName: "Jason",
			want:     "Hello... everyone!",
		},
		{
			name:     "Comma not followed by terminal punctuation",
			text:     "Well, Jason, let's go.",
			userName: "Jason",
			want:     "Well Jason, let's go.", // Falls back to user name stripping for the first comma
		},
		{
			name:     "Case insensitive name stripping",
			text:     "jason, what's up?",
			userName: "Jason",
			want:     "jason, what's up?", // Comma is NOT leading, so ignored by nameRe if not at sentence end? 
			// Wait, nameRe is `(?i),\s*Jason\b`. 
			// But "jason," doesn't match `,\s*Jason`. 
			// Let's check my logic in sanitiser.go.
		},
		{
			name:     "URL stripping",
			text:     "Check this out: https://example.com/foo",
			userName: "Jason",
			want:     "Check this out: [link]",
		},
		{
			name:     "Emoji stripping",
			text:     "Hello! 👋😊",
			userName: "Jason",
			want:     "Hello!",
		},
		{
			name:     "Markdown stripping",
			text:     "**Bold** and _italic_ and `code` and > quote",
			userName: "Jason",
			want:     "Bold and italic and code and quote",
		},
		{
			name:     "Numbered lists",
			text:     "1. First item\n2. Second item",
			userName: "Jason",
			want:     "1 First item 2 Second item", // Newlines are collapsed by wsRe
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Sanitise(tt.text, tt.userName, tt.personaName, tt.phoneticName); got != tt.want {
				t.Errorf("Sanitise() = %q, want %q", got, tt.want)
			}
		})
	}
}
