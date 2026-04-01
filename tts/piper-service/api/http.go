package api

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
)

type TTSRequest struct {
	Text      string `json:"text"`
	Annotated bool   `json:"annotated"`
}

type ModelRequest struct {
	Model string `json:"model"`
}

func (api *API) handleTTS(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		sendError(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req TTSRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendError(w, "Invalid JSON payload", http.StatusBadRequest)
		return
	}

	if req.Text == "" {
		sendError(w, "Text cannot be empty", http.StatusBadRequest)
		return
	}

	audioData, err := api.Manager.Synthesize(req.Text)
	if err != nil {
		sendError(w, fmt.Sprintf("Synthesis failed: %v", err), http.StatusInternalServerError)
		return
	}

	// For simple HTTP response, return raw PCM or a minimum WAV header.
	// Since client expects raw rawPCM audio or can decode it, 
	// we will stream it as application/octet-stream (raw PCM 16kHz Mono 16-bit by default).
	// To make it standard, let's wrap it in a WAV header.
	writeWavHeader(w, len(audioData), api.Manager.GetSampleRate())
	
	// Convert int16 to bytes for writing
	buf := new(bytes.Buffer)
	for _, sample := range audioData {
		binary.Write(buf, binary.LittleEndian, sample)
	}

	w.Header().Set("Content-Type", "audio/wav")
	w.Write(buf.Bytes())
}

func writeWavHeader(w http.ResponseWriter, numSamples, sampleRate int) {
	// Standard WAV header for dynamic Hz, 16-bit, Mono
	// Not fully robust, but suitable for simple tests.
	// We'll just stream raw data over application/octet-stream for WS currently,
	// but HTTP clients might expect a WAV header.
	dataSize := int32(numSamples * 2)
	header := make([]byte, 44)
	sr := uint32(sampleRate)

	copy(header[0:], "RIFF")
	binary.LittleEndian.PutUint32(header[4:], uint32(36+dataSize))
	copy(header[8:], "WAVE")
	copy(header[12:], "fmt ")
	binary.LittleEndian.PutUint32(header[16:], 16)   // Subchunk1Size
	binary.LittleEndian.PutUint16(header[20:], 1)    // AudioFormat (PCM)
	binary.LittleEndian.PutUint16(header[22:], 1)    // NumChannels
	binary.LittleEndian.PutUint32(header[24:], sr)   // SampleRate
	binary.LittleEndian.PutUint32(header[28:], sr*2) // ByteRate
	binary.LittleEndian.PutUint16(header[32:], 2)    // BlockAlign
	binary.LittleEndian.PutUint16(header[34:], 16)   // BitsPerSample
	copy(header[36:], "data")
	binary.LittleEndian.PutUint32(header[40:], uint32(dataSize))
	w.Write(header)
}

func (api *API) handleModels(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		entries, err := os.ReadDir(api.ModelDir)
		if err != nil {
			sendError(w, "Failed to read models directory", http.StatusInternalServerError)
			return
		}

		models := make([]string, 0)
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".onnx") {
				models = append(models, e.Name())
			}
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(models)
		return
	}

	if r.Method == http.MethodPost {
		// Used to switch models (e.g., alias for /models/current)
		var req ModelRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			sendError(w, "Invalid json", http.StatusBadRequest)
			return
		}

		if err := api.Manager.LoadModel(req.Model); err != nil {
			sendError(w, err.Error(), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "success"})
		return
	}

	sendError(w, "Method not allowed", http.StatusMethodNotAllowed)
}

func (api *API) handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		sendError(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	active, cache, totalSize := api.Manager.GetDetailedStatus()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"active_model":                active,
		"cached_models":               cache,
		"total_cache_size_estimate":   totalSize,
	})
}
