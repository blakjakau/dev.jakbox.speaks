package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"
)

type OverrideRule struct {
	Description string            `json:"description"`
	Type        string            `json:"type"` // "regex", "string", or "map"
	Match       string            `json:"match"`
	Replace     string            `json:"replace"`
	Map         map[string]string `json:"map"`
	PreSplit    bool              `json:"pre_split"`
	regex       *regexp.Regexp
	replacer    *strings.Replacer
}

type TTSOverrides struct {
	Global []OverrideRule `json:"global"`
	Kokoro []OverrideRule `json:"kokoro"`
	Piper  []OverrideRule `json:"piper"`
}

var (
	preSplitOverrides   []OverrideRule
	postSplitOverrides  []OverrideRule
	overridesMutex      sync.RWMutex
	overridesFile       = "tts-override.json"
	lastOverridesUpdate time.Time
)

func InitOverrides() {
	// Look for tts-override.json in current dir or parent dir (to support running from subdirectories)
	if _, err := os.Stat(overridesFile); os.IsNotExist(err) {
		if _, err := os.Stat("../" + overridesFile); err == nil {
			overridesFile = "../" + overridesFile
		}
	}

	loadOverrides()
	go watchOverrides()
}

func loadOverrides() {
	data, err := os.ReadFile(overridesFile)
	if err != nil {
		if os.IsNotExist(err) {
			log.Printf("[Overrides] %s not found, using defaults", overridesFile)
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

	var preRules []OverrideRule
	var postRules []OverrideRule

	processRule := func(r OverrideRule, category string) {
		if r.Type == "regex" {
			re, err := regexp.Compile(r.Match)
			if err != nil {
				log.Printf("[Overrides] Invalid regex in %s rule '%s': %v", category, r.Description, err)
				return
			}
			r.regex = re
		} else if r.Type == "map" {
			if len(r.Map) > 0 {
				var pairs []string
				for k, v := range r.Map {
					pairs = append(pairs, k, v)
				}
				r.replacer = strings.NewReplacer(pairs...)
			}
		}

		if r.PreSplit {
			preRules = append(preRules, r)
		} else {
			postRules = append(postRules, r)
		}
	}

	// Add global rules first
	for _, r := range tts.Global {
		processRule(r, "global")
	}

	// Add Piper specific rules
	for _, r := range tts.Piper {
		processRule(r, "piper")
	}

	overridesMutex.Lock()
	preSplitOverrides = preRules
	postSplitOverrides = postRules
	overridesMutex.Unlock()

	log.Printf("[Overrides] Loaded %d rules (Pre: %d, Post: %d) from %s", len(preRules)+len(postRules), len(preRules), len(postRules), overridesFile)
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
			log.Printf("[Overrides] %s changed, reloading...", overridesFile)
			loadOverrides()
		}
	}
}

func ApplyPreSplit(text string) (string, *strings.Replacer, *strings.Replacer) {
	overridesMutex.RLock()
	defer overridesMutex.RUnlock()

	if len(preSplitOverrides) == 0 {
		return text, strings.NewReplacer(), strings.NewReplacer()
	}

	marked := text
	var inferencePairs []string
	var displayPairs []string

	for i, r := range preSplitOverrides {
		found := false
		matchCount := 0

		if r.Type == "regex" && r.regex != nil {
			marked = r.regex.ReplaceAllStringFunc(marked, func(match string) string {
				found = true
				marker := fmt.Sprintf("\x00R%d_%d\x01", i, matchCount)
				matchCount++
				inferencePairs = append(inferencePairs, marker, r.ReplaceAll(match))
				displayPairs = append(displayPairs, marker, match)
				return marker
			})
		} else if r.Type == "string" {
			if strings.Contains(marked, r.Match) {
				found = true
				marker := fmt.Sprintf("\x00R%d\x01", i)
				marked = strings.ReplaceAll(marked, r.Match, marker)
				inferencePairs = append(inferencePairs, marker, r.Replace)
				displayPairs = append(displayPairs, marker, r.Match)
			}
		} else if r.Type == "map" && r.replacer != nil {
			for k, v := range r.Map {
				if strings.Contains(marked, k) {
					found = true
					subMarker := fmt.Sprintf("\x00M%d_%s\x01", i, k)
					marked = strings.ReplaceAll(marked, k, subMarker)
					inferencePairs = append(inferencePairs, subMarker, v)
					displayPairs = append(displayPairs, subMarker, k)
				}
			}
		}

		if found {
			log.Printf("[Overrides:Pre] Marked rule: %s", r.Description)
		}
	}

	return marked, strings.NewReplacer(inferencePairs...), strings.NewReplacer(displayPairs...)
}

func (r *OverrideRule) ReplaceAll(match string) string {
	if r.Type == "regex" && r.regex != nil {
		return r.regex.ReplaceAllString(match, r.Replace)
	}
	return r.Replace
}

func ApplyPostSplit(text string) string {
	overridesMutex.RLock()
	defer overridesMutex.RUnlock()

	if len(postSplitOverrides) == 0 {
		return text
	}

	for _, r := range postSplitOverrides {
		switch r.Type {
		case "regex":
			if r.regex != nil {
				text = r.regex.ReplaceAllString(text, r.Replace)
			}
		case "string":
			text = strings.ReplaceAll(text, r.Match, r.Replace)
		case "map":
			if r.replacer != nil {
				text = r.replacer.Replace(text)
			}
		}
	}

	return text
}
