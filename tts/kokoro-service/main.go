package main

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"regexp"
	"strings"

	"github.com/gorilla/websocket"
	sherpa "github.com/k2-fsa/sherpa-onnx-go/sherpa_onnx"
	"io/ioutil"
	"sync"
	"os"
	"os/exec"
	"path/filepath"
	"math"
	"runtime"
	"runtime/debug"
)

var (
	mixerMutex sync.Mutex
	customMixes = make(map[string]string) // name -> slot_name
	slotToName  = make(map[string]string) // slot_name -> name
	
	// Feature flags
	trainerEnabled = true
)

var mixesFile = "mixes.json"

func init() {
	// Look for mixes.json in current dir or parent dir
	if _, err := os.Stat(mixesFile); os.IsNotExist(err) {
		if _, err := os.Stat("../" + mixesFile); err == nil {
			mixesFile = "../" + mixesFile
		}
	}

	// Load feature config
	featureCfgPath := "feature_config.json"
	if _, err := os.Stat(featureCfgPath); os.IsNotExist(err) {
		if _, err := os.Stat("../" + featureCfgPath); err == nil {
			featureCfgPath = "../" + featureCfgPath
		}
	}
	
	if data, err := os.ReadFile(featureCfgPath); err == nil {
		var cfg struct {
			TrainerEnabled bool `json:"trainer_enabled"`
		}
		if err := json.Unmarshal(data, &cfg); err == nil {
			trainerEnabled = cfg.TrainerEnabled
			log.Printf("Feature Config: trainer_enabled=%v", trainerEnabled)
		}
	}
}

func loadMixes() {
	data, err := ioutil.ReadFile(mixesFile)
	if err == nil {
		var m map[string]string
		if err := json.Unmarshal(data, &m); err == nil {
			customMixes = m
			for k, v := range m {
				slotToName[v] = k
			}
		}
	}
}

func saveMixes() {
	data, _ := json.MarshalIndent(customMixes, "", "  ")
	ioutil.WriteFile(mixesFile, data, 0644)
}

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

type SynthesisRequest struct {
	Text        string  `json:"text"`
	Model       string  `json:"model"`
	LengthScale float64 `json:"length_scale"`
	Annotated   bool    `json:"annotated"`
}

type MetadataFrame struct {
	Type          string  `json:"type"`
	Text          string  `json:"text"`
	AudioBytes    int     `json:"audio_bytes,omitempty"`
	SampleRate    int     `json:"sampleRate,omitempty"`
}

// Global TTS instances
var tts *sherpa.OfflineTts
var sandboxTts *sherpa.OfflineTts
var sandboxPath = "models/kokoro-multi-lang-v1_0/voices_sandbox.bin"

var kokoroVoices = []string{
	"af_alloy", "af_aoede", "af_bella", "af_heart", "af_jessica",
	"af_kore", "af_nicole", "af_nova", "af_river", "af_sarah",
	"af_sky", "am_adam", "am_echo", "am_eric", "am_fenrir",
	"am_liam", "am_michael", "am_onyx", "am_puck", "am_santa",
	"bf_alice", "bf_emma", "bf_isabella", "bf_lily", "bm_daniel",
	"bm_fable", "bm_george", "bm_lewis", "ef_dora", "em_alex",
	"ff_siwis", "hf_alpha", "hf_beta", "hm_omega", "hm_psi",
	"if_sara", "im_nicola", "jf_alpha", "jf_gongitsune", "jf_nezumi",
	"jf_tebukuro", "jm_kumo", "pf_dora", "pm_alex", "pm_santa",
	"zf_xiaobei", "zf_xiaoni", "zf_xiaoxiao", "zf_xiaoyi", "zm_yunjian",
	"zm_yunxi", "zm_yunxia", "zm_yunyang",
}

const defaultVoice = "bf_emma"

func getVoiceID(name string) int {
	if name == "" {
		name = defaultVoice
	}
	// Check custom names first
	if slot, ok := customMixes[name]; ok {
		name = slot
	}
	for i, v := range kokoroVoices {
		if v == name {
			return i
		}
	}
	return getVoiceID(defaultVoice)
}

var globalConfig sherpa.OfflineTtsConfig

func reloadEngines() {
	mixerMutex.Lock()
	defer mixerMutex.Unlock()

	log.Println("Refreshing TTS engines...")
	
	// Delete old main engine
	if tts != nil {
		sherpa.DeleteOfflineTts(tts)
	}
	
	// Re-init main engine
	tts = sherpa.NewOfflineTts(&globalConfig)
	
	// Delete old sandbox engine
	if sandboxTts != nil {
		sherpa.DeleteOfflineTts(sandboxTts)
	}
	
	// Re-init sandbox engine
	sbCfg := globalConfig
	sbCfg.Model.Kokoro.Voices = sandboxPath
	numCores := int(runtime.NumCPU() / 2)
	if numCores < 1 { numCores = 1 }
	sbCfg.Model.NumThreads = numCores
	sbCfg.Model.Debug = 0
	sbCfg.Model.Provider = "cpu" // Force CPU for sandbox to save VRAM
	sandboxTts = sherpa.NewOfflineTts(&sbCfg)
	
	// Force Garbage Collection to reclaim memory from deleted engines
	runtime.GC()
	debug.FreeOSMemory()
	
	log.Println("TTS engines refreshed.")
}

func main() {
	provider := flag.String("provider", "cuda", "Hardware acceleration provider (cuda, vulkan, openvino, cpu)")
	enableSandbox := flag.Bool("sandbox", false, "Enable the sandbox TTS mixer engine (off by default)")
	flag.Parse()

	log.Printf("Initializing Sherpa-ONNX Kokoro TTS Backend with provider: %s", *provider)

	globalConfig = sherpa.OfflineTtsConfig{
		Model: sherpa.OfflineTtsModelConfig{
			Kokoro: sherpa.OfflineTtsKokoroModelConfig{
				Model:   "models/kokoro-multi-lang-v1_0/model.onnx",
				Voices:  "models/kokoro-multi-lang-v1_0/voices.bin",
				Tokens:  "models/kokoro-multi-lang-v1_0/tokens.txt",
				DataDir: "models/kokoro-multi-lang-v1_0/espeak-ng-data",
				DictDir: "",
				Lexicon: "models/kokoro-multi-lang-v1_0/lexicon-us-en.txt,models/kokoro-multi-lang-v1_0/lexicon-zh.txt",
			},
			NumThreads: int(runtime.NumCPU() / 2),
			Debug:      1,
			Provider:   *provider,
		},
	}

	ttsEngine := sherpa.NewOfflineTts(&globalConfig)
	if ttsEngine == nil {
		log.Fatalf("Failed to create Sherpa OfflineTts engine. Check model paths.")
	}
	defer func() {
		mixerMutex.Lock()
		defer mixerMutex.Unlock()
		if tts != nil {
			sherpa.DeleteOfflineTts(tts)
			tts = nil
		}
		runtime.GC()
		debug.FreeOSMemory()
	}()
	tts = ttsEngine
	
	log.Println("Sherpa-ONNX TTS initialized successfully.")

	// Pre-warm Sandbox Engine if enabled
	if *enableSandbox {
		// Create initial sandbox file from master
		masterData, err := ioutil.ReadFile("models/kokoro-multi-lang-v1_0/voices.bin")
		if err == nil {
			ioutil.WriteFile(sandboxPath, masterData, 0644)
		}
		sbCfg := globalConfig
		sbCfg.Model.Kokoro.Voices = sandboxPath
		numCores := int(runtime.NumCPU() / 2)
		if numCores < 1 { numCores = 1 }
		sbCfg.Model.NumThreads = numCores
		sbCfg.Model.Provider = "cpu" // Force CPU for sandbox to save VRAM
		
		sTts := sherpa.NewOfflineTts(&sbCfg)
		if sTts != nil {
			sandboxTts = sTts
			log.Println("Sandbox TTS Engine pre-warmed (CPU).")
		} else {
			log.Println("Warning: Failed to pre-warm Sandbox TTS Engine.")
		}
	} else {
		log.Println("Sandbox TTS Mixer is disabled (use --sandbox to enable).")
	}

	defer func() {
		mixerMutex.Lock()
		defer mixerMutex.Unlock()
		if tts != nil { 
			sherpa.DeleteOfflineTts(tts) 
			tts = nil
		}
		if sandboxTts != nil { 
			sherpa.DeleteOfflineTts(sandboxTts) 
			sandboxTts = nil
		}
		runtime.GC()
		debug.FreeOSMemory()
	}()

	http.HandleFunc("/stream", streamHandler)
	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"uptime_seconds":     100,
			"memory_usage_bytes": 0,
			"goroutines":         2,
		})
	})
	http.HandleFunc("/models", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		var displayNames []string
		for _, v := range kokoroVoices {
			if name, ok := slotToName[v]; ok {
				displayNames = append(displayNames, name)
			} else {
				displayNames = append(displayNames, v)
			}
		}
		json.NewEncoder(w).Encode(displayNames)
	})
	http.HandleFunc("/status", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		active := defaultVoice
		if name, ok := slotToName[active]; ok {
			active = name
		}
		json.NewEncoder(w).Encode(map[string]interface{}{
			"service_type":              "kokoro",
			"active_model":              active,
			"cached_models":             []string{"kokoro-multi-lang-v1_0"},
			"total_cache_size_estimate": 0,
		})
	})
	
	http.HandleFunc("/api/mix/preview", handleMixPreview)
	http.HandleFunc("/api/mix/save", handleMixSave)
	
	http.HandleFunc("/api/trainer/process", func(w http.ResponseWriter, r *http.Request) {
		if !trainerEnabled {
			http.Error(w, "Experimental Voice Mapper is disabled in this build.", http.StatusNotImplemented)
			return
		}
		handleTrainerProcess(w, r)
	})
	
	http.HandleFunc("/api/trainer/save", func(w http.ResponseWriter, r *http.Request) {
		if !trainerEnabled {
			http.Error(w, "Experimental Voice Mapper is disabled in this build.", http.StatusNotImplemented)
			return
		}
		handleTrainerSave(w, r)
	})

	http.HandleFunc("/api/config", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"trainer_enabled": trainerEnabled,
		})
	})
	
	http.Handle("/", http.FileServer(http.Dir("public")))

	// Initialize Mixer
	loadMixes()
	if _, err := LoadAllVoices("models/kokoro-multi-lang-v1_0/voices.bin"); err != nil {
		log.Printf("Warning: Failed to load voices into mixer cache: %v", err)
	}

	// Initialize Overrides
	initOverrides()

	log.Println("Starting kokoro-service on :4411...")
	if err := http.ListenAndServe(":4411", nil); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}

func handleTrainerProcess(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" { return }
	
	err := r.ParseMultipartForm(50 << 20) // 50MB
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}

	tempDir, _ := os.MkdirTemp("research/voice-training/temp", "train_*")
	defer os.RemoveAll(tempDir)

	var wavPaths []string
	files := r.MultipartForm.File["audio"]
	for _, f := range files {
		src, _ := f.Open()
		dstPath := filepath.Join(tempDir, f.Filename)
		dst, _ := os.Create(dstPath)
		ioutil.ReadAll(src) // Consume
		src.Seek(0, 0)
		data, _ := ioutil.ReadAll(src)
		os.WriteFile(dstPath, data, 0644)
		dst.Close()
		src.Close()
		wavPaths = append(wavPaths, dstPath)
	}

	iterations := r.FormValue("iterations")
	if iterations == "" { iterations = "5" }
	seed := r.FormValue("seed")
	if seed == "" { seed = "auto" }
	
	// Resolve seed name (e.g. "bm_jarvis" -> "zm_yunyang")
	resolvedSeed := seed
	if seed != "auto" {
		if slotName, ok := customMixes[seed]; ok {
			resolvedSeed = slotName
		}
	}

	outputBin := filepath.Join(tempDir, "result.bin")
	
	// Actually, let's just use absolute paths for the command
	absPython, _ := filepath.Abs("../../research/voice-training/.venv/bin/python3")
	absScript, _ := filepath.Abs("../../research/voice-training/scripts/voice_mapper.py")
	absOutput, _ := filepath.Abs(outputBin)
	
	var absWavs []string
	for _, p := range wavPaths {
		ap, _ := filepath.Abs(p)
		absWavs = append(absWavs, ap)
	}

	// Set up streaming response
	f, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming not supported", 500)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Transfer-Encoding", "chunked")

	args := append([]string{absScript}, absWavs...)
	args = append(args, "--output", absOutput, "--iterations", iterations, "--seed", resolvedSeed)
	
	trainerCmd := exec.Command(absPython, args...)
	trainerCmd.Env = append(os.Environ(), "PYTHONUNBUFFERED=1")
	
	stdout, _ := trainerCmd.StdoutPipe()
	stderr, _ := trainerCmd.StderrPipe()
	
	if err := trainerCmd.Start(); err != nil {
		fmt.Fprintf(w, "Error starting trainer: %v\n", err)
		return
	}

	// Stream output
	scanner := bufio.NewScanner(io.MultiReader(stdout, stderr))
	for scanner.Scan() {
		fmt.Fprintf(w, "%s\n", scanner.Text())
		f.Flush()
	}

	if err := trainerCmd.Wait(); err != nil {
		fmt.Fprintf(w, "Trainer finished with error: %v\n", err)
		return
	}

	// Load result and update sandbox index 0
	resultData, err := os.ReadFile(absOutput)
	if err == nil {
		vd := ParseRawVoice(resultData)
		mixerMutex.Lock()
		CreateSandbox("models/kokoro-multi-lang-v1_0/voices.bin", sandboxPath, 0, vd)
		
		// RE-INIT SANDBOX ENGINE
		if sandboxTts != nil { 
			sherpa.DeleteOfflineTts(sandboxTts) 
			sandboxTts = nil
		}
		sbCfg := globalConfig
		sbCfg.Model.Kokoro.Voices = sandboxPath
		numCores := int(runtime.NumCPU() / 2)
		if numCores < 1 { numCores = 1 }
		sbCfg.Model.NumThreads = numCores
		sbCfg.Model.Debug = 0
		sbCfg.Model.Provider = "cpu" // Force CPU for sandbox
		sandboxTts = sherpa.NewOfflineTts(&sbCfg)
		
		// Reclaim memory
		runtime.GC()
		debug.FreeOSMemory()
		mixerMutex.Unlock()
		
		// Send final magic line with JSON
		fmt.Fprintf(w, "RESULT:{\"status\":\"ok\",\"voice_id\":\"sandbox_0\"}\n")
		f.Flush()
	} else {
		fmt.Fprintf(w, "Error reading result: %v\n", err)
	}
}

func handleTrainerSave(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" { return }
	var req struct {
		Name string `json:"name"`
		Slot string `json:"slot"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}

	// Load currently trained voice from sandbox slot 0
	data, err := os.ReadFile(sandboxPath)
	if err != nil {
		http.Error(w, "Sandbox not found", 500)
		return
	}

	// Extract slot 0
	vd := ParseRawVoice(data[0:voiceSize])
	
	// Save to target slot
	slotID := getVoiceID(req.Slot)
	mixerMutex.Lock()
	if err := ApplySave("models/kokoro-multi-lang-v1_0/voices.bin", slotID, vd); err != nil {
		mixerMutex.Unlock()
		http.Error(w, err.Error(), 500)
		return
	}

	// Update names
	customMixes[req.Name] = req.Slot
	slotToName[req.Slot] = req.Name
	saveMixes()

	reloadEngines()
	mixerMutex.Unlock()

	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func handleMixRequest(conn *websocket.Conn, raw map[string]interface{}) {
	text, _ := raw["text"].(string)
	weightsRaw, _ := raw["weights"].(map[string]interface{})
	method, _ := raw["method"].(string)
	binWeightsRaw, _ := raw["bin_weights"].([]interface{})
	
	if sandboxTts == nil {
		log.Println("Sandbox Engine not available")
		return
	}

	mixerMutex.Lock()
	defer mixerMutex.Unlock()

	var indices []int
	var weights []float32
	for name, w := range weightsRaw {
		indices = append(indices, getVoiceID(name))
		weights = append(weights, float32(w.(float64)))
	}

	var mixed VoiceData
	if method == "eq" && len(binWeightsRaw) > 0 {
		var bw [][]float32
		for _, row := range binWeightsRaw {
			var r []float32
			for _, val := range row.([]interface{}) {
				r = append(r, float32(val.(float64)))
			}
			bw = append(bw, r)
		}
		mixed = MixEQ(indices, bw)
	} else if method == "slerp" {
		mixed = MixSLERP(indices, weights)
	} else {
		mixed = MixLinear(indices, weights)
	}

	// Update Sandbox
	if err := CreateSandbox("models/kokoro-multi-lang-v1_0/voices.bin", sandboxPath, 0, mixed); err != nil {
		log.Printf("Sandbox write error: %v", err)
		return
	}

	// RE-INIT SANDBOX ENGINE TO PICK UP NEW EMBEDDING
	if sandboxTts != nil {
		sherpa.DeleteOfflineTts(sandboxTts)
	}
	sbCfg := globalConfig
	sbCfg.Model.Kokoro.Voices = sandboxPath
	numCores := int(runtime.NumCPU() / 2)
	if numCores < 1 { numCores = 1 }
	sbCfg.Model.NumThreads = numCores
	sbCfg.Model.Debug = 0
	sandboxTts = sherpa.NewOfflineTts(&sbCfg)

	if sandboxTts == nil {
		log.Println("Failed to re-init sandbox engine")
		return
	}

	// Generate & Stream
	rate := sandboxTts.SampleRate()
	startFrame, _ := json.Marshal(MetadataFrame{ Type: "start", SampleRate: rate })
	conn.WriteMessage(websocket.TextMessage, startFrame)

	marked, infRepl, _ := ApplyPreSplit(text)
	text = infRepl.Replace(marked)
	text = ApplyPostSplit(text)
	audio := sandboxTts.Generate(text, 0, 1.0)
	if audio != nil && len(audio.Samples) > 0 {
		pcmBytes := float32ToInt16PCM(audio.Samples)
		meta, _ := json.Marshal(MetadataFrame{
			Type: "text", Text: text, AudioBytes: len(pcmBytes), SampleRate: rate,
		})
		conn.WriteMessage(websocket.TextMessage, meta)
		conn.WriteMessage(websocket.BinaryMessage, pcmBytes)
	}

	endMeta, _ := json.Marshal(MetadataFrame{ Type: "end" })
	conn.WriteMessage(websocket.TextMessage, endMeta)
}

func streamHandler(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("WS Upgrade Error: %v", err)
		return
	}
	defer conn.Close()

	for {
		messageType, p, err := conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
				log.Printf("Read error: %v", err)
			}
			return
		}

		if messageType != websocket.TextMessage {
			continue
		}

		var pRaw map[string]interface{}
		if err := json.Unmarshal(p, &pRaw); err != nil {
			log.Printf("Invalid JSON payload: %v", err)
			continue
		}

		msgType, _ := pRaw["type"].(string)
		
		if msgType == "refine_preview" {
			text, _ := pRaw["text"].(string)
			binPath, _ := pRaw["bin_path"].(string)
			
			// Load raw binary floats
			data, err := os.ReadFile(binPath)
			if err == nil {
				var vd VoiceData
				for i := 0; i < len(data); i += 4 {
					bits := binary.LittleEndian.Uint32(data[i : i+4])
					vd = append(vd, math.Float32frombits(bits))
				}
				
				mixerMutex.Lock()
				// Update Sandbox Slot 0
				CreateSandbox("models/kokoro-multi-lang-v1_0/voices.bin", sandboxPath, 0, vd)
				
				// RE-INIT SANDBOX
				if sandboxTts != nil { 
					sherpa.DeleteOfflineTts(sandboxTts)
					sandboxTts = nil
				}
				sbCfg := globalConfig
				sbCfg.Model.Kokoro.Voices = sandboxPath
				numCores := int(runtime.NumCPU() / 2)
				if numCores < 1 { numCores = 1 }
				sbCfg.Model.NumThreads = numCores
				sbCfg.Model.Debug = 0
				sbCfg.Model.Provider = "cpu" // Force CPU for sandbox
				sandboxTts = sherpa.NewOfflineTts(&sbCfg)
				
				// Reclaim memory
				runtime.GC()
				debug.FreeOSMemory()
				
				if sandboxTts != nil {
					rate := sandboxTts.SampleRate()
					startFrame, _ := json.Marshal(MetadataFrame{ Type: "start", SampleRate: rate })
					conn.WriteMessage(websocket.TextMessage, startFrame)

					audio := sandboxTts.Generate(text, 0, 1.0)
					if audio != nil && len(audio.Samples) > 0 {
						pcmBytes := float32ToInt16PCM(audio.Samples)
						meta, _ := json.Marshal(MetadataFrame{
							Type: "text", Text: text, AudioBytes: len(pcmBytes), SampleRate: rate,
						})
						conn.WriteMessage(websocket.TextMessage, meta)
						conn.WriteMessage(websocket.BinaryMessage, pcmBytes)
					}
				}
				mixerMutex.Unlock()
			}
			endMeta, _ := json.Marshal(MetadataFrame{ Type: "end" })
			conn.WriteMessage(websocket.TextMessage, endMeta)
			continue
		}

		if msgType == "mix_request" {
			handleMixRequest(conn, pRaw)
			continue
		}

		var req SynthesisRequest
		if err := json.Unmarshal(p, &req); err != nil {
			log.Printf("Invalid JSON payload: %v", err)
			continue
		}

		var engine = tts
		sid := getVoiceID(req.Model)
		
		mixerMutex.Lock()
		if req.Model == "sandbox_0" {
			engine = sandboxTts
			sid = 0
		}
		
		if engine == nil {
			mixerMutex.Unlock()
			log.Printf("Warning: Engine for %s is not initialized", req.Model)
			continue
		}
		
		log.Printf("Received Synthesis Request: text len: %d, voice: %s (SID: %d)", len(req.Text), req.Model, sid)
		rate := engine.SampleRate()
		mixerMutex.Unlock()

		// Send initial Start Frame to sync sample rate
		startFrame, _ := json.Marshal(MetadataFrame{
			Type:       "start",
			SampleRate: rate,
		})
		conn.WriteMessage(websocket.TextMessage, startFrame)

		marked, infRepl, dispRepl := ApplyPreSplit(req.Text)
		sentences := splitIntoSentences(marked)
		
		for _, s := range sentences {
			if s == "" {
				continue
			}

			displayS := strings.TrimSpace(dispRepl.Replace(s))
			s = infRepl.Replace(s)
			s = ApplyPostSplit(s)
			log.Printf("Synthesizing sentence: %s [SID: %d]", s, sid)
			
			// Adjust speed using req.LengthScale
			speed := 1.0
			if req.LengthScale > 0 {
				speed = 1.0 / req.LengthScale 
			}

			// Generate audio - PROTECT WITH MUTEX to avoid race with reload
			mixerMutex.Lock()
			audio := engine.Generate(s, sid, float32(speed))
			mixerMutex.Unlock()
			
			if audio == nil || len(audio.Samples) == 0 {
				log.Println("Warning: empty audio generated")
				continue
			}

			// Convert float32 samples to int16 PCM
			pcmBytes := float32ToInt16PCM(audio.Samples)

			// Send Metadata
			meta, _ := json.Marshal(MetadataFrame{
				Type:       "text",
				Text:       displayS,
				AudioBytes: len(pcmBytes),
				SampleRate: rate,
			})
			if err := conn.WriteMessage(websocket.TextMessage, meta); err != nil {
				return
			}

			// Send Audio Chunk
			if err := conn.WriteMessage(websocket.BinaryMessage, pcmBytes); err != nil {
				return
			}
		}

		// Send End Frame
		endMeta, _ := json.Marshal(MetadataFrame{
			Type: "end",
		})
		conn.WriteMessage(websocket.TextMessage, endMeta)
	}
}

func handleMixPreview(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" { return }
	var req struct {
		Text    string             `json:"text"`
		Weights map[string]float32 `json:"weights"`
		Method  string             `json:"method"`
		BinWeights [][]float32      `json:"bin_weights"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}

	mixerMutex.Lock()
	defer mixerMutex.Unlock()

	var indices []int
	var weights []float32
	for name, weight := range req.Weights {
		indices = append(indices, getVoiceID(name))
		weights = append(weights, weight)
	}

	var mixed VoiceData
	if req.Method == "eq" && len(req.BinWeights) > 0 {
		mixed = MixEQ(indices, req.BinWeights)
	} else if req.Method == "slerp" {
		mixed = MixSLERP(indices, weights)
	} else {
		mixed = MixLinear(indices, weights)
	}

	if err := CreateSandbox("models/kokoro-multi-lang-v1_0/voices.bin", sandboxPath, 0, mixed); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}

	// RE-INIT SANDBOX ENGINE TO PICK UP NEW EMBEDDING
	if sandboxTts != nil {
		sherpa.DeleteOfflineTts(sandboxTts)
	}
	sbCfg := globalConfig
	sbCfg.Model.Kokoro.Voices = sandboxPath
	numCores := int(runtime.NumCPU() / 2)
	if numCores < 1 { numCores = 1 }
	sbCfg.Model.NumThreads = numCores
	sbCfg.Model.Debug = 0
	sandboxTts = sherpa.NewOfflineTts(&sbCfg)

	if sandboxTts == nil {
		http.Error(w, "Sandbox engine not initialized", 500)
		return
	}

	marked, infRepl, _ := ApplyPreSplit(req.Text)
	text := infRepl.Replace(marked)
	text = ApplyPostSplit(text)
	audio := sandboxTts.Generate(text, 0, 1.0)
	if audio == nil {
		http.Error(w, "Failed to generate preview", 500)
		return
	}

	pcmBytes := float32ToInt16PCM(audio.Samples)
	
	// Create WAV directly
	wav := createWav(pcmBytes, int(24000))
	w.Header().Set("Content-Type", "audio/wav")
	w.Write(wav)
}

func handleMixSave(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" { return }
	var req struct {
		Name    string             `json:"name"`
		Slot    string             `json:"slot"`
		Weights map[string]float32 `json:"weights"`
		Method  string             `json:"method"`
		BinWeights [][]float32      `json:"bin_weights"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}

	var indices []int
	var weights []float32
	for name, weight := range req.Weights {
		indices = append(indices, getVoiceID(name))
		weights = append(weights, weight)
	}

	var mixed VoiceData
	if req.Method == "eq" && len(req.BinWeights) > 0 {
		mixed = MixEQ(indices, req.BinWeights)
	} else if req.Method == "slerp" {
		mixed = MixSLERP(indices, weights)
	} else {
		mixed = MixLinear(indices, weights)
	}

	slotID := getVoiceID(req.Slot)
	if err := ApplySave("models/kokoro-multi-lang-v1_0/voices.bin", slotID, mixed); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}

	// Update names
	customMixes[req.Name] = req.Slot
	slotToName[req.Slot] = req.Name
	saveMixes()

	// RELOAD ENGINES to apply changes to sampler
	reloadEngines()

	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func createWav(pcm []byte, rate int) []byte {
	size := len(pcm)
	header := make([]byte, 44)
	copy(header[0:4], "RIFF")
	binary.LittleEndian.PutUint32(header[4:8], uint32(36+size))
	copy(header[8:12], "WAVE")
	copy(header[12:16], "fmt ")
	binary.LittleEndian.PutUint32(header[16:20], 16)
	binary.LittleEndian.PutUint16(header[20:22], 1)
	binary.LittleEndian.PutUint16(header[22:24], 1)
	binary.LittleEndian.PutUint32(header[24:28], uint32(rate))
	binary.LittleEndian.PutUint32(header[28:32], uint32(rate*2))
	binary.LittleEndian.PutUint16(header[32:34], 2)
	binary.LittleEndian.PutUint16(header[34:36], 16)
	copy(header[36:40], "data")
	binary.LittleEndian.PutUint32(header[40:44], uint32(size))
	return append(header, pcm...)
}

func float32ToInt16PCM(samples []float32) []byte {
	buf := new(bytes.Buffer)
	for _, f := range samples {
		if f < -1.0 { f = -1.0 }
		if f > 1.0 { f = 1.0 }
		binary.Write(buf, binary.LittleEndian, int16(f*32767.0))
	}
	return buf.Bytes()
}

func splitIntoSentences(text string) []string {
	re := regexp.MustCompile(`[^.!?]+[.!?]*`)
	raw := re.FindAllString(text, -1)
	if len(raw) == 0 { return []string{text} }
	return raw
}
