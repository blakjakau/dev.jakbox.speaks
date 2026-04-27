#!/bin/bash
set -e

# --- Configuration ---
KOKORO_MODEL_URL="https://github.com/k2-fsa/sherpa-onnx/releases/download/tts-models/kokoro-multi-lang-v1_0.tar.bz2"
ROOT_DIR="`pwd`"
SHARED_ENGINE_DIR="$ROOT_DIR/../sherpa-engine"

# --- Dependency Checks ---
echo "--- Checking Dependencies ---"
for cmd in go curl unzip; do
    if ! command -v $cmd > /dev/null 2>&1; then
        echo "Error: $cmd is not installed."
        exit 1
    fi
done

if [ ! -d "$SHARED_ENGINE_DIR/build/lib" ]; then
    echo "ERROR: Shared Sherpa Engine not found at $SHARED_ENGINE_DIR"
    echo "Please run setup.sh inside tts/sherpa-engine first to initialize the shared engine."
    exit 1
fi

# --- Feature Selection ---
if [ -t 0 ]; then
    read -p "Do you want to use the experimental voice mapper? [y/N] " ENABLE_TRAINER
else
    ENABLE_TRAINER="n"
fi
ENABLE_TRAINER=${ENABLE_TRAINER:-n}

if [[ "$ENABLE_TRAINER" =~ ^[Yy]$ ]]; then
    echo '{"trainer_enabled": true}' > "feature_config.json"
else
    echo '{"trainer_enabled": false}' > "feature_config.json"
fi

# --- Hardware Acceleration Selection ---
echo ""
echo "--- Service Hardware Provider Selection ---"
echo "Select the preferred hardware acceleration provider for this specific microservice."
echo "Ensure the chosen provider was compiled into the shared engine."
if [ -t 0 ]; then
    echo "1) NVIDIA GPU (CUDA)"
    echo "2) CPU Only"
    read -p "Selection [1-2]: " HW_CHOICE
else
    HW_CHOICE="1"
fi
HW_CHOICE=${HW_CHOICE:-1}

case $HW_CHOICE in
    2)
        SELECTED_PROVIDER="cpu"
        ;;
    *)
        SELECTED_PROVIDER="cuda"
        ;;
esac

echo "$SELECTED_PROVIDER" > "provider.cfg"

# --- Models & Assets ---
mkdir -p "$ROOT_DIR/models"

# Download models
if [ ! -d "$ROOT_DIR/models/kokoro-multi-lang-v1_0" ]; then
    echo "Downloading Kokoro model..."
    curl -SL "$KOKORO_MODEL_URL" -o kokoro-multi-lang-v1_0.tar.bz2
    tar xf kokoro-multi-lang-v1_0.tar.bz2 -C "$ROOT_DIR/models/"
    rm kokoro-multi-lang-v1_0.tar.bz2
fi

# --- Build Go Service ---
echo "Building Go Service..."
go build -o "kokoro-service" .

if [ ! -f "mixes.json" ]; then echo "{}" > "mixes.json"; fi

echo "Setup Complete!"
