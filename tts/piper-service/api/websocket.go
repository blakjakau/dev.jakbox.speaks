package api

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

type sessionState struct {
	firstChunkSent bool
	isAnnotated    bool
}

func (api *API) handleStream(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("Websocket upgrade failed: %v", err)
		return
	}
	defer func() {
		log.Printf("Websocket client disconnected: %v", conn.RemoteAddr())
		conn.Close()
	}()
	log.Printf("Websocket client connected: %v", conn.RemoteAddr())

	// Per-connection state
	textBuffer := ""
	state := &sessionState{
		firstChunkSent: false,
		isAnnotated:    false,
	}
	textInput := make(chan struct {
		text      string
		annotated bool
	}, 100)
	done := make(chan struct{})

	// Timer for 1500ms inactivity flush
	flushTimer := time.NewTimer(1500 * time.Millisecond)
	flushTimer.Stop()

	// Synthesis & Processing Processor
	go func() {
		defer close(done)
		for {
			select {
			case req, ok := <-textInput:
				if !ok {
					// Final flush before closing
					log.Printf("[WS] Channel closed, performing final flush of %d chars", len(textBuffer))
					api.flushRemaining(conn, &textBuffer, state)
					return
				}

				if req.annotated {
					state.isAnnotated = true
				}

				log.Printf("[WS] Received text chunk: '%s'", req.text)

				// Input received, stop the timer and ensure it's drained
				if !flushTimer.Stop() {
					select {
					case <-flushTimer.C:
					default:
					}
				}

				textBuffer += req.text

				// Iteratively process buffers as boundaries are identified (the "gates")
				for {
					minLength := 10
					hardLimit := 350

					splitIdx := findSentenceBoundary(textBuffer, minLength, hardLimit)
					if splitIdx == -1 {
						break // Buffer incomplete, wait for more text or inactivity
					}

					segment := textBuffer[:splitIdx+1]
					textBuffer = textBuffer[splitIdx+1:]

					log.Printf("[WS] Gated segment identified: '%s' (Stage 1: %v)", segment, !state.firstChunkSent)
					api.synthesizeAndSend(conn, segment, state)
				}

				// Always start/restart the inactivity timer unconditionally
				// to ensure we can officially close the stream.
				flushTimer.Reset(1500 * time.Millisecond)

			case <-flushTimer.C:
				if len(textBuffer) > 0 {
					log.Printf("[WS] Inactivity flush trigger: '%s'", textBuffer)
					api.flushRemaining(conn, &textBuffer, state)
				} else if state.firstChunkSent {
					log.Printf("[WS] Inactivity end trigger: stream completed")
					api.finalizeStream(conn, state)
				}
			}
		}
	}()

	// Reader Loop
	for {
		messageType, p, err := conn.ReadMessage()
		if err != nil {
			close(textInput)
			break
		}

		if messageType != websocket.TextMessage {
			continue
		}

		var req TTSRequest
		if err := json.Unmarshal(p, &req); err != nil {
			log.Printf("WebSocket invalid json: %v", err)
			continue
		}

		if req.Text != "" {
			textInput <- struct {
				text      string
				annotated bool
			}{req.Text, req.Annotated}
		}
	}
	<-done
}

// synthesizeAndSend performs core synthesis and writes binary data to WS immediately.
func (api *API) synthesizeAndSend(conn *websocket.Conn, text string, state *sessionState) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}

	log.Printf("[WS] Engine Synthesis Start: '%s'", text)

	if state.isAnnotated {
		textMap := map[string]interface{}{
			"type": "text",
			"text": text,
		}
		if p, err := json.Marshal(textMap); err == nil {
			conn.WriteMessage(websocket.TextMessage, p)
		}
	}

	err := api.Manager.SynthesizeStream(text, func(audioData []int16) {
		if len(audioData) == 0 {
			return
		}

		if !state.firstChunkSent {
			srMap := map[string]interface{}{
				"type":       "start",
				"sampleRate": api.Manager.GetSampleRate(),
			}
			if p, err := json.Marshal(srMap); err == nil {
				conn.WriteMessage(websocket.TextMessage, p)
			}
			state.firstChunkSent = true
		}

		log.Printf("[WS] Binary Chunk: sending %d samples", len(audioData))

		// Convert to raw bytes to stream as binary message
		buf := new(bytes.Buffer)
		for _, v := range audioData {
			binary.Write(buf, binary.LittleEndian, v)
		}

		if err := conn.WriteMessage(websocket.BinaryMessage, buf.Bytes()); err != nil {
			log.Printf("Failed to write to WS: %v", err)
		}
	})

	if err != nil {
		log.Printf("Streaming synthesis failed on WS: %v", err)
		errResp := map[string]string{"error": err.Error()}
		if p, err := json.Marshal(errResp); err == nil {
			conn.WriteMessage(websocket.TextMessage, p)
		}
		return
	}
	log.Printf("[WS] Engine Synthesis Complete: '%s'", text)
}

// flushRemaining consumes the rest of the buffer after a timeout or connection end.
func (api *API) flushRemaining(conn *websocket.Conn, buffer *string, state *sessionState) {
	text := *buffer
	if text == "" {
		return
	}

	log.Printf("[WS] Flushing remaining text (%d chars). Splitting into sentences.", len(text))

	// Even during a flush, we split by sentence to avoid Piper truncation issues
Loop:
	for {
		if text == "" {
			break
		}

		// Use Stage 1 rules (aggressive splitting) for the flush to ensure nothing is missed
		splitIdx := findSentenceBoundary(text, 0, 350)
		if splitIdx == -1 {
			// No more boundaries, synthesize the rest as one last chunk
			api.synthesizeAndSend(conn, text, state)
			break Loop
		}

		segment := text[:splitIdx+1]
		text = text[splitIdx+1:]

		api.synthesizeAndSend(conn, segment, state)
	}

	*buffer = ""
	api.finalizeStream(conn, state)
}

// finalizeStream formally ends the stream block by sending a trailing JSON message.
func (api *API) finalizeStream(conn *websocket.Conn, state *sessionState) {
	if !state.firstChunkSent {
		return // Nothing was ever sent, so don't emit end
	}
	endMap := map[string]interface{}{"type": "end"}
	if p, err := json.Marshal(endMap); err == nil {
		conn.WriteMessage(websocket.TextMessage, p)
	}
	state.firstChunkSent = false // Reset gate for an entirely new utterance
	state.isAnnotated = false    // Reset annotated mode for the next block
}
