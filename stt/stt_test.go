package stt

import (
	"testing"
)

func TestAlignTranscripts(t *testing.T) {
	tests := []struct {
		name          string
		old           string
		new           string
		expectStable  string
		expectSpec    string
	}{
		{
			name:         "Simple overlap",
			old:          "hello world this is",
			new:          "this is a test",
			expectStable: "hello world",
			expectSpec:   "this is a test",
		},
		{
			name:         "No overlap",
			old:          "hello world",
			new:          "completely different",
			expectStable: "",
			expectSpec:   "completely different",
		},
		{
			name:         "Middle overlap",
			old:          "i think that we should",
			new:          "we should go home now",
			expectStable: "i think that",
			expectSpec:   "we should go home now",
		},
		{
			name:         "Single word overlap (ignored)",
			old:          "hello world",
			new:          "world peace",
			expectStable: "",
			expectSpec:   "world peace",
		},
		{
			name:         "Case insensitive match",
			old:          "HELLO WORLD THIS IS",
			new:          "this is a test",
			expectStable: "HELLO WORLD",
			expectSpec:   "this is a test",
		},
		{
			name:         "Superset progressive stability",
			old:          "this is a long sentence that",
			new:          "this is a long sentence that keeps growing",
			expectStable: "this is a long",
			expectSpec:   "sentence that keeps growing",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stable, spec := alignTranscripts(tt.old, tt.new)
			if stable != tt.expectStable {
				t.Errorf("alignTranscripts() stable = %v, want %v", stable, tt.expectStable)
			}
			if spec != tt.expectSpec {
				t.Errorf("alignTranscripts() spec = %v, want %v", spec, tt.expectSpec)
			}
		})
	}
}

func TestAlignTranscriptsRolling(t *testing.T) {
	tests := []struct {
		name         string
		old          string
		new          string
		expectStable string
		expectSpec   string
	}{
		{
			name:         "Simple overlap clear of edges",
			old:          "hey there could you please go to the",
			new:          "could you please go to the store",
			expectStable: "hey there could you please",
			expectSpec:   "go to the store",
		},
		{
			name:         "Edge hallucination on new segment",
			old:          "hey there could you please",
			new:          "uhhh could you please go to the",
			expectStable: "hey there",
			expectSpec:   "could you please go to the",
		},
		{
			name:         "Edge hallucination on old segment",
			old:          "hey there could you uhhh",
			new:          "there could you please go to",
			expectStable: "hey",
			expectSpec:   "there could you please go to",
		},
		{
			name:         "Very short segment fallback",
			old:          "hey there",
			new:          "there buddy",
			expectStable: "hey",
			expectSpec:   "there buddy",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stable, spec := alignTranscriptsRolling(tt.old, tt.new)
			if stable != tt.expectStable {
				t.Errorf("alignTranscriptsRolling() stable = '%v', want '%v'", stable, tt.expectStable)
			}
			if spec != tt.expectSpec {
				t.Errorf("alignTranscriptsRolling() spec = '%v', want '%v'", spec, tt.expectSpec)
			}
		})
	}
}
