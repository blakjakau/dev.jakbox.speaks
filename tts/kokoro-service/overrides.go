package main

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

type OverrideRule struct {
	Description string `json:"description"`
	Type        string `json:"type"` // "regex" or "string"
	Match       string `json:"match"`
	Replace     string `json:"replace"`
	regex       *regexp.Regexp
}

type TTSOverrides struct {
	Global []OverrideRule `json:"global"`
	Kokoro []OverrideRule `json:"kokoro"`
	Piper  []OverrideRule `json:"piper"`
}

var (
	currentOverrides    []OverrideRule
	overridesMutex      sync.RWMutex
	overridesFile       = "tts-override.json"
	lastOverridesUpdate time.Time
)

func initOverrides() {
	// Look for tts-override.json in current dir or parent dir (to support running from build/ subdir)
	if _, err := os.Stat(overridesFile); os.IsNotExist(err) {
		if _, err := os.Stat("../" + overridesFile); err == nil {
			overridesFile = "../" + overridesFile
		}
	}

	loadOverrides()
	go watchOverrides()
}

func loadOverrides() {
	absPath, _ := filepath.Abs(overridesFile)
	data, err := os.ReadFile(overridesFile)
	if err != nil {
		if os.IsNotExist(err) {
			log.Printf("[Overrides] %s not found (resolved to %s), using defaults", overridesFile, absPath)
			return
		}
		log.Printf("[Overrides] Error reading %s: %v", overridesFile, err)
		return
	}

	var tts TTSOverrides
	if err := json.Unmarshal(data, &tts); err != nil {
		log.Printf("[Overrides] Error parsing %s: %v", overridesFile, err)
		return
	}

	var rules []OverrideRule
	// Add global rules first
	for _, r := range tts.Global {
		if r.Type == "regex" {
			re, err := regexp.Compile(r.Match)
			if err != nil {
				log.Printf("[Overrides] Invalid regex in global rule '%s': %v", r.Description, err)
				continue
			}
			r.regex = re
		}
		rules = append(rules, r)
		log.Printf("[Overrides] Added global rule: [%s] %s -> %s", r.Type, r.Match, r.Replace)
	}

	// Add Kokoro specific rules
	for _, r := range tts.Kokoro {
		if r.Type == "regex" {
			re, err := regexp.Compile(r.Match)
			if err != nil {
				log.Printf("[Overrides] Invalid regex in kokoro rule '%s': %v", r.Description, err)
				continue
			}
			r.regex = re
		}
		rules = append(rules, r)
		log.Printf("[Overrides] Added kokoro rule: [%s] %s -> %s", r.Type, r.Match, r.Replace)
	}

	overridesMutex.Lock()
	currentOverrides = rules
	overridesMutex.Unlock()

	log.Printf("[Overrides] Loaded %d total rules from %s", len(rules), absPath)
}

func watchOverrides() {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		info, err := os.Stat(overridesFile)
		if err != nil {
			continue
		}

		if info.ModTime().After(lastOverridesUpdate) {
			lastOverridesUpdate = info.ModTime()
			log.Printf("[Overrides] %s changed (ModTime: %v), reloading...", overridesFile, lastOverridesUpdate)
			loadOverrides()
		}
	}
}

func applyOverrides(text string) string {
	overridesMutex.RLock()
	defer overridesMutex.RUnlock()

	original := text
	for _, r := range currentOverrides {
		if r.Type == "regex" && r.regex != nil {
			text = r.regex.ReplaceAllString(text, r.Replace)
		} else if r.Type == "string" {
			text = strings.ReplaceAll(text, r.Match, r.Replace)
		}
	}

	if text != original {
		log.Printf("[Overrides] Text modified:\n  IN:  %s\n  OUT: %s", original, text)
	}

	return text
}
