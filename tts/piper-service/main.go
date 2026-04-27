package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"runtime"
	"strings"
)

func main() {
	port := flag.String("port", "4410", "Port to run the service on")
	modelsDir := flag.String("models", "./models", "Directory containing .onnx voice models")
	espeakData := flag.String("espeak", "./piper/espeak-ng-data", "Path to eSpeak NG data directory")
	provider := flag.String("provider", "cpu", "Hardware acceleration provider (cpu, cuda)")
	// Default threads = NumCPU/2 maps to physical cores on HT machines (e.g. i7 = 8 logical / 2 = 4 physical)
	defaultThreads := runtime.NumCPU() / 2
	if defaultThreads < 1 {
		defaultThreads = 1
	}
	threads := flag.Int("threads", defaultThreads, "Number of ONNX inference threads (default: NumCPU/2)")
	flag.Parse()

	log.Printf("Starting Piper TTS Service on port %s (Provider: %s, Threads: %d/%d logical)", *port, *provider, *threads, runtime.NumCPU())
	log.Printf("Models directory: %s", *modelsDir)

	// Ensure models directory exists
	if _, err := os.Stat(*modelsDir); os.IsNotExist(err) {
		log.Printf("Warning: models directory does not exist: %s. It must be created by setup.sh or manually.", *modelsDir)
		os.MkdirAll(*modelsDir, 0755)
	}

	// Initialize Piper Manager
	manager := NewManager(*modelsDir, *espeakData, *provider, *threads)
	defer manager.Close()

	// Find the first available alphabetical .onnx model to preload
	entries, err := os.ReadDir(*modelsDir)
	if err == nil {
		var firstModel string
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".onnx") {
				firstModel = e.Name()
				break
			}
		}
		
		if firstModel != "" {
			log.Printf("Auto-loading initial model: %s", firstModel)
			if err := manager.LoadModel(firstModel); err != nil {
				log.Fatalf("FATAL: failed to preload initial model %s: %v\n"+
					"  Check that --espeak and --models paths are correct and the model is not corrupted.\n"+
					"  Run with launch.sh which sets up LD_LIBRARY_PATH and LD_PRELOAD correctly.", firstModel, err)
			}
		} else {
			log.Println("No .onnx models found in models directory")
		}
	} else {
		log.Printf("Failed to read models directory: %v", err)
	}

	// Initialize Overrides
	InitOverrides()

	// Initialize API router
	router := NewRouter(manager, *modelsDir)

	log.Printf("Service is ready. Listening on :%s", *port)
	if err := http.ListenAndServe(fmt.Sprintf(":%s", *port), router); err != nil {
		log.Fatalf("HTTP server error: %v", err)
	}
}
