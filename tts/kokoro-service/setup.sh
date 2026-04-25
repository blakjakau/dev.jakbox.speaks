#!/bin/bash
set -e

# --- Configuration ---
KOKORO_MODEL_URL="https://github.com/k2-fsa/sherpa-onnx/releases/download/tts-models/kokoro-multi-lang-v1_0.tar.bz2"
ROOT_DIR="`pwd`"
BUILD_DIR="$ROOT_DIR/build"

# Dependency paths
mkdir -p "$BUILD_DIR/deps"
SHERPA_SRC_DIR="$BUILD_DIR/deps/sherpa-onnx"

echo "Sherpa Source:  $SHERPA_SRC_DIR"

# --- Dependency Checks ---
echo "--- Checking Dependencies ---"
for cmd in cmake g++ go curl python3 pip3 unzip; do
    if ! command -v $cmd > /dev/null 2>&1; then
        echo "Error: $cmd is not installed."
        exit 1
    fi
done

# --- Feature Selection ---
if [ -t 0 ]; then
    read -p "Do you want to use the experimental voice mapper? [y/N] " ENABLE_TRAINER
else
    ENABLE_TRAINER="n"
fi
ENABLE_TRAINER=${ENABLE_TRAINER:-n}

mkdir -p "$BUILD_DIR"
if [[ "$ENABLE_TRAINER" =~ ^[Yy]$ ]]; then
    echo '{"trainer_enabled": true}' > "$BUILD_DIR/feature_config.json"
else
    echo '{"trainer_enabled": false}' > "$BUILD_DIR/feature_config.json"
fi

# --- Hardware Acceleration Selection ---
echo ""
echo "--- Hardware Acceleration Selection ---"
if [ -t 0 ]; then
    echo "1) NVIDIA GPU (CUDA)"
    echo "2) Intel iGPU/CPU (OpenVINO)"
    echo "3) Generic GPU (Vulkan)"
    echo "4) CPU Only"
    read -p "Selection [1-4]: " HW_CHOICE
else
    HW_CHOICE="1"
fi
HW_CHOICE=${HW_CHOICE:-1}

case $HW_CHOICE in
    2)
        SELECTED_PROVIDER="openvino"
        CMAKE_FLAGS="-DSHERPA_ONNX_ENABLE_GPU=OFF -DBUILD_SHARED_LIBS=ON"
        ;;
    3)
        SELECTED_PROVIDER="vulkan"
        CMAKE_FLAGS="-DSHERPA_ONNX_ENABLE_GPU=OFF -DBUILD_SHARED_LIBS=ON"
        ;;
    4)
        SELECTED_PROVIDER="cpu"
        CMAKE_FLAGS="-DSHERPA_ONNX_ENABLE_GPU=OFF -DBUILD_SHARED_LIBS=ON"
        ;;
    *)
        SELECTED_PROVIDER="cuda"
        CMAKE_FLAGS="-DSHERPA_ONNX_ENABLE_GPU=ON -DBUILD_SHARED_LIBS=ON"
        ;;
esac

echo "$SELECTED_PROVIDER" > "$BUILD_DIR/provider.cfg"

if [ "$SELECTED_PROVIDER" == "cuda" ]; then
    if [ ! -d "$BUILD_DIR/lib/nvidia" ] || [ ! -f "$BUILD_DIR/lib/libonnxruntime_providers_cuda.so" ]; then
        echo "Fetching required NVIDIA libraries and ONNX Runtime..."
        rm -rf "$BUILD_DIR/lib"
        mkdir -p "$BUILD_DIR/lib"
        mkdir -p "$BUILD_DIR/include"
        
        # We still need cuDNN 8 from pip as it's not bundled with ONNX or present in Ollama
        echo "Fetching cuDNN 8 via pip..."
        pip3 install --no-cache-dir --target "$BUILD_DIR/lib" \
            nvidia-cudnn-cu11==8.9.6.50
        
        # Download Official Microsoft ONNX Runtime 1.17.1 (CUDA 12 Edition)
        echo "Downloading Official ONNX Runtime 1.17.1 (CUDA 12)..."
        mkdir -p "$BUILD_DIR/tmp_ort"
        ORT_URL="https://github.com/microsoft/onnxruntime/releases/download/v1.17.1/onnxruntime-linux-x64-cuda12-1.17.1.tgz"
        curl -sSL "$ORT_URL" | tar xz -C "$BUILD_DIR/tmp_ort" --strip-components=1
        
        # Verify extraction succeeded before moving
        if [ -d "$BUILD_DIR/tmp_ort/include" ]; then
            cp -P "$BUILD_DIR/tmp_ort/lib"/libonnxruntime* "$BUILD_DIR/lib/"
            cp -R "$BUILD_DIR/tmp_ort/include/." "$BUILD_DIR/include/"
        else
            echo "ERROR: Failed to extract ONNX Runtime from GitHub."
            exit 1
        fi
        rm -rf "$BUILD_DIR/tmp_ort"
    else
        echo "CUDA & ONNX libraries already cached. Skipping download."
    fi
elif [ "$SELECTED_PROVIDER" == "openvino" ]; then
    if [ ! -f "$BUILD_DIR/lib/libopenvino.so" ] || [ ! -f "$BUILD_DIR/lib/libonnxruntime.so" ] || [ ! -f "$BUILD_DIR/include/onnxruntime_c_api.h" ]; then
        echo "Fetching OpenVINO runtime libraries via wheel extraction..."
        rm -rf "$BUILD_DIR/lib"
        mkdir -p "$BUILD_DIR/lib"
        mkdir -p "$BUILD_DIR/include"
        TMP_WHL_DIR="$BUILD_DIR/tmp_whl"
        mkdir -p "$TMP_WHL_DIR"
        pip3 download --no-cache-dir -d "$TMP_WHL_DIR" onnxruntime-openvino openvino
        for whl in "$TMP_WHL_DIR"/*.whl; do
            unzip -q -o "$whl" -d "$TMP_WHL_DIR/extracted"
        done
        # Organize libraries
        find "$TMP_WHL_DIR/extracted" -name "*.so*" -exec cp -P {} "$BUILD_DIR/lib/" \;
        
        # Wheels often provide versioned .so (e.g. libonnxruntime.so.1.24.1)
        # The linker requires the generic libonnxruntime.so symlink, and runtime requires .so.1
        if [ ! -f "$BUILD_DIR/lib/libonnxruntime.so" ]; then
            LATEST_SO=$(ls "$BUILD_DIR/lib"/libonnxruntime.so.* | head -n 1)
            if [ -n "$LATEST_SO" ]; then
                ln -sf "$(basename "$LATEST_SO")" "$BUILD_DIR/lib/libonnxruntime.so"
                ln -sf "$(basename "$LATEST_SO")" "$BUILD_DIR/lib/libonnxruntime.so.1"
            fi
        fi
        
        # Organize headers (Download official C++ headers since wheels don't have them)
        echo "Downloading official ONNX Runtime C++ headers..."
        HDR_URL="https://github.com/microsoft/onnxruntime/releases/download/v1.17.1/onnxruntime-linux-x64-1.17.1.tgz"
        # Download and extract just the include folder
        curl -SL "$HDR_URL" | tar xz -C "$BUILD_DIR/include" --strip-components=2 "onnxruntime-linux-x64-1.17.1/include"
        
        rm -rf "$TMP_WHL_DIR"
    else
        echo "OpenVINO runtime libraries already cached. Skipping download."
    fi
fi

# Force Sherpa-ONNX CMake to use our perfectly compatible local bindings
# This bypasses the default ONNX GPU download which is hardcoded to a CUDA 12 / cuDNN 9 patched zip!
export SHERPA_ONNXRUNTIME_LIB_DIR="$BUILD_DIR/lib"
export SHERPA_ONNXRUNTIME_INCLUDE_DIR="$BUILD_DIR/include"
mkdir -p "$BUILD_DIR/include"
CMAKE_FLAGS="$CMAKE_FLAGS -DSHERPA_ONNX_USE_PRE_INSTALLED_ONNXRUNTIME_IF_AVAILABLE=ON"

# --- Sherpa-ONNX Source ---
if [ ! -d "$SHERPA_SRC_DIR" ]; then
    echo "Cloning sherpa-onnx..."
    mkdir -p $(dirname "$SHERPA_SRC_DIR")
    # Bypass user's broken ~/.gitconfig which forces git:// protocol that is blocked by their firewall
    HOME=/tmp git clone --recursive https://github.com/k2-fsa/sherpa-onnx "$SHERPA_SRC_DIR"
fi

# Apply patch for Legacy Architecture Support
CMAKE_PATCH_FILE="$SHERPA_SRC_DIR/cmake/onnxruntime-linux-x86_64-gpu.cmake"
if [ -f "$CMAKE_PATCH_FILE" ]; then
    echo "Applying Legacy ORT patch..."
    sed -i 's/v1\.[0-9]*\.[0-9]*/v1.17.1/g' "$CMAKE_PATCH_FILE"
    sed -i 's/1\.[0-9]*\.[0-9]*/1.17.1/g' "$CMAKE_PATCH_FILE"
    sed -i 's/onnxruntime-linux-x64-gpu-1.17.1.tgz/onnxruntime-linux-x64-gpu-1.17.1-patched.zip/g' "$CMAKE_PATCH_FILE"
    sed -i 's/set(onnxruntime_HASH "SHA256=[^"]*")/set(onnxruntime_HASH "SHA256=1261de176e8d9d4d2019f8fa8c732c6d11494f3c6e73168ab6d2cc0903f22551")/g' "$CMAKE_PATCH_FILE"
fi

# Patch Sherpa-ONNX Source for OpenVINO
echo "Applying robust self-healing OpenVINO source patches..."

# 1. Idempotency checks to ensure patches are only applied once.
# We do this instead of \`git checkout\` because the dependency might be downloaded as a ZIP (no .git).

# 2. Patch provider.h
if ! grep -q "kOpenVINO =" "$SHERPA_SRC_DIR/sherpa-onnx/csrc/provider.h"; then
    sed -i '/kSpacemiT = 7,/a \  kOpenVINO = 8,' "$SHERPA_SRC_DIR/sherpa-onnx/csrc/provider.h"
fi

# 3. Patch provider.cc using a temporary file
if ! grep -q "return Provider::kOpenVINO;" "$SHERPA_SRC_DIR/sherpa-onnx/csrc/provider.cc"; then
    cat > "$BUILD_DIR/ov_pcc.cc" <<EOF
  } else if (s == "openvino") {
    return Provider::kOpenVINO;
EOF
    sed -i '/return Provider::kSpacemiT;/r '"$BUILD_DIR/ov_pcc.cc"'' "$SHERPA_SRC_DIR/sherpa-onnx/csrc/provider.cc"
fi

# 4. Patch session.cc using a temporary file
if ! grep -q "device_type\"] = \"AUTO\"" "$SHERPA_SRC_DIR/sherpa-onnx/csrc/session.cc"; then
    # Overwrite the old GPU patch if it exists by resetting the file first
    git -C "$SHERPA_SRC_DIR" checkout sherpa-onnx/csrc/session.cc 2>/dev/null || true
    
    cat > "$BUILD_DIR/ov_sess.cc" <<EOF
    case Provider::kOpenVINO: {
      std::unordered_map<std::string, std::string> provider_options(config);
      if (provider_options.find("device_type") == provider_options.end()) {
          provider_options["device_type"] = "AUTO";
      }
      sess_opts.AppendExecutionProvider("OpenVINO", provider_options);
      break;
    }
EOF
    # We insert BEFORE Spacemit
    sed -i '/case Provider::kSpacemiT: {/i \\' "$SHERPA_SRC_DIR/sherpa-onnx/csrc/session.cc" 
    sed -i '/case Provider::kSpacemiT: {/i \PLACEHOLDER_OV' "$SHERPA_SRC_DIR/sherpa-onnx/csrc/session.cc"
    sed -i '/PLACEHOLDER_OV/r '"$BUILD_DIR/ov_sess.cc"'' "$SHERPA_SRC_DIR/sherpa-onnx/csrc/session.cc"
    sed -i '/PLACEHOLDER_OV/d' "$SHERPA_SRC_DIR/sherpa-onnx/csrc/session.cc"
fi

# 5. Setup CUDA provider device selection
if ! grep -q "options.device_id =" "$SHERPA_SRC_DIR/sherpa-onnx/csrc/session.cc"; then
    sed -i 's|// set more options on need|options.device_id = 0;|g' "$SHERPA_SRC_DIR/sherpa-onnx/csrc/session.cc"
fi

rm -f "$BUILD_DIR/ov_pcc.cc" "$BUILD_DIR/ov_sess.cc"
echo "Source patches successfully applied via grep checks."

# --- Build Sherpa-ONNX ---
echo "--- Building Sherpa-ONNX for $SELECTED_PROVIDER ---"
cd "$SHERPA_SRC_DIR"
rm -rf CMakeCache.txt CMakeFiles/ _deps/
mkdir -p build && cd build && rm -rf *
cmake $CMAKE_FLAGS -DSHERPA_ONNX_ENABLE_PYTHON=OFF ..
make -j4
cd "$ROOT_DIR"

# --- Populate Build Directory ---
# Extract Sherpa's artifacts without destroying our manually downloaded CUDA 12 ONNX payload
echo "Populating build artifacts..."
mkdir -p "$BUILD_DIR/lib"
mkdir -p "$BUILD_DIR/models"
find "$SHERPA_SRC_DIR/build/lib" -name "lib*.so*" ! -name "libonnxruntime*" -exec cp -v {} "$BUILD_DIR/lib/" \;

# Download models
if [ ! -d "$BUILD_DIR/models/kokoro-multi-lang-v1_0" ]; then
    echo "Downloading Kokoro model..."
    curl -SL "$KOKORO_MODEL_URL" -o kokoro-multi-lang-v1_0.tar.bz2
    tar xf kokoro-multi-lang-v1_0.tar.bz2 -C "$BUILD_DIR/models/"
    rm kokoro-multi-lang-v1_0.tar.bz2
fi

# --- Build Go Service ---
echo "Building Go Service..."
go build -o "$BUILD_DIR/kokoro-service" .

# --- Development Symlinks & Assets ---
ln -sf "build/models/kokoro-multi-lang-v1_0" "models"
ln -sf "build/feature_config.json" "feature_config.json"
if [ ! -f "mixes.json" ]; then echo "{}" > "mixes.json"; fi

# Ensure assets are in the build folder for isolated execution
echo "Syncing assets to build folder..."
cp -R public "$BUILD_DIR/"
if [ -f "mixes.json" ]; then cp mixes.json "$BUILD_DIR/"; fi
if [ -f "feature_config.json" ]; then cp feature_config.json "$BUILD_DIR/"; fi
if [ -f "tts-override.json" ]; then cp tts-override.json "$BUILD_DIR/"; fi

echo "Setup Complete!"
