package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"log"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"os"
	"path/filepath"
	//"net/url"

	"github.com/gorilla/websocket"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
)

const (
	whisperURL    = "http://localhost:8081/inference"
	piperBin      = "./piper/piper"                     // Path to the piper executable
	piperModel    = "./piper/en_GB-cori-medium.onnx" // Path to the voice model
	//en_GB-cori-medium.onnx
	//en_GB-southern_english_female-low.onnx
	sampleRate    = 16000
	
	// ollamaURL     = "http://localhost:11434/api/generate"
	// ollamaChatURL = "http://localhost:11434/api/chat"
	// ollamaModel   = "gemma3n:e2b" // Change this if you pulled a different model!

	ollamaURL     = "http://192.168.1.21:11434/api/generate"
	ollamaChatURL = "http://192.168.1.21:11434/api/chat"
	ollamaModel   = "llama3.2:3b" // Change this if you pulled a different model!
	// mistral-nemo:12b
	// gemma3n:e2b
	// gemma3:1b-it-qat
	// gemma3:4b-it-qat
)

type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ClientSession struct {
	ClientID string        `json:"-"`
	History  []ChatMessage `json:"history"`
	Archive  []ChatMessage `json:"archive"`
	Summary  string        `json:"summary"`
	Mutex    sync.Mutex    `json:"-"`
	ConnMutex sync.Mutex   `json:"-"` // Dedicated lock for WebSocket writes
}

func safeWrite(ws *websocket.Conn, session *ClientSession, msgType int, data []byte) error {
	session.ConnMutex.Lock()
	defer session.ConnMutex.Unlock()
	return ws.WriteMessage(msgType, data)
}

func getContextPath(clientID string) string {
	safeID := strings.ReplaceAll(clientID, "/", "")
	safeID = strings.ReplaceAll(safeID, "\\", "")
	safeID = strings.ReplaceAll(safeID, "..", "")
	return filepath.Join(".", "context", safeID+".json")
}

func loadSession(clientID string) *ClientSession {
	session := &ClientSession{ClientID: clientID}
	data, err := os.ReadFile(getContextPath(clientID))
	if err == nil {
		json.Unmarshal(data, session)
	}
	return session
}

func saveSession(session *ClientSession) {
	session.Mutex.Lock()
	defer session.Mutex.Unlock()
	if data, err := json.MarshalIndent(session, "", "  "); err == nil {
		os.MkdirAll(filepath.Join(".", "context"), 0755)
		os.WriteFile(getContextPath(session.ClientID), data, 0644)
	}
}

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

func handleConnections(w http.ResponseWriter, r *http.Request) {
	ws, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Println("Upgrade error:", err)
		return
	}
	defer ws.Close()

	clientID := r.URL.Query().Get("client_id")
	if clientID == "" {
		clientID = "default"
	}
	fmt.Printf("Client connected: %s\n", clientID)
	session := loadSession(clientID)
	var activeCancel context.CancelFunc

	for {
		messageType, p, err := ws.ReadMessage()
		if err != nil {
			return
		}

		// Handle incoming text (TTS Request)
		if messageType == websocket.TextMessage {
			text := string(p)

			if text == "[INTERRUPT]" {
				if activeCancel != nil {
					activeCancel()
					activeCancel = nil
				}
				continue
			}

			go func(t string) {
				audioBytes, err := queryTTS(t)
				if err != nil {
					log.Println("TTS error:", err)
					return
				}
				safeWrite(ws, session, websocket.BinaryMessage, audioBytes)
			}(text)
		}

		// Handle incoming audio (STT Request)
		if messageType == websocket.BinaryMessage {
			// Minimum byte length check (~0.5 seconds of 16-bit PCM is 16000 bytes)
			if len(p) < 16000 {
				continue
			}

			// Process the complete phrase sent by the client
			go func(audio []byte) {
				text, err := queryWhisper(audio)
				if err != nil {
					log.Println("Whisper error:", err)
					return
				}
				
				// Clean up Whisper's special tokens and hallucinations
				text = strings.ReplaceAll(text, "[BLANK_AUDIO]", "")
				text = strings.TrimSpace(text)

				if text != "" {
					session.Mutex.Lock()
					if activeCancel != nil {
						activeCancel()
					}
					ctx, cancel := context.WithCancel(context.Background())
					activeCancel = cancel
					session.Mutex.Unlock()

					// 1. Send the user's transcription to the browser
					if err := safeWrite(ws, session, websocket.TextMessage, []byte(text)); err != nil {
						log.Println("Write error:", err)
					}

					// 2. Stream Ollama and TTS responses to the browser
					safeWrite(ws, session, websocket.TextMessage, []byte("[AI_START]"))
					if err := streamOllamaAndTTS(ctx, text, ws, session); err != nil {
						log.Println("Ollama stream error:", err)
					}
					safeWrite(ws, session, websocket.TextMessage, []byte("[AI_END]"))
				} else {
					// Inform the frontend that the audio was ignored (e.g., background noise)
					safeWrite(ws, session, websocket.TextMessage, []byte("[IGNORED]"))
				}
			}(p)
		}
	}
}

func addWavHeader(pcmData []byte) []byte {
	buf := new(bytes.Buffer)
	// RIFF header
	buf.Write([]byte("RIFF"))
	binary.Write(buf, binary.LittleEndian, uint32(36+len(pcmData)))
	buf.Write([]byte("WAVE"))
	// fmt chunk
	buf.Write([]byte("fmt "))
	binary.Write(buf, binary.LittleEndian, uint32(16))
	binary.Write(buf, binary.LittleEndian, uint16(1))      // AudioFormat: PCM
	binary.Write(buf, binary.LittleEndian, uint16(1))      // NumChannels: Mono
	binary.Write(buf, binary.LittleEndian, uint32(16000))  // SampleRate: 16kHz
	binary.Write(buf, binary.LittleEndian, uint32(32000))  // ByteRate: SampleRate * NumChannels * BitsPerSample/8
	binary.Write(buf, binary.LittleEndian, uint16(2))      // BlockAlign: NumChannels * BitsPerSample/8
	binary.Write(buf, binary.LittleEndian, uint16(16))     // BitsPerSample: 16
	// data chunk
	buf.Write([]byte("data"))
	binary.Write(buf, binary.LittleEndian, uint32(len(pcmData)))
	buf.Write(pcmData)
	return buf.Bytes()
}

func queryWhisper(audioData []byte) (string, error) {
	if len(audioData) == 0 {
		return "", fmt.Errorf("empty audio data")
	}

	wavData := addWavHeader(audioData)
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)

	h := make(textproto.MIMEHeader)
	h.Set("Content-Disposition", `form-data; name="file"; filename="input.wav"`)
	h.Set("Content-Type", "audio/wav")
	part, _ := writer.CreatePart(h)
	part.Write(wavData)
	writer.Close()

	resp, err := http.Post(whisperURL, writer.FormDataContentType(), body)
	if err != nil {
		return "", err
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

func streamOllamaAndTTS(ctx context.Context, prompt string, ws *websocket.Conn, session *ClientSession) error {
	session.Mutex.Lock()
	sysContent := fmt.Sprintf("You are 'Alex', a highly capable, voice-based AI programming assistant. "+
		"You are built on a low-latency architecture: a Vanilla JS + WebAudio frontend, a Go WebSocket backend, Whisper STT for hearing, Ollama for thinking, and Piper TTS for speaking. "+
		"You are currently thinking using the %s model and speaking using the %s voice model. "+
		"Your output is sent directly to a Text-to-Speech engine. "+
		"You MUST strictly follow these rules: 1. NEVER use emojis. 2. NEVER use markdown formatting like asterisks. 3. Keep responses conversational, concise, and easy to listen to. 4. If explaining code, speak it out logically rather than printing raw syntax.",
		ollamaModel, piperModel)

	if session.Summary != "" {
		sysContent += "\n\nContext from earlier in the conversation: " + session.Summary
	}

	messages := []ChatMessage{
		{Role: "system", Content: sysContent},
	}
	messages = append(messages, session.History...)
	messages = append(messages, ChatMessage{Role: "user", Content: prompt})
	session.Mutex.Unlock()

	payload := map[string]interface{}{
		"model":    ollamaModel,
		"messages": messages,
		"stream":   true,
	}
	body, _ := json.Marshal(payload)

	req, err := http.NewRequestWithContext(ctx, "POST", ollamaChatURL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	decoder := json.NewDecoder(resp.Body)
	var sentence strings.Builder
	var fullResponse strings.Builder

	// Setup an asynchronous TTS Worker so Ollama tokens aren't blocked by audio generation
	ttsChan := make(chan string, 50)
	var ttsBusy atomic.Bool
	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		for text := range ttsChan {
			if ctx.Err() != nil {
				return // Abort if interrupted
			}
			ttsBusy.Store(true) // Mark Piper as busy
			if audioBytes, err := queryTTS(text); err == nil {
				if ctx.Err() == nil {
					safeWrite(ws, session, websocket.BinaryMessage, audioBytes)
				}
			}
			ttsBusy.Store(false) // Mark Piper as idle
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err() // Abort processing if interrupted
		default:
		}

		var result struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
			Done bool `json:"done"`
		}
		if err := decoder.Decode(&result); err != nil {
			break
		}

		content := result.Message.Content

		// Stream text token directly to UI
		safeWrite(ws, session, websocket.TextMessage, []byte(content))

		// Buffer token for TTS processing
		sentence.WriteString(content)
		fullResponse.WriteString(content)
		
		cleanChunk := strings.TrimSpace(sentence.String())
		isSentenceBoundary := strings.ContainsAny(content, ".!?:")
		isParagraphBoundary := strings.Contains(content, "\n")

		// DYNAMIC CHUNKING LOGIC:
		// 1. Always flush on paragraph end or generation end.
		// 2. If it's a sentence end, ONLY flush if Piper is idle (gives fast TTFA).
		// 3. Fallback: Flush if the buffer is getting too large (>250 chars).
		shouldFlush := result.Done || 
			isParagraphBoundary || 
			(isSentenceBoundary && !ttsBusy.Load()) || 
			(isSentenceBoundary && len(cleanChunk) > 250)

		if shouldFlush && len(cleanChunk) > 0 {
				// Strip markdown punctuation so Piper doesn't read it aloud
				ttsText := cleanChunk
				ttsText = strings.ReplaceAll(ttsText, "**", "")
				ttsText = strings.ReplaceAll(ttsText, "*", "")
				ttsText = strings.ReplaceAll(ttsText, "#", "")
				ttsText = strings.ReplaceAll(ttsText, "_", "")
				ttsText = strings.ReplaceAll(ttsText, "`", "")
				
			select {
			case ttsChan <- ttsText: // Ship to background worker
			case <-ctx.Done():
			}
			sentence.Reset()
		}

		if result.Done {
			break
		}
	}

	// Close the channel and wait for the TTS worker to finish its last chunk
	close(ttsChan)
	wg.Wait()

	// If not interrupted, save to history and trigger background summary if needed
	if ctx.Err() == nil {
		session.Mutex.Lock()
		session.History = append(session.History, ChatMessage{Role: "user", Content: prompt})
		session.History = append(session.History, ChatMessage{Role: "assistant", Content: fullResponse.String()})

		if len(session.History) > 14 { // 7 interactions
			toSummarize := make([]ChatMessage, 6) // Slice off oldest 3 interactions
			copy(toSummarize, session.History[:6])

			session.Archive = append(session.Archive, toSummarize...)
			session.History = session.History[6:]

			go generateSummaryAsync(toSummarize, session)
		}
		session.Mutex.Unlock()
		saveSession(session)
	}

	return nil
}

func generateSummaryAsync(messages []ChatMessage, session *ClientSession) {
	var transcript strings.Builder
	for _, msg := range messages {
		transcript.WriteString(fmt.Sprintf("%s: %s\n", msg.Role, msg.Content))
	}

	session.Mutex.Lock()
	prevSummary := session.Summary
	session.Mutex.Unlock()

	prompt := "Summarize the following conversation concisely. "
	if prevSummary != "" {
		prompt += "Incorporate this previous summary context: " + prevSummary + "\n\n"
	}
	prompt += "New conversation to add to summary:\n" + transcript.String() + "\n\nProvide ONLY the concise summary text."

	payload := map[string]interface{}{
		"model":  ollamaModel,
		"prompt": prompt,
		"stream": false, // Synchronous request for the background worker
	}
	body, _ := json.Marshal(payload)

	resp, err := http.Post(ollamaURL, "application/json", bytes.NewReader(body))
	if err != nil {
		log.Println("Summary error:", err)
		return
	}
	defer resp.Body.Close()

	var result struct {
		Response string `json:"response"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err == nil {
		session.Mutex.Lock()
		session.Summary = strings.TrimSpace(result.Response)
		log.Println("--- Context Memory Summarized for client ---")
		session.Mutex.Unlock()
		saveSession(session)
	}
}

func queryTTS(text string) ([]byte, error) {
	// Execute piper binary: -f - tells it to output the WAV file directly to standard output
	cmd := exec.Command(piperBin, "--model", piperModel, "-f", "-")
	
	// Feed our text into Piper's standard input
	cmd.Stdin = strings.NewReader(text)
	
	// Capture the raw WAV audio from Piper's standard output
	var out bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("piper execution failed: %v, stderr: %s", err, stderr.String())
	}

	return out.Bytes(), nil
}

func main() {
	http.HandleFunc("/ws", handleConnections)
	http.Handle("/", http.FileServer(http.Dir("./public")))

	fmt.Println("Server started on :3000")
	err := http.ListenAndServe(":3000", nil)
	if err != nil {
		log.Fatal("ListenAndServe: ", err)
	}
}
