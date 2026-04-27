package main

import (
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	sherpa "github.com/k2-fsa/sherpa-onnx-go/sherpa_onnx"
)

const (
	MaxCachedModels = 5
	ModelTTL        = 30 * time.Minute
)

type Engine struct {
	tts        *sherpa.OfflineTts
	modelName  string
	sampleRate int
	lastUsed   time.Time
}

func (e *Engine) Close() {
	if e.tts != nil {
		sherpa.DeleteOfflineTts(e.tts)
		e.tts = nil
	}
}

type GlobalMetrics struct {
	StartTime              time.Time
	TotalUtterances        uint64
	TotalWords             uint64
	TotalAudioSeconds      float64
	TotalInferenceDuration time.Duration
	TotalRequestBytes      uint64
	TotalResponseBytes     uint64
	mu                     sync.Mutex
}

type Manager struct {
	cache      map[string]*Engine
	activeName string
	dataPath   string // eSpeak data path
	modelDir   string
	provider   string
	threads    int
	metrics    GlobalMetrics
	mu         sync.RWMutex
}

func NewManager(modelDir, espeakData, provider string, threads int) *Manager {
	m := &Manager{
		cache:    make(map[string]*Engine),
		dataPath: espeakData,
		modelDir: modelDir,
		provider: provider,
		threads:  threads,
		metrics: GlobalMetrics{
			StartTime: time.Now(),
		},
	}

	// Normalised provider names
	if strings.ToLower(m.provider) == "gpu" {
		m.provider = "cuda"
	}

	go m.reaper()
	return m
}

func (m *Manager) reaper() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		m.mu.Lock()
		now := time.Now()
		for name, engine := range m.cache {
			if name == m.activeName {
				continue
			}
			if now.Sub(engine.lastUsed) > ModelTTL {
				log.Printf("[Piper] Reaper: Evicting dormant model %s", name)
				delete(m.cache, name)
				engine.Close()
			}
		}
		m.mu.Unlock()
	}
}

func (m *Manager) LoadModel(modelName string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if engine, ok := m.cache[modelName]; ok {
		m.activeName = modelName
		engine.lastUsed = time.Now()
		return nil
	}

	// Evict if cache full
	if len(m.cache) >= MaxCachedModels {
		var lruName string
		var lruTime time.Time
		for name, e := range m.cache {
			if name == m.activeName {
				continue
			}
			if lruName == "" || e.lastUsed.Before(lruTime) {
				lruName = name
				lruTime = e.lastUsed
			}
		}
		if lruName != "" {
			log.Printf("[Piper] Cache full, evicting LRU: %s", lruName)
			engine := m.cache[lruName]
			delete(m.cache, lruName)
			engine.Close()
		}
	}

	modelPath := filepath.Join(m.modelDir, modelName)
	if !strings.HasSuffix(modelPath, ".onnx") {
		modelPath += ".onnx"
	}

	if _, err := os.Stat(modelPath); os.IsNotExist(err) {
		return fmt.Errorf("model not found: %s", modelPath)
	}

	log.Printf("[Piper] Loading model: %s (Provider: %s, Threads: %d)", modelName, m.provider, m.threads)

	config := sherpa.OfflineTtsConfig{
		Model: sherpa.OfflineTtsModelConfig{
			Vits: sherpa.OfflineTtsVitsModelConfig{
				Model:       modelPath,
				Lexicon:     "",
				Tokens:      filepath.Join(m.modelDir, "tokens.txt"),
				DataDir:     m.dataPath,
				NoiseScale:  0.667,
				LengthScale: 1.0,
			},
			NumThreads: m.threads,
			Debug:      0,
			Provider:   m.provider,
		},
	}

	tts := sherpa.NewOfflineTts(&config)
	if tts == nil {
		return fmt.Errorf("failed to initialize sherpa-onnx for model %s", modelName)
	}

	engine := &Engine{
		tts:        tts,
		modelName:  modelName,
		sampleRate: tts.SampleRate(),
		lastUsed:   time.Now(),
	}

	m.cache[modelName] = engine
	m.activeName = modelName
	return nil
}

func (m *Manager) GetSampleRate() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if e, ok := m.cache[m.activeName]; ok {
		return e.sampleRate
	}
	return 22050
}

func (m *Manager) Synthesize(text string, lengthScale, noiseScale, noiseW float32) ([]int16, error) {
	m.mu.RLock()
	engine := m.cache[m.activeName]
	m.mu.RUnlock()

	if engine == nil {
		return nil, errors.New("no model currently loaded")
	}

	start := time.Now()
	audio := engine.tts.Generate(text, 0, float32(lengthScale))
	duration := time.Since(start)

	if audio == nil {
		return nil, errors.New("synthesis failed")
	}

	// Update metrics
	audioLen := float64(len(audio.Samples)) / float64(engine.sampleRate)
	m.metrics.mu.Lock()
	m.metrics.TotalUtterances++
	m.metrics.TotalWords += uint64(len(strings.Fields(text)))
	m.metrics.TotalAudioSeconds += audioLen
	m.metrics.TotalInferenceDuration += duration
	m.metrics.mu.Unlock()

	return float32ToInt16Generic(audio.Samples), nil
}

func (m *Manager) SynthesizeStream(text string, lengthScale, noiseScale, noiseW float32, cb func([]int16)) error {
	samples, err := m.Synthesize(text, lengthScale, noiseScale, noiseW)
	if err != nil {
		return err
	}
	// Normalise streaming for Piper (it's non-streaming internally)
	cb(samples)
	return nil
}

func (m *Manager) GetDetailedStatus() (string, interface{}, int64, map[string]interface{}) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	active := m.activeName
	var totalSize int64
	cacheInfo := make([]map[string]interface{}, 0)

	for name, e := range m.cache {
		cacheInfo = append(cacheInfo, map[string]interface{}{
			"name":      name,
			"last_used": e.lastUsed,
		})
	}

	m.metrics.mu.Lock()
	defer m.metrics.mu.Unlock()
	
	rtf := 0.0
	if m.metrics.TotalAudioSeconds > 0 {
		rtf = m.metrics.TotalInferenceDuration.Seconds() / m.metrics.TotalAudioSeconds
	}

	metrics := map[string]interface{}{
		"uptime_seconds":           time.Since(m.metrics.StartTime).Seconds(),
		"total_utterances":         m.metrics.TotalUtterances,
		"total_words":              m.metrics.TotalWords,
		"total_audio_seconds":      m.metrics.TotalAudioSeconds,
		"average_real_time_factor": rtf,
	}

	return active, cacheInfo, totalSize, metrics
}

func (m *Manager) Status() (string, string) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.activeName, ""
}

func (m *Manager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, e := range m.cache {
		e.Close()
	}
	m.cache = make(map[string]*Engine)
}

func (m *Manager) RecordBytes(in, out uint64) {
	m.metrics.mu.Lock()
	defer m.metrics.mu.Unlock()
	m.metrics.TotalRequestBytes += in
	m.metrics.TotalResponseBytes += out
}

func float32ToInt16Generic(samples []float32) []int16 {
	result := make([]int16, len(samples))
	for i, s := range samples {
		val := s * 32767.0
		if val > 32767 {
			val = 32767
		} else if val < -32768 {
			val = -32768
		}
		result[i] = int16(val)
	}
	return result
}
