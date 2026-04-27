#!/bin/bash
set -e

# --- Configuration ---
ROOT_DIR="`pwd`"
MODELS_DIR="$ROOT_DIR/models"
SHARED_ENGINE_DIR="$ROOT_DIR/../sherpa-engine"

# --- Dependency Checks ---
echo "--- Checking Dependencies ---"
for cmd in go curl python3 pip3 unzip; do
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

# --- Hardware Acceleration Selection ---
echo ""
echo "--- Service Hardware Provider Selection ---"
echo "Select the preferred hardware acceleration provider for this specific microservice."
echo "Even if the shared engine has CUDA, you may want Piper to run on CPU."
if [ -t 0 ]; then
    echo "1) NVIDIA GPU (CUDA)"
    echo "2) CPU Only"
    read -p "Selection [1-2]: " HW_CHOICE
else
    HW_CHOICE="2"
fi
HW_CHOICE=${HW_CHOICE:-2}

case $HW_CHOICE in
    2)
        SELECTED_PROVIDER="cpu"
        ;;
    *)
        SELECTED_PROVIDER="cuda"
        ;;
esac

echo "$SELECTED_PROVIDER" > "provider.cfg"

# --- Network Resilience Shims (The "Fake Bin" Strategy) ---
mkdir -p "fake_bin"
export PATH="$ROOT_DIR/fake_bin:$PATH"

# 2. Curl Shim: Ensure -L (follow redirects) and add timeout/retry resilience
cat << 'EOF' > "fake_bin/curl"
#!/bin/bash
if [[ "$*" == *"-L"* ]]; then
    exec /usr/bin/curl --connect-timeout 10 --retry 3 "$@"
else
    exec /usr/bin/curl -L --connect-timeout 10 --retry 3 "$@"
fi
EOF
chmod +x "fake_bin/curl"

# --- Models & Assets ---
mkdir -p "$MODELS_DIR"

# Function to download a Sherpa-ONNX Piper model.
download_model() {
    local repo_name=$1
    local base_url="https://github.com/k2-fsa/sherpa-onnx/releases/download/tts-models/$repo_name.tar.bz2"
    local short_name="${repo_name#vits-piper-}"
    
    if find "$MODELS_DIR" -maxdepth 1 -name "*${short_name}*.onnx" | grep -q .; then
        echo "Model $repo_name already present, skipping."
        return
    fi
    
    echo "Downloading Sherpa-optimized Piper model: $repo_name..."
    curl -fSL "$base_url" -o model_temp.tar.bz2
    local curl_status=$?
    
    if [ $curl_status -ne 0 ]; then
        echo "  [ERROR] Download failed. Skipping."
        rm -f model_temp.tar.bz2
        return 1
    fi
    
    local file_size=$(stat -c%s model_temp.tar.bz2 2>/dev/null || echo 0)
    if [ "$file_size" -lt 10000 ]; then
        echo "  [ERROR] Download too small. Skipping."
        rm -f model_temp.tar.bz2
        return 1
    fi
    
    if ! head -c 3 model_temp.tar.bz2 | grep -q 'BZh'; then
        echo "  [ERROR] Invalid bzip2 file. Skipping."
        rm -f model_temp.tar.bz2
        return 1
    fi
    
    # Extract only .onnx and .onnx.json (.txt tokens are in model metadata json/runtime now, or espeak fallback)
    tar -xf model_temp.tar.bz2 -C "$MODELS_DIR" --strip-components=1 \
        --wildcards --no-anchored '*.onnx' '*.onnx.json' '*.txt'
    rm model_temp.tar.bz2
    echo "  -> $(find "$MODELS_DIR" -maxdepth 1 -name "*${short_name}*.onnx")"
}

echo "--- Downloading Core Piper Models ---"
download_model "vits-piper-en_GB-alan-medium"
download_model "vits-piper-en_US-lessac-medium"

# --- Patch any user-supplied standard Piper models ---
echo "Patching any unpatched Piper models in models/..."
python3 -m pip install --quiet onnx 2>/dev/null || pip3 install --quiet onnx 2>/dev/null || true
python3 patch_piper_models.py "$MODELS_DIR"

# --- Build Go Service ---
echo "Building Go Service..."
go build -o "piper-service" .

echo "Setup Complete!"
