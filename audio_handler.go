package main

import (
	"context"
	"encoding/binary"
	"log"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"speaks.jakbox.dev/stt"
)

func handleAudioRequest(ws *websocket.Conn, session *ClientSession, p []byte, clientType string) {
	// Hybrid-Protocol Gateway: Check for Streaming Magic Byte 0xFF
	if len(p) > 9 && p[0] == 0xFF {
		handleStreamingAudio(ws, session, p)
		return
	}

	if clientType != "tool" {
		session.Mutex.Lock()
		session.LastActiveConn = ws
		session.Mutex.Unlock()
	}
	// Minimum byte length check (~0.5 seconds of 16-bit PCM is 16000 bytes)
	// Now with 8-byte timestamp prefix
	if len(p) < 16008 {
		log.Printf("[STT] Ignored short audio chunk: %d bytes (min 16008 with timestamp)", len(p))
		return
	}

	// Extract 8-byte Big Endian timestamp (milliseconds since epoch)
	startTimeMs := int64(binary.BigEndian.Uint64(p[:8]))
	audioData := p[8:]
	baseTime := time.Unix(0, startTimeMs*int64(time.Millisecond))

	// Process the complete phrase sent by the client
	session.Mutex.Lock()
	isAssAtStart := session.IsAssistant
	session.Mutex.Unlock()

	go func(audio []byte, bt time.Time, isA bool) {
		configMutex.RLock()
		sampleRate := config.SampleRate
		configMutex.RUnlock()

		text, err := sttManager.Transcribe(context.Background(), audio, sampleRate, session.GetSTTServers())

		if err != nil {
			if strings.Contains(err.Error(), "all STT nodes are unhealthy") {
				session.Mutex.Lock()
				t := session.ActiveThread()
				failMsg := "[System: All STT nodes are currently offline or unhealthy. The user's most recent audio could not be transcribed. Please inform the user of this service interruption and offer to help via text instead.]"
				session.appendMessage("system", failMsg, t)
				session.Mutex.Unlock()

				ctx, cancel := context.WithCancel(context.Background())
				session.Mutex.Lock()
				session.ActiveCancel = cancel
				session.Mutex.Unlock()

				if err := streamLLMAndTTS(ctx, "[SYSTEM_STT_FAILURE]", ws, session); err != nil {
					log.Println("LLM stt failure stream error:", err)
				}
			}
			log.Println("Whisper error:", err)
			return
		}
		log.Printf("[STT] Whisper Transcribed: '%s' (Start: %v)", text, bt.Format("15:04:05.000"))

		filteredText, isArtifact := stt.Filter(text)
		if isArtifact {
			log.Printf("[STT] Suppressed Whisper artifact: '%s'", text)
			safeWrite(ws, session, websocket.TextMessage, []byte("[IGNORED]"))
			return
		}
		text = filteredText
		if handleSystemTranscription(session, text, ws) {
			return
		}

		if shouldProcessPrompt(session, text, bt) {
			session.Mutex.Lock()
			if session.ActiveCancel != nil {
				session.ActiveCancel()
			}
			ctx, cancel := context.WithCancel(context.Background())
			session.ActiveCancel = cancel
			session.Mutex.Unlock()

			sendOrBroadcastText(nil, session, []byte("[CHAT]:"+text))

			if isA {
				text += " :ASSISTANT"
			}
			if err := streamLLMAndTTS(ctx, text, ws, session); err != nil {

				log.Println("LLM stream error:", err)
			}
			log.Println("[LLM] Stream complete.")
		} else {
			safeWrite(ws, session, websocket.TextMessage, []byte("[IGNORED]"))
		}
	}(audioData, baseTime, isAssAtStart)
}

func formatTranscriptSummary(text string) string {
	configMutex.RLock()
	verbose := config.VerboseLogging
	configMutex.RUnlock()
	if verbose {
		return text
	}

	words := strings.Fields(text)
	wordCount := len(words)
	if wordCount <= 10 {
		return text
	}
	return strings.Join(words[:5], " ") + " ... " + strings.Join(words[wordCount-5:], " ")
}

func handleStreamingAudio(ws *websocket.Conn, session *ClientSession, p []byte) {
	// Protocol: [0xFF][TYPE][SEQ_ID (8 bytes)][DATA]
	packetType := p[1]
	seqID := binary.BigEndian.Uint64(p[2:10])
	audioData := p[10:]

	session.BufferMutex.Lock()
	defer session.BufferMutex.Unlock()

	// VERSION GATING: Version 1+ uses real-time orchestrator, Version 0 uses legacy buffer-then-submit
	if session.Version >= 1 {
		stream, exists := session.STTStreams[int64(seqID)]
		if !exists {
			configMutex.RLock()
			sampleRate := config.SampleRate
			configMutex.RUnlock()

			// Select a healthy node for affinity (proper round-robin)
			pool := session.GetSTTServers()
			pinned, _ := sttManager.PickNodeFromPool(pool)
			pinnedURL := ""
			if pinned != nil {
				pinnedURL = pinned.URL
			}

			var loggedInitial bool
			stream = &stt.StreamSession{
				Manager:          sttManager,
				PinnedURL:        pinnedURL,
				Pool:             pool,
				SampleRate:       sampleRate,
				MinBufferSecs:    config.STTMinBuffer,
				MaxBufferSecs:    config.STTMaxBuffer,
				EnergyThreshold:  config.STTEnergyThreshold,
				UseRollingWindow: true,
				OnUpdate: func(fullTranscript string) {
					if !loggedInitial && fullTranscript != "" {
						words := strings.Fields(fullTranscript)
						if len(words) > 0 {
							log.Printf("[STT-Live] %d: %s...", seqID, formatTranscriptSummary(fullTranscript))
							loggedInitial = true
						}
					}
					targetWebClients(session, []byte("[STT_LIVE]:"+fullTranscript))

					// Barge-In Trigger: If meaningful words detected, stop any active AI response
					if stt.IsSubstantial(fullTranscript) {
						session.Mutex.Lock()
						activeCancel := session.ActiveCancel
						session.Mutex.Unlock()

						if activeCancel != nil {
							log.Printf("[Barge-In] Substantial words detected (seq %d). Interrupting AI.", seqID)
							// 1. Tell client to stop audio hardware
							safeWrite(ws, session, websocket.TextMessage, []byte("[STOP_AUDIO]"))
							// 2. Kill LLM/TTS generation
							activeCancel()
							session.Mutex.Lock()
							session.ActiveCancel = nil
							session.Mutex.Unlock()
						}
					}
				},
			}
			session.STTStreams[int64(seqID)] = stream
			session.StreamingStartTime = time.Now()
			log.Printf("[STT-Stream-v1] Started new session for sequence %d (Affinity: %s)", seqID, pinnedURL)
		}

		if packetType == 0x01 { // STREAM
			stream.PushAudio(audioData)
		} else if packetType == 0x02 { // END
			// Final synchronous wrap-up to ensure the tail is processed
			fullTranscript := stream.Finish()

			log.Printf("[STT-Stream-v1] Finalizing sequence %d", seqID)

			// Use the format: "[STT-Live] Final Transcription (35 words transcribed) sequence %d: 'initial ... last'"
			wordCount := len(strings.Fields(fullTranscript))
			log.Printf("[STT-Live] Final Transcription (%d words transcribed) sequence %d: '%s'", wordCount, seqID, formatTranscriptSummary(fullTranscript))

			// Clean up
			delete(session.STTStreams, int64(seqID))

			// Process the final transcript
			go processStreamingWhisper(ws, session, fullTranscript)
		}
	} else {
		// LEGACY PATH (Version 0)
		// Reset if sequence changes (prevents interleaving/orphaned buffers)
		if session.ActiveSeqID != int64(seqID) {
			session.ActiveSeqID = int64(seqID)
			session.StreamingBuffer = nil // Reset on new sequence
			session.StreamingStartTime = time.Now()
		}

		if packetType == 0x01 { // STREAM
			session.StreamingBuffer = append(session.StreamingBuffer, audioData...)
			// No live feedback for legacy clients
		} else if packetType == 0x02 { // END
			fullBuffer := session.StreamingBuffer
			session.StreamingBuffer = nil
			session.ActiveSeqID = 0

			log.Printf("[STT-Legacy] Finalizing sequence %d (%d bytes)", seqID, len(fullBuffer))

			go func(audio []byte) {
				configMutex.RLock()
				sampleRate := config.SampleRate
				configMutex.RUnlock()

				text, err := sttManager.Transcribe(context.Background(), audio, sampleRate, session.GetSTTServers())
				if err != nil {
					log.Printf("[STT-Legacy] Transcription failed: %v", err)
					safeWrite(ws, session, websocket.TextMessage, []byte("[IGNORED]"))
					return
				}
				log.Printf("[STT-Legacy] Final Transcription (%d words transcribed) sequence %d: '%s'", len(strings.Fields(text)), seqID, formatTranscriptSummary(text))
				processStreamingWhisper(ws, session, text)
			}(fullBuffer)
		}
	}
}

func processStreamingWhisper(ws *websocket.Conn, session *ClientSession, text string) {
	if text == "" {
		log.Println("[STT-Stream] Empty final transcript, ignoring.")
		return
	}

	filteredText, isArtifact := stt.Filter(text)
	if isArtifact {
		log.Printf("[STT-Stream] Suppressed Whisper artifact: '%s'", text)
		safeWrite(ws, session, websocket.TextMessage, []byte("[IGNORED]"))
		return
	}
	text = filteredText
	text = normalisePersonaName(session, text)
	if handleSystemTranscription(session, text, ws) {
		return
	}

	if text != "" {
		if shouldProcessPrompt(session, text, session.StreamingStartTime) {
			session.Mutex.Lock()
			if session.ActiveCancel != nil {
				session.ActiveCancel()
			}
			ctx, cancel := context.WithCancel(context.Background())
			session.ActiveCancel = cancel
			session.Mutex.Unlock()

			sendOrBroadcastText(nil, session, []byte("[CHAT]:"+text))

			if err := streamLLMAndTTS(ctx, text, ws, session); err != nil {
				log.Println("LLM stream error:", err)
			}
			log.Println("[LLM-Stream] Stream complete.")
		} else {
			log.Printf("[STT-Stream] Prompt IGNORED (Passive Mode / No Wake Word): '%s'", text)
			safeWrite(ws, session, websocket.TextMessage, []byte("[IGNORED]"))
		}
	}
}
