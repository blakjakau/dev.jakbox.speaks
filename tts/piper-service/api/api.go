package api

import (
	"encoding/json"
	"log"
	"net/http"

	"github.com/gorilla/websocket"
	"github.com/jakbox/speax/tts/piper-service/piper"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true // Allow all origins for this local service
	},
}

type API struct {
	Manager  *piper.Manager
	ModelDir string
}

func NewRouter(manager *piper.Manager, modelDir string) http.Handler {
	api := &API{
		Manager:  manager,
		ModelDir: modelDir,
	}

	mux := http.NewServeMux()
	
	mux.HandleFunc("/health", api.handleHealth)
	
	mux.HandleFunc("/tts", api.handleTTS)
	mux.HandleFunc("/stream", api.handleStream)
	
	mux.HandleFunc("/models", api.handleModels)
	mux.HandleFunc("/status", api.handleStatus)
	
	// Serve sample testing page
	mux.HandleFunc("/sample.html", api.handleSample)
	mux.HandleFunc("/sample", api.handleSample) // Convenient alias

	return mux
}

func (api *API) handleSample(w http.ResponseWriter, r *http.Request) {
	http.ServeFile(w, r, "./public/sample.html")
}

// Write JSON error response
func sendError(w http.ResponseWriter, msg string, code int) {
	log.Printf("Error %d: %s", code, msg)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
