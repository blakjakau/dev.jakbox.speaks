package stt

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Node represents a single Whisper transcription service instance.
type Node struct {
	URL                string        `json:"URL"`
	Zombie             bool          `json:"Zombie"`
	LastResponseTime   time.Duration `json:"LastResponseTime"`
	LastExecutionTime  time.Duration `json:"LastExecutionTime"`
	RollingCutoffRatio float64       `json:"RollingCutoffRatio"`
	FailureCount       int           `json:"FailureCount"`
	TotalRequests      int           `json:"TotalRequests"`
	TotalFailures      int           `json:"TotalFailures"`
}

// Manager coordinates a pool of STT nodes.
type Manager struct {
	nodes []*Node
	index uint32
	mu    sync.RWMutex
	onLog func(string)
}

// NewManager creates a new STT manager with the given node URLs and optional logging callback.
func NewManager(urls []string, onLog func(string)) *Manager {
	m := &Manager{onLog: onLog}
	m.UpdateURLs(urls)
	return m
}

// UpdateURLs synchronizes the manager's node pool with the provided list of URLs.
func (m *Manager) UpdateURLs(urls []string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	existing := make(map[string]*Node)
	for _, n := range m.nodes {
		existing[n.URL] = n
	}

	var newNodes []*Node
	for _, url := range urls {
		if n, ok := existing[url]; ok {
			newNodes = append(newNodes, n)
		} else {
			newNodes = append(newNodes, &Node{URL: url})
		}
	}
	m.nodes = newNodes
}

// GetStatus returns a snapshot of the current state of all nodes.
func (m *Manager) GetStatus() []*Node {
	m.mu.RLock()
	defer m.mu.RUnlock()

	copy := make([]*Node, len(m.nodes))
	for i, n := range m.nodes {
		nCopy := *n
		copy[i] = &nCopy
	}
	return copy
}

func (m *Manager) getHealthyNode() (*Node, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	numNodes := len(m.nodes)
	if numNodes == 0 {
		return nil, fmt.Errorf("no STT nodes available")
	}

	for i := 0; i < numNodes; i++ {
		idx := atomic.AddUint32(&m.index, 1) - 1
		node := m.nodes[idx%uint32(numNodes)]
		if !node.Zombie {
			return node, nil
		}
	}
	return nil, fmt.Errorf("all STT nodes are unhealthy")
}

// Transcribe sends audio data to a healthy node and returns the transcribed text.
func (m *Manager) Transcribe(ctx context.Context, pcmData []byte, sampleRate int) (string, error) {
	if len(pcmData) == 0 {
		return "", fmt.Errorf("empty audio data")
	}

	durationSecs := float64(len(pcmData)) / float64(sampleRate*2)
	timeoutSecs := (durationSecs * 0.25) + 2.5
	timeoutDuration := time.Duration(timeoutSecs * float64(time.Second))

	wavData := AddWavHeader(pcmData, sampleRate)

	for attempt := 0; attempt < 3; attempt++ {
		node, err := m.getHealthyNode()
		if err != nil {
			return "", err
		}

		body := &bytes.Buffer{}
		writer := multipart.NewWriter(body)
		h := make(textproto.MIMEHeader)
		h.Set("Content-Disposition", `form-data; name="file"; filename="input.wav"`)
		h.Set("Content-Type", "audio/wav")
		part, _ := writer.CreatePart(h)
		part.Write(wavData)
		writer.Close()

		if m.onLog != nil {
			m.onLog(fmt.Sprintf("[STT] Sending audio to node: %s (Attempt %d)", node.URL, attempt+1))
		}

		start := time.Now()
		req, err := http.NewRequestWithContext(ctx, "POST", node.URL, body)
		if err != nil {
			return "", err
		}
		req.Header.Set("Content-Type", writer.FormDataContentType())

		resp, err := http.DefaultClient.Do(req)
		duration := time.Since(start)

		// Update metrics
		node.LastExecutionTime = duration
		ratio := duration.Seconds() / timeoutDuration.Seconds()
		const alpha = 0.2
		if node.RollingCutoffRatio == 0 {
			node.RollingCutoffRatio = ratio
		} else {
			node.RollingCutoffRatio = (ratio * alpha) + (node.RollingCutoffRatio * (1.0 - alpha))
		}

		node.TotalRequests++
		if err != nil || resp.StatusCode != http.StatusOK || duration > timeoutDuration {
			node.FailureCount++
			node.TotalFailures++

			statusStr := "N/A"
			if resp != nil {
				statusStr = resp.Status
				resp.Body.Close()
			}

			if node.FailureCount >= 5 {
				node.Zombie = true
				if m.onLog != nil {
					m.onLog(fmt.Sprintf("[STT] Node flagged: %s (Duration: %v, Cutoff Ratio: %.2f, Status: %s, Err: %v).", node.URL, duration, ratio, statusStr, err))
				}
			} else {
				if m.onLog != nil {
					m.onLog(fmt.Sprintf("[STT] Node attempt failed: %s (Duration: %v, Cutoff Ratio: %.2f, Status: %s, Err: %v). Failures: %d/5", node.URL, duration, ratio, statusStr, err, node.FailureCount))
				}
			}
			continue // Retry with another node
		}

		node.Zombie = false
		node.LastResponseTime = duration
		node.FailureCount = 0
		if m.onLog != nil {
			m.onLog(fmt.Sprintf("[STT] Node response: %s (Duration: %v, Cutoff Ratio: %.2f)", node.URL, duration.Truncate(time.Millisecond), ratio))
		}

		defer resp.Body.Close()
		var result struct {
			Text string `json:"text"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			return "", err
		}
		return result.Text, nil
	}

	return "", fmt.Errorf("transcription failed after multiple attempts")
}

// Filter determines if a transcription should be ignored as an artifact/hallucination.
func Filter(text string) (string, bool) {
	text = strings.TrimSpace(text)
	text = strings.ReplaceAll(text, "[BLANK_AUDIO]", "")
	text = strings.ReplaceAll(text, "[Audio]", "")
	text = strings.ReplaceAll(text, "(silence)", "")
	text = strings.TrimSpace(text)

	if text == "" || text == "." || text == "..." {
		return "", true
	}

	lower := strings.ToLower(text)
	cleaner := strings.Trim(lower, ".,!? ")

	// Common Whisper artifacts/hallucinations
	artifacts := []string{
		"thank you", "thank you.", "thank you for watching", "thanks for watching",
		"bye", "goodbye", "you", "please like and subscribe",
	}

	for _, a := range artifacts {
		if cleaner == a {
			return "", true
		}
	}

	// Handle bracketed/parenthesized annotations
	if (strings.HasPrefix(text, "[") && strings.HasSuffix(text, "]")) ||
		(strings.HasPrefix(text, "(") && strings.HasSuffix(text, ")")) {
		lowerText := strings.ToLower(text)
		if lowerText == "[pause]" || lowerText == "[resume]" || lowerText == "[request_sync]" || lowerText == "[whisper_status]" {
			return text, false // Pass through special markers
		}
		return "", true
	}

	if strings.Contains(lower, "thank you") && len(text) < 15 {
		return "", true
	}

	return text, false
}

// AddWavHeader wraps PCM data in a WAV container.
func AddWavHeader(pcmData []byte, sampleRate int) []byte {
	buf := new(bytes.Buffer)
	buf.Write([]byte("RIFF"))
	binary.Write(buf, binary.LittleEndian, uint32(36+len(pcmData)))
	buf.Write([]byte("WAVE"))
	buf.Write([]byte("fmt "))
	binary.Write(buf, binary.LittleEndian, uint32(16))
	binary.Write(buf, binary.LittleEndian, uint16(1)) // AudioFormat: PCM
	binary.Write(buf, binary.LittleEndian, uint16(1)) // NumChannels: Mono
	binary.Write(buf, binary.LittleEndian, uint32(sampleRate))
	binary.Write(buf, binary.LittleEndian, uint32(sampleRate*2))
	binary.Write(buf, binary.LittleEndian, uint16(2))
	binary.Write(buf, binary.LittleEndian, uint16(16))
	buf.Write([]byte("data"))
	binary.Write(buf, binary.LittleEndian, uint32(len(pcmData)))
	buf.Write(pcmData)
	return buf.Bytes()
}
