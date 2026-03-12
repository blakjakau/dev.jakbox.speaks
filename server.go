package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"log"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"strconv"
	"os"
	"path/filepath"
	"time"
	//"net/url"

	"github.com/gorilla/websocket"
	"os/exec"
	"strings"
	"sync"
)

const (
	// whisperURL    = "http://192.168.1.21:8081/inference"
	whisperURL    = "http://localhost:8081/inference"

	piperBin      = "./piper/piper"                     // Path to the piper executable
	defaultVoice  = "en_GB-alba-medium.onnx"            // Default fallback
	sampleRate    = 16000
	
	ollamaURL     = "http://localhost:11434/api/generate"
	ollamaChatURL = "http://localhost:11434/api/chat"
	// ollamaModel   = "gemma3n:e2b" // Change this if you pulled a different model!

	// ollamaURL     = "http://192.168.1.21:11434/api/generate"
	// ollamaChatURL = "http://192.168.1.21:11434/api/chat"
	ollamaModel   = "gemma3:1b-it-qat" //""llama3.2:1b" // Change this if you pulled a different model!
	
	// llama3.2:1b
	// llama3.2:3b
	// gemma3:1b-it-qat
	// gemma3:4b-it-qat
	// mistral-nemo:12b
	// gemma3n:e2b
	// gemma3:270m
)

var (
	googleClientID     string
	googleClientSecret string
)

func init() {
	data, err := os.ReadFile("google-client-secret.json")
	if err == nil {
		var creds struct {
			ClientID     string `json:"client_id"`
			ClientSecret string `json:"client_secret"`
			Web          *struct {
				ClientID     string `json:"client_id"`
				ClientSecret string `json:"client_secret"`
			} `json:"web"`
		}
		if err := json.Unmarshal(data, &creds); err == nil {
			if creds.Web != nil {
				googleClientID, googleClientSecret = creds.Web.ClientID, creds.Web.ClientSecret
			} else {
				googleClientID, googleClientSecret = creds.ClientID, creds.ClientSecret
			}
			log.Println("Loaded Google OAuth credentials from google-client-secret.json")
		}
	}

	if googleClientID == "" {
		googleClientID = os.Getenv("GOOGLE_CLIENT_ID")
		googleClientSecret = os.Getenv("GOOGLE_CLIENT_SECRET")
		if googleClientID != "" && googleClientSecret != "" {
			log.Println("Loaded Google OAuth credentials from environment variables")
		}		
	}

	if googleClientID == "" || googleClientSecret == "" {
		log.Fatal("FATAL: Missing Google OAuth credentials. Provide google-client-secret.json or launch using:\n\nGOOGLE_CLIENT_ID=\"your_id\" GOOGLE_CLIENT_SECRET=\"your_secret\" go run server.go\n")
	}
}

type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ClientSession struct {
	ClientID string        `json:"-"`
	History  []ChatMessage `json:"history"`
	Archive  []ChatMessage `json:"archive"`
	Summary  string        `json:"summary"`
	UserName string        `json:"userName"`
	UserBio  string        `json:"userBio"`
	Provider string        `json:"provider"`
	APIKey   string        `json:"-"` // Ephemeral API key
	Model    string        `json:"model"`
	Voice    string        `json:"voice"`
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

func sendHistory(ws *websocket.Conn, session *ClientSession) {
	session.Mutex.Lock()
	combined := make([]ChatMessage, 0, len(session.Archive)+len(session.History))
	combined = append(combined, session.Archive...)
	combined = append(combined, session.History...)
	historyJSON, err := json.Marshal(combined)
	session.Mutex.Unlock()
	if err == nil {
		safeWrite(ws, session, websocket.TextMessage, []byte("[HISTORY]"+string(historyJSON)))
	}
}

func sendSummary(ws *websocket.Conn, session *ClientSession) {
	session.Mutex.Lock()
	summary := session.Summary
	archiveTurns := len(session.Archive) / 2

	// Rough token estimate: 1 token ~= 4 chars
	chars := len(summary)
	for _, msg := range session.History {
		chars += len(msg.Content)
	}
	estTokens := chars / 4

	maxTokens := 8192 // Default Ollama visual scale
	if session.Provider == "gemini" {
		maxTokens = 1000000 // Gemini 1.5 Flash scale
	}

	payload, err := json.Marshal(map[string]interface{}{
		"text":            summary,
		"archiveTurns":    archiveTurns,
		"maxArchiveTurns": 100, // 200 messages / 2
		"estTokens":       estTokens,
		"maxTokens":       maxTokens,
	})
	session.Mutex.Unlock()
	if err == nil {
		safeWrite(ws, session, websocket.TextMessage, []byte("[SUMMARY]"+string(payload)))
	}
}

func sendSettings(ws *websocket.Conn, session *ClientSession) {
	session.Mutex.Lock()
	settingsJSON, err := json.Marshal(map[string]string{
		"userName": session.UserName,
		"userBio":  session.UserBio,
		"provider": session.Provider,
		"model":    session.Model,
		"voice":    session.Voice,
	})
	session.Mutex.Unlock()
	if err == nil {
		safeWrite(ws, session, websocket.TextMessage, []byte("[SETTINGS_SYNC]"+string(settingsJSON)))
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

	cookie, err := r.Cookie("speax_session")
	if err != nil || cookie.Value == "" {
		log.Println("Unauthorized WS connection attempt")
		return
	}
	clientID := cookie.Value
	fmt.Printf("Client connected: %s\n", clientID)
	session := loadSession(clientID)
	var activeCancel context.CancelFunc

	session.Mutex.Lock()
	connectMsg := fmt.Sprintf("[System Note: User connected at %s]", time.Now().Format("Monday, January 2, 2006, 15:04 MST"))
	session.History = append(session.History, ChatMessage{Role: "system", Content: connectMsg})
	session.Mutex.Unlock()
	saveSession(session)

	defer func() {
		session.Mutex.Lock()
		lastIdx := len(session.History) - 1
		if lastIdx >= 0 && strings.HasPrefix(session.History[lastIdx].Content, "[System Note: User connected at") {
			// No actual turns were generated, so expunge the connection timestamp
			session.History = session.History[:lastIdx]
		} else {
			disconnectMsg := fmt.Sprintf("[System Note: User disconnected at %s]", time.Now().Format("Monday, January 2, 2006, 15:04 MST"))
			session.History = append(session.History, ChatMessage{Role: "system", Content: disconnectMsg})
		}
		session.Mutex.Unlock()
		saveSession(session)
		ws.Close()
	}()

	// Sync history to client on connect
	sendHistory(ws, session)
	sendSettings(ws, session)
	sendSummary(ws, session)

	// Retroactively build summary if we have an archive but no summary
	session.Mutex.Lock()
	needsSummary := len(session.Archive) > 0 && session.Summary == ""
	session.Mutex.Unlock()
	if needsSummary {
		go rebuildSummaryAsync(session, ws)
	}

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

			if text == "[CLEAR_HISTORY]" {
				session.Mutex.Lock()
				session.History = []ChatMessage{}
				session.Archive = []ChatMessage{}
				session.Summary = ""
				session.Mutex.Unlock()
				saveSession(session)
				sendHistory(ws, session)
				sendSummary(ws, session)
				continue
			}

			if text == "[REBUILD_SUMMARY]" {
				go rebuildSummaryAsync(session, ws)
				continue
			}

			if strings.HasPrefix(text, "[SETTINGS]") {
				var settings struct {
					UserName string `json:"userName"`
					UserBio  string `json:"userBio"`
					Provider string `json:"provider"`
					APIKey   string `json:"apiKey"`
					Model    string `json:"model"`
					Voice    string `json:"voice"`
				}
				if err := json.Unmarshal([]byte(strings.TrimPrefix(text, "[SETTINGS]")), &settings); err == nil {
					session.Mutex.Lock()
					session.UserName = settings.UserName
					session.UserBio = settings.UserBio
					session.Provider = settings.Provider
					session.APIKey = settings.APIKey
					session.Model = settings.Model
					session.Voice = settings.Voice
					session.Mutex.Unlock()
					log.Printf("Updated settings for client %s: User=%s, Provider=%s, Model=%s, Voice=%s", session.ClientID, session.UserName, session.Provider, session.Model, session.Voice)
					saveSession(session)
				}
				continue
			}

			if strings.HasPrefix(text, "[DELETE_MSG]:") {
				idx, err := strconv.Atoi(strings.TrimPrefix(text, "[DELETE_MSG]:"))
				if err == nil {
					session.Mutex.Lock()
					archiveLen := len(session.Archive)
					isArchiveDelete := false
					if idx >= 0 && idx < archiveLen {
						session.Archive = append(session.Archive[:idx], session.Archive[idx+1:]...)
						isArchiveDelete = true
					} else if idx >= archiveLen && idx < archiveLen+len(session.History) {
						hIdx := idx - archiveLen
						session.History = append(session.History[:hIdx], session.History[hIdx+1:]...)
					}
					session.Mutex.Unlock()
					saveSession(session)
					sendHistory(ws, session) // re-sync UI
					if isArchiveDelete {
						go rebuildSummaryAsync(session, ws)
					}
				}
				continue
			}

			go func(t string) {
				session.Mutex.Lock()
				v := session.Voice
				session.Mutex.Unlock()
				if v == "" { v = defaultVoice }
				audioBytes, err := queryTTS(t, v)
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
					if err := streamLLMAndTTS(ctx, text, ws, session); err != nil {
						log.Println("Ollama stream error:", err)
					}
					safeWrite(ws, session, websocket.TextMessage, []byte("[AI_END]"))
					// Sync finalized history back to client to enable edit/delete
					sendHistory(ws, session)
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

func extractVoiceName(filename string) string {
	base := strings.TrimSuffix(filename, ".onnx")
	parts := strings.Split(base, "-")
	if len(parts) >= 3 {
		return strings.Join(parts[1:len(parts)-1], "-") // Grabs everything between lang and quality
	} else if len(parts) == 2 {
		return parts[1]
	}
	return base
}

func streamLLMAndTTS(ctx context.Context, prompt string, ws *websocket.Conn, session *ClientSession) error {
	session.Mutex.Lock()
	provider := session.Provider
	apiKey := session.APIKey
	session.Mutex.Unlock()

	if provider == "gemini" && apiKey != "" {
		return streamGeminiAndTTS(ctx, prompt, ws, session, apiKey)
	}
	return streamOllamaAndTTS(ctx, prompt, ws, session)
}

func streamGeminiAndTTS(ctx context.Context, prompt string, ws *websocket.Conn, session *ClientSession, apiKey string) error {
	session.Mutex.Lock()
	model := session.Model
	userName := session.UserName
	userBio := session.UserBio
	voice := session.Voice
	if model == "" { model = "gemini-1.5-flash" }
	if voice == "" { voice = defaultVoice }
	voiceName := extractVoiceName(voice)
	sysContent := fmt.Sprintf("You are 'Alyx', a highly capable, voice-based AI programming assistant. "+
		"\n\n You are currently thinking using Google Gemini: %s"+
		"\n\nYour output is sent directly to a Text-to-Speech engine. "+
		"\n\n You are speaking using the voice model: %s  "+
		"\n\nYou MUST strictly follow these rules: 1. NEVER use emojis. 2. NEVER use markdown formatting like asterisks. 3. Keep responses conversational, concise, and easy to listen to. Avoid long verbose responses unless explicitly requested", model, voiceName)
	
	if userName != "" {
		sysContent += fmt.Sprintf("\n\nThe user's name is: %s.", userName)
	}
	if userBio != "" {
		sysContent += fmt.Sprintf("\nUser Bio: %s", userBio)
	}
	
	currentTime := time.Now().Format("Monday, January 2, 2006, 15:04 MST")
	log.Printf("System time injected (Gemini): %s", currentTime)
	sysContent += fmt.Sprintf("\n\nThe current date and time is: %s.", currentTime)
	

	if session.Summary != "" {
		sysContent += "\n\nContext from earlier in the conversation: " + session.Summary
	}


	type Part struct{ Text string `json:"text"` }
	type Content struct {
		Role  string `json:"role"`
		Parts []Part `json:"parts"`
	}

	var contents []Content
	for _, msg := range session.History {
		role := "user"
		if msg.Role == "assistant" { role = "model" }
		contents = append(contents, Content{Role: role, Parts: []Part{{Text: msg.Content}}})
	}
	contents = append(contents, Content{Role: "user", Parts: []Part{{Text: prompt}}})
	session.Mutex.Unlock()

	payload := map[string]interface{}{
		"systemInstruction": map[string]interface{}{"parts": []Part{{Text: sysContent}}},
		"contents":          contents,
	}
	body, _ := json.Marshal(payload)

	reqURL := fmt.Sprintf("https://generativelanguage.googleapis.com/v1beta/models/%s:streamGenerateContent?alt=sse&key=%s", model, apiKey)
	req, err := http.NewRequestWithContext(ctx, "POST", reqURL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	ttsChan := make(chan string, 50)
	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		for text := range ttsChan {
			if ctx.Err() != nil { return }
			audioBytes, err := queryTTS(text, voice)
			if err != nil {
				log.Println("Gemini TTS Worker Error:", err)
			} else if ctx.Err() == nil {
				safeWrite(ws, session, websocket.BinaryMessage, audioBytes)
			}
		}
	}()

	var sentence strings.Builder
	var fullResponse strings.Builder
	scanner := bufio.NewScanner(resp.Body)

	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "data: ") {
			data := strings.TrimPrefix(line, "data: ")
			if data == "" { continue }

			var result struct {
				Candidates []struct {
					Content struct {
						Parts []struct{ Text string `json:"text"` } `json:"parts"`
					} `json:"content"`
				} `json:"candidates"`
			}
			if err := json.Unmarshal([]byte(data), &result); err == nil && len(result.Candidates) > 0 && len(result.Candidates[0].Content.Parts) > 0 {
				content := result.Candidates[0].Content.Parts[0].Text
				safeWrite(ws, session, websocket.TextMessage, []byte(content))
				sentence.WriteString(content)
				fullResponse.WriteString(content)

				currentStr := sentence.String()
				flushIdx := -1
				if pIdx := strings.Index(currentStr, "\n"); pIdx != -1 {
					flushIdx = pIdx
				} else if len(currentStr) > 30 {
					for i := 30; i < len(currentStr); i++ {
						if currentStr[i] == '.' || currentStr[i] == '!' || currentStr[i] == '?' || currentStr[i] == ':' {
							// Lookahead to ensure it's a real boundary (followed by space or EOF)
							if i+1 == len(currentStr) || currentStr[i+1] == ' ' {
								flushIdx = i
								break
							}
						}
					}
				}

				if flushIdx != -1 {
					chunkToSend := currentStr[:flushIdx+1]
					remainder := currentStr[flushIdx+1:]
					
					ttsText := strings.TrimSpace(chunkToSend)
					if len(ttsText) > 0 {
						ttsText = strings.ReplaceAll(ttsText, "**", "")
						ttsText = strings.ReplaceAll(ttsText, "*", "")
						ttsText = strings.ReplaceAll(ttsText, "#", "")
						ttsText = strings.ReplaceAll(ttsText, "_", "")
						ttsText = strings.ReplaceAll(ttsText, "`", "")
						select {
						case ttsChan <- ttsText:
						case <-ctx.Done():
						}
					}
					sentence.Reset()
					sentence.WriteString(remainder) // Keep the rest for the next chunk
				}
			}
		}
	}

	if cleanChunk := strings.TrimSpace(sentence.String()); len(cleanChunk) > 0 {
		ttsText := cleanChunk
		ttsText = strings.ReplaceAll(ttsText, "**", "")
		ttsText = strings.ReplaceAll(ttsText, "*", "")
		ttsText = strings.ReplaceAll(ttsText, "#", "")
		ttsText = strings.ReplaceAll(ttsText, "_", "")
		ttsText = strings.ReplaceAll(ttsText, "`", "")
		select {
		case ttsChan <- ttsText:
		case <-ctx.Done():
		}
	}

	close(ttsChan)
	wg.Wait()

	if ctx.Err() == nil {
		session.Mutex.Lock()
		session.History = append(session.History, ChatMessage{Role: "user", Content: prompt})
		session.History = append(session.History, ChatMessage{Role: "assistant", Content: fullResponse.String()})
		if len(session.History) > 30 { // Keep ~15 turns active for the LLM context
			toSummarize := make([]ChatMessage, 10) // Summarize oldest 5 turns
			copy(toSummarize, session.History[:10])
			session.Archive = append(session.Archive, toSummarize...)
			session.History = session.History[10:]
			
			if len(session.Archive) > 200 { // Cap the visual UI history to 100 turns
				session.Archive = session.Archive[len(session.Archive)-200:]
			}
			go generateSummaryAsync(toSummarize, session, ws)
		}
		session.Mutex.Unlock()
		saveSession(session)
	}
	return nil
}

func streamOllamaAndTTS(ctx context.Context, prompt string, ws *websocket.Conn, session *ClientSession) error {
	session.Mutex.Lock()
	model := session.Model
	userName := session.UserName
	userBio := session.UserBio
	voice := session.Voice
	if model == "" { model = ollamaModel }
	if voice == "" { voice = defaultVoice }
	voiceName := extractVoiceName(voice)
	sysContent := fmt.Sprintf("You are 'Alyx', a highly capable, voice-based AI programming assistant. "+
		"You are built on a low-latency architecture: a Vanilla JS + WebAudio frontend, a Go WebSocket backend, Whisper STT for hearing, Ollama for thinking, and Piper TTS for speaking. "+
		"You are currently thinking using the %s model and speaking using the %s voice model. "+
		"Your output is sent directly to a Text-to-Speech engine. "+
		"You MUST strictly follow these rules: 1. NEVER use emojis. 2. NEVER use markdown formatting like asterisks. 3. Keep responses conversational, concise, and easy to listen to. Avoid long verbose responses unless explicitly requested",
		model, voiceName)

	if userName != "" {
		sysContent += fmt.Sprintf("\n\nThe user's name is: %s.", userName)
	}
	if userBio != "" {
		sysContent += fmt.Sprintf("\nUser Bio: %s", userBio)
	}

	currentTime := time.Now().Format("Monday, January 2, 2006, 15:04 MST")
	log.Printf("System time injected (Ollama): %s", currentTime)
	sysContent += fmt.Sprintf("\n\nThe current date and time is: %s.", currentTime)

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
		"model":    model,
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
	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		for text := range ttsChan {
			if ctx.Err() != nil {
				return // Abort if interrupted
			}
			audioBytes, err := queryTTS(text, voice)
			if err != nil {
				log.Println("Ollama TTS Worker Error:", err)
			} else if ctx.Err() == nil {
				safeWrite(ws, session, websocket.BinaryMessage, audioBytes)
			}
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
		
		currentStr := sentence.String()
		flushIdx := -1
		if pIdx := strings.Index(currentStr, "\n"); pIdx != -1 {
			flushIdx = pIdx
		} else if len(currentStr) > 30 {
			for i := 30; i < len(currentStr); i++ {
				if currentStr[i] == '.' || currentStr[i] == '!' || currentStr[i] == '?' || currentStr[i] == ':' {
					if i+1 == len(currentStr) || currentStr[i+1] == ' ' {
						flushIdx = i
						break
					}
				}
			}
		}

		if flushIdx != -1 {
			chunkToSend := currentStr[:flushIdx+1]
			remainder := currentStr[flushIdx+1:]
			
			ttsText := strings.TrimSpace(chunkToSend)
			if len(ttsText) > 0 {
				ttsText = strings.ReplaceAll(ttsText, "**", "")
				ttsText = strings.ReplaceAll(ttsText, "*", "")
				ttsText = strings.ReplaceAll(ttsText, "#", "")
				ttsText = strings.ReplaceAll(ttsText, "_", "")
				ttsText = strings.ReplaceAll(ttsText, "`", "")
				select {
				case ttsChan <- ttsText:
				case <-ctx.Done():
				}
			}
			sentence.Reset()
			sentence.WriteString(remainder) // Keep the rest for the next chunk
		}

		if result.Done {
			break
		}
	}

	// Flush any final text remaining in the buffer when the stream ends
	if cleanChunk := strings.TrimSpace(sentence.String()); len(cleanChunk) > 0 {
		ttsText := cleanChunk
		ttsText = strings.ReplaceAll(ttsText, "**", "")
		ttsText = strings.ReplaceAll(ttsText, "*", "")
		ttsText = strings.ReplaceAll(ttsText, "#", "")
		ttsText = strings.ReplaceAll(ttsText, "_", "")
		ttsText = strings.ReplaceAll(ttsText, "`", "")
		select {
		case ttsChan <- ttsText:
		case <-ctx.Done():
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

		if len(session.History) > 30 { // Keep ~15 turns active for the LLM context
			toSummarize := make([]ChatMessage, 10) // Summarize oldest 5 turns
			copy(toSummarize, session.History[:10])

			session.Archive = append(session.Archive, toSummarize...)
			session.History = session.History[10:]

			if len(session.Archive) > 200 { // Cap the visual UI history to 100 turns
				session.Archive = session.Archive[len(session.Archive)-200:]
			}
			go generateSummaryAsync(toSummarize, session, ws)
		}
		session.Mutex.Unlock()
		saveSession(session)
	}

	return nil
}

func rebuildSummaryAsync(session *ClientSession, ws *websocket.Conn) {
	session.Mutex.Lock()
	archiveCopy := make([]ChatMessage, len(session.Archive))
	copy(archiveCopy, session.Archive)
	session.Summary = "" // Clear existing summary to trigger full rebuild
	session.Mutex.Unlock()
	generateSummaryAsync(archiveCopy, session, ws)
}

func generateSummaryAsync(messages []ChatMessage, session *ClientSession, ws *websocket.Conn) {
	var transcript strings.Builder
	for _, msg := range messages {
		transcript.WriteString(fmt.Sprintf("%s: %s\n", msg.Role, msg.Content))
	}

	session.Mutex.Lock()
	prevSummary := session.Summary
	provider := session.Provider
	apiKey := session.APIKey
	model := session.Model
	session.Mutex.Unlock()

	prompt := "Summarize the following conversation comprehensively. Capture key facts, user preferences, technical decisions, and the overall progression of the discussion. "
	if prevSummary != "" {
		prompt += "Incorporate this previous summary context: " + prevSummary + "\n\n"
	}
	prompt += "New conversation to add to summary:\n" + transcript.String() + "\n\nProvide a detailed but well-structured summary."

	var newSummary string
	localSuccess := false

	// 1. Always attempt local summarization first to save tokens (Fast fail after 10s)
	localPayload := map[string]interface{}{"model": ollamaModel, "prompt": prompt, "stream": false}
	localBody, _ := json.Marshal(localPayload)
	
	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Post(ollamaURL, "application/json", bytes.NewReader(localBody))
	if err != nil {
		log.Printf("Local Ollama summary failed (Network/Timeout): %v", err)
	} else {
		defer resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			var result struct{ Response string `json:"response"` }
			if json.NewDecoder(resp.Body).Decode(&result) == nil {
				newSummary = strings.TrimSpace(result.Response)
				localSuccess = true
				log.Println("--- Context Memory Summarized locally via Ollama (Saved API Tokens!) ---")
			}
		} else {
			buf := new(bytes.Buffer)
			buf.ReadFrom(resp.Body)
			log.Printf("Local Ollama summary failed (HTTP %d): %s", resp.StatusCode, buf.String())
		}
	}

	// 2. Fallback to Gemini if Ollama is offline or failed
	if !localSuccess && provider == "gemini" && apiKey != "" {
		type Part struct{ Text string `json:"text"` }
		payload := map[string]interface{}{
			"contents": []map[string]interface{}{
				{"role": "user", "parts": []Part{{Text: prompt}}},
			},
		}
		body, _ := json.Marshal(payload)
		if model == "" { model = "gemini-1.5-flash" }
		reqURL := fmt.Sprintf("https://generativelanguage.googleapis.com/v1beta/models/%s:generateContent?key=%s", model, apiKey)
		if resp, err := http.Post(reqURL, "application/json", bytes.NewReader(body)); err == nil {
			defer resp.Body.Close()
			var result struct {
				Candidates []struct {
					Content struct {
						Parts []struct{ Text string `json:"text"` } `json:"parts"`
					} `json:"content"`
				} `json:"candidates"`
			}
			if json.NewDecoder(resp.Body).Decode(&result) == nil && len(result.Candidates) > 0 && len(result.Candidates[0].Content.Parts) > 0 {
				newSummary = strings.TrimSpace(result.Candidates[0].Content.Parts[0].Text)
				log.Println("--- Context Memory Summarized via Gemini API (Local fallback failed) ---")
			}
		}
	}

	if newSummary != "" {
		session.Mutex.Lock()
		session.Summary = newSummary
		session.Mutex.Unlock()
		saveSession(session)
		if ws != nil {
			sendSummary(ws, session)
		}
	}
}

func queryTTS(text string, voiceFile string) ([]byte, error) {
	modelPath := filepath.Join(".", "piper", voiceFile)
	// Execute piper binary: -f - tells it to output the WAV file directly to standard output
	cmd := exec.Command(piperBin, "--model", modelPath, "-f", "-")
	
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

func handleLogin(w http.ResponseWriter, r *http.Request) {
	b := make([]byte, 16)
	rand.Read(b)
	state := base64.URLEncoding.EncodeToString(b)
	http.SetCookie(w, &http.Cookie{Name: "oauthstate", Value: state, Path: "/", MaxAge: 3600, HttpOnly: true})

	baseURL := os.Getenv("PUBLIC_URL")
	if baseURL == "" {
		scheme := "https"
		if r.Header.Get("X-Forwarded-Proto") == "http" {
			scheme = "http"
		}
		baseURL = scheme + "://" + r.Host
	}
	redirectURI := baseURL + "/auth/callback"
	log.Printf("[Login] Host: %s, X-Forwarded-Proto: '%s', Generated Redirect: %s", r.Host, r.Header.Get("X-Forwarded-Proto"), redirectURI)

	url := fmt.Sprintf("https://accounts.google.com/o/oauth2/v2/auth?client_id=%s&redirect_uri=%s&response_type=code&scope=openid profile email&state=%s", googleClientID, redirectURI, state)
	http.Redirect(w, r, url, http.StatusTemporaryRedirect)
}

func handleCallback(w http.ResponseWriter, r *http.Request) {
	stateCookie, err := r.Cookie("oauthstate")
	if err != nil || r.FormValue("state") != stateCookie.Value {
		http.Error(w, "Invalid state", http.StatusBadRequest)
		return
	}

	data := url.Values{}
	data.Set("client_id", googleClientID)
	data.Set("client_secret", googleClientSecret)
	data.Set("code", r.FormValue("code"))
	data.Set("grant_type", "authorization_code")
	
	baseURL := os.Getenv("PUBLIC_URL")
	if baseURL == "" {
		scheme := "https"
		if r.Header.Get("X-Forwarded-Proto") == "http" {
			scheme = "http"
		}
		baseURL = scheme + "://" + r.Host
	}
	redirectURI := baseURL + "/auth/callback"
	log.Printf("[Callback] Host: %s, X-Forwarded-Proto: '%s', Generated Redirect: %s", r.Host, r.Header.Get("X-Forwarded-Proto"), redirectURI)
	data.Set("redirect_uri", redirectURI)

	resp, err := http.PostForm("https://oauth2.googleapis.com/token", data)
	if err != nil {
		http.Error(w, "Token exchange failed", http.StatusInternalServerError)
		return
	}
	defer resp.Body.Close()

	var tokenRes struct{ AccessToken string `json:"access_token"` }
	json.NewDecoder(resp.Body).Decode(&tokenRes)

	req, _ := http.NewRequest("GET", "https://www.googleapis.com/oauth2/v2/userinfo", nil)
	req.Header.Set("Authorization", "Bearer "+tokenRes.AccessToken)
	userResp, _ := http.DefaultClient.Do(req)
	defer userResp.Body.Close()

	var userRes struct {
		ID      string `json:"id"`
		Picture string `json:"picture"`
	}
	json.NewDecoder(userResp.Body).Decode(&userRes)

	if userRes.ID == "" {
		http.Error(w, "Failed to retrieve user ID from Google", http.StatusInternalServerError)
		return
	}

	http.SetCookie(w, &http.Cookie{Name: "speax_session", Value: userRes.ID, Path: "/", MaxAge: 86400 * 30, HttpOnly: false})
	http.SetCookie(w, &http.Cookie{Name: "speax_avatar", Value: url.QueryEscape(userRes.Picture), Path: "/", MaxAge: 86400 * 30, HttpOnly: false})
	http.Redirect(w, r, "/", http.StatusTemporaryRedirect)
}

func handleModels(w http.ResponseWriter, r *http.Request) {
	provider := r.URL.Query().Get("provider")
	apiKey := r.URL.Query().Get("apiKey")

	type ModelData struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	var out []ModelData

	if provider == "gemini" && apiKey != "" {
		reqURL := "https://generativelanguage.googleapis.com/v1beta/models?key=" + apiKey
		if resp, err := http.Get(reqURL); err == nil {
			defer resp.Body.Close()
			var res struct {
				Models []struct {
					Name                       string   `json:"name"`
					DisplayName                string   `json:"displayName"`
					SupportedGenerationMethods []string `json:"supportedGenerationMethods"`
				} `json:"models"`
			}
			json.NewDecoder(resp.Body).Decode(&res)
			for _, m := range res.Models {
				for _, method := range m.SupportedGenerationMethods {
					if method == "generateContent" {
						out = append(out, ModelData{ID: strings.TrimPrefix(m.Name, "models/"), Name: m.DisplayName})
						break
					}
				}
			}
		}
	} else if provider == "ollama" {
		if resp, err := http.Get("http://localhost:11434/api/tags"); err == nil {
			defer resp.Body.Close()
			var res struct{ Models []struct{ Name string `json:"name"` } `json:"models"` }
			json.NewDecoder(resp.Body).Decode(&res)
			for _, m := range res.Models { out = append(out, ModelData{ID: m.Name, Name: m.Name}) }
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out)
}

func handleVoices(w http.ResponseWriter, r *http.Request) {
	files, err := os.ReadDir("./piper")
	var out []string
	if err == nil {
		for _, f := range files {
			if strings.HasSuffix(f.Name(), ".onnx") {
				out = append(out, f.Name())
			}
		}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out)
}

func main() {
	http.HandleFunc("/auth/login", handleLogin)
	http.HandleFunc("/auth/callback", handleCallback)
	http.HandleFunc("/api/models", handleModels)
	http.HandleFunc("/api/voices", handleVoices)
	http.HandleFunc("/ws", handleConnections)
	http.Handle("/", http.FileServer(http.Dir("./public")))

	fmt.Println("Server started on :3000")
	err := http.ListenAndServe(":3000", nil)
	if err != nil {
		log.Fatal("ListenAndServe: ", err)
	}
}
