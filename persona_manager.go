package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

type PersonaTheme struct {
	Primary    string `json:"primary"`
	Secondary  string `json:"secondary"`
	Tertiary   string `json:"tertiary"`
	Background string `json:"background"`
	Panel      string `json:"panel"`
}

type Persona struct {
	Name                  string        `json:"name"`
	NameMutations         string        `json:"name_mutations"`
	PhoneticPronunciation string        `json:"phonetic_pronunciation"`
	Tone                  string        `json:"tone"`
	AddressStyle          string        `json:"address_style"`
	Focus                 string        `json:"focus"`
	InteractionStyle      string        `json:"interaction_style"`
	Constraints           string        `json:"constraints"`
	VoiceFile             string        `json:"voice_file"`
	Voice                 []VoiceOption `json:"voice"`
	VoiceNoiseScale       float64       `json:"voice_noise_scale,omitempty"`
	VoiceNoiseW           float64       `json:"voice_noise_w,omitempty"`
	VoiceLengthScale      float64       `json:"voice_length_scale,omitempty"`
	VoiceVariance         float64       `json:"voice_variance,omitempty"`
	Theme                 PersonaTheme  `json:"theme"`
}

func init() {
	loadPersonas("personas.json")
	watchPersonas("personas.json")
}

func (p *Persona) UnmarshalJSON(data []byte) error {
	type Alias Persona
	aux := &struct {
		Voice json.RawMessage `json:"voice"`
		*Alias
	}{
		Alias: (*Alias)(p),
	}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}

	if len(aux.Voice) > 0 {
		// Try to unmarshal as []VoiceOption
		var voiceOptions []VoiceOption
		if err := json.Unmarshal(aux.Voice, &voiceOptions); err == nil {
			p.Voice = voiceOptions
		} else {
			// Try to unmarshal as []string
			var voiceStrings []string
			if err := json.Unmarshal(aux.Voice, &voiceStrings); err == nil {
				p.Voice = make([]VoiceOption, len(voiceStrings))
				for i, s := range voiceStrings {
					p.Voice[i] = VoiceOption{Name: s}
				}
			} else {
				// If it's something else, let it be empty or return error
			}
		}
	}

	return nil
}

var (
	personas      map[string]Persona
	personasMutex sync.RWMutex
)

func loadPersonas(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	var newPersonas map[string]Persona
	if err := json.Unmarshal(data, &newPersonas); err != nil {
		return err
	}

	personasMutex.Lock()
	personas = newPersonas
	personasMutex.Unlock()

	log.Printf("Loaded %d personas from %s", len(personas), path)
	return nil
}

func watchPersonas(path string) {
	initialStat, err := os.Stat(path)
	if err != nil {
		log.Printf("Error stating personas file: %v", err)
		return
	}

	lastModTime := initialStat.ModTime()
	ticker := time.NewTicker(2 * time.Second)
	go func() {
		for range ticker.C {
			stat, err := os.Stat(path)
			if err != nil {
				continue
			}
			if stat.ModTime().After(lastModTime) {
				lastModTime = stat.ModTime()
				log.Println("Personas file change detected, reloading...")
				if err := loadPersonas(path); err != nil {
					log.Printf("FAILED to reload personas: %v (Update ignored)", err)
				} else {
					log.Println("Personas successfully reloaded, updating active sessions...")
					activeSessionsMutex.Lock()
					for _, session := range activeSessions {
						session.Mutex.Lock()
						if session.Voice != "" {
							vName := strings.ToLower(extractVoiceName(session.Voice))
							personasMutex.RLock()
							if p, ok := personas[vName]; ok {
								session.Theme = p.Theme
							}
							personasMutex.RUnlock()
						}
						session.Mutex.Unlock()
					}
					activeSessionsMutex.Unlock()
				}
			}
		}
	}()
}

func normalisePersonaName(session *ClientSession, content string) string {
	session.Mutex.Lock()
	vName := strings.ToLower(extractVoiceName(session.Voice))
	session.Mutex.Unlock()

	if vName == "" {
		return content
	}

	personasMutex.RLock()
	persona, personaOk := personas[vName]
	personasMutex.RUnlock()

	if personaOk && persona.NameMutations != "" {
		mutations := strings.Fields(persona.NameMutations)
		for _, mut := range mutations {
			re, err := regexp.Compile("(?i)\\b" + regexp.QuoteMeta(mut) + "\\b")
			if err == nil {
				content = re.ReplaceAllString(content, persona.Name)
			}
		}
	}

	return content
}

func extractVoiceName(filename string) string {
	base := strings.TrimSuffix(filename, ".onnx")

	// Strip known technical suffixes first
	re := regexp.MustCompile(`-(qint8|int8|fp16|low|medium|high|standard)$`)
	base = re.ReplaceAllString(base, "")

	parts := strings.Split(base, "-")
	if len(parts) >= 2 {
		// If it looks like lang-name (e.g. en_GB-alba), return the name
		// Check if first part looks like a language code (e.g. en_GB, en_US)
		langRe := regexp.MustCompile(`^[a-z]{2}_[A-Z]{2}$`)
		if langRe.MatchString(parts[0]) {
			return parts[1]
		}
	}

	// Fallback to the whole base name if no dash-split or it doesn't look like lang code
	return base
}

func handleVoices(w http.ResponseWriter, r *http.Request) {
	type PersonaInfo struct {
		ID        string `json:"id"`
		Name      string `json:"name"`
		VoiceFile string `json:"voice_file"`
	}

	personasMutex.RLock()
	defer personasMutex.RUnlock()

	var out []PersonaInfo
	for id, p := range personas {
		if p.VoiceFile == "" {
			continue
		}
		// Check if the voice file actually exists
		modelPath := filepath.Join(".", "piper", "models", p.VoiceFile)
		if _, err := os.Stat(modelPath); err == nil {
			out = append(out, PersonaInfo{
				ID:        id,
				Name:      p.Name,
				VoiceFile: p.VoiceFile,
			})
		}
	}

	// Sort by name for UI consistency
	sort.Slice(out, func(i, j int) bool {
		return out[i].Name < out[j].Name
	})

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out)
}
