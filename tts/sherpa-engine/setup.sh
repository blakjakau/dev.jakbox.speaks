#!/bin/bash
set -e

# --- Configuration ---
ROOT_DIR="`pwd`"
BUILD_DIR="$ROOT_DIR/build"
ESPEAK_DIR="$ROOT_DIR/espeak-ng-data"

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

# --- Hardware Acceleration Selection ---
# Note: sherpa-onnx OfflineTts supports 'cpu' and 'cuda' providers.
echo ""
echo "--- Shared Engine Hardware Acceleration ---"
echo "Select the hardware acceleration for the shared TTS engine."
echo "Even if you choose CUDA here, individual services can still opt to run in CPU mode."
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
        CMAKE_FLAGS="-DSHERPA_ONNX_ENABLE_GPU=OFF -DBUILD_SHARED_LIBS=ON"
        ;;
    *)
        SELECTED_PROVIDER="cuda"
        CMAKE_FLAGS="-DSHERPA_ONNX_ENABLE_GPU=ON -DBUILD_SHARED_LIBS=ON"
        ;;
esac

echo "$SELECTED_PROVIDER" > "$BUILD_DIR/provider.cfg"

# --- Pre-download problematic CMake dependencies ---
# We download them to /tmp where sherpa-onnx's CMake scripts are patched/configured to look for them.
echo "Pre-fetching problematic dependencies to /tmp..."
curl -L -o /tmp/hclust-cpp-2026-02-25.tar.gz https://github.com/csukuangfj/hclust-cpp/archive/refs/tags/2026-02-25.tar.gz
curl -L -o /tmp/piper-phonemize-78a788e0b719013401572d70fef372e77bff8e43.zip https://github.com/csukuangfj/piper-phonemize/archive/78a788e0b719013401572d70fef372e77bff8e43.zip

# --- Library Management ---
if [ "$SELECTED_PROVIDER" == "cuda" ]; then
    # cuDNN 8 shim — needed for Pascal (SM61) hardware alongside CUDA 12 / cuDNN 9 ONNX Runtime.
    if [ ! -f "$BUILD_DIR/lib/libcudnn.so.8" ] && [ ! -f "$BUILD_DIR/lib/nvidia/cuda_cudnn/lib/libcudnn.so.8" ]; then
        echo "Fetching cuDNN 8 & cuFFT shims for Pascal hardware support..."
        mkdir -p "$BUILD_DIR/lib"
        pip3 install --no-cache-dir --target "$BUILD_DIR/lib" nvidia-cudnn-cu11==8.9.6.50 nvidia-cufft-cu11 nvidia-cublas-cu11
    fi

    # Download Official Microsoft ONNX Runtime 1.17.1 (CUDA 12 Edition)
    # The CMake step builds against the patched version, but we need the official CUDA provider libraries
    # and headers extracted into our lib folder so the Go binary can link at runtime.
    if [ ! -f "$BUILD_DIR/lib/libonnxruntime_providers_cuda.so" ]; then
        echo "Downloading Official ONNX Runtime 1.17.1 (CUDA 12)..."
        mkdir -p "$BUILD_DIR/tmp_ort"
        ORT_URL="https://github.com/microsoft/onnxruntime/releases/download/v1.17.1/onnxruntime-linux-x64-cuda12-1.17.1.tgz"
        curl -sSL "$ORT_URL" | tar xz -C "$BUILD_DIR/tmp_ort" --strip-components=1
        
        if [ -d "$BUILD_DIR/tmp_ort/include" ]; then
            cp -P "$BUILD_DIR/tmp_ort/lib"/libonnxruntime* "$BUILD_DIR/lib/"
            mkdir -p "$BUILD_DIR/include"
            cp -R "$BUILD_DIR/tmp_ort/include/." "$BUILD_DIR/include/"
        else
            echo "ERROR: Failed to extract ONNX Runtime from GitHub."
            exit 1
        fi
        rm -rf "$BUILD_DIR/tmp_ort"
    fi
fi

# --- Sherpa-ONNX Source & Build ---
if [ ! -d "$SHERPA_SRC_DIR" ]; then
    echo "Cloning sherpa-onnx..."
    mkdir -p $(dirname "$SHERPA_SRC_DIR")
    # Bypass user's broken ~/.gitconfig which forces git:// protocol
    HOME=/tmp git clone --recursive https://github.com/k2-fsa/sherpa-onnx "$SHERPA_SRC_DIR"
fi

# Apply the Legacy ORT patch to pin ONNX Runtime to the Pascal-compatible v1.17.1 patched build
if [ "$SELECTED_PROVIDER" == "cuda" ]; then
    CMAKE_PATCH_FILE="$SHERPA_SRC_DIR/cmake/onnxruntime-linux-x86_64-gpu.cmake"
    if [ -f "$CMAKE_PATCH_FILE" ]; then
        echo "Applying Legacy ORT patch (pinning to v1.17.1 Pascal-compatible build)..."
        sed -i 's/v1\.[0-9]*\.[0-9]*/v1.17.1/g' "$CMAKE_PATCH_FILE"
        sed -i 's/1\.[0-9]*\.[0-9]*/1.17.1/g' "$CMAKE_PATCH_FILE"
        sed -i 's/onnxruntime-linux-x64-gpu-1.17.1.tgz/onnxruntime-linux-x64-gpu-1.17.1-patched.zip/g' "$CMAKE_PATCH_FILE"
        sed -i 's/set(onnxruntime_HASH "SHA256=[^"]*")/set(onnxruntime_HASH "SHA256=1261de176e8d9d4d2019f8fa8c732c6d11494f3c6e73168ab6d2cc0903f22551")/g' "$CMAKE_PATCH_FILE"
    fi
fi

echo "--- Building Sherpa-ONNX for $SELECTED_PROVIDER ---"
cd "$SHERPA_SRC_DIR"
rm -rf CMakeCache.txt CMakeFiles/
mkdir -p build && cd build && rm -rf *
cmake $CMAKE_FLAGS -DSHERPA_ONNX_ENABLE_PYTHON=OFF ..
make -j$(nproc)
cd "$ROOT_DIR"

# Populate build artifacts
mkdir -p "$BUILD_DIR/lib"
find "$SHERPA_SRC_DIR/build/lib" -name "lib*.so*" ! -name "libonnxruntime*" -exec cp -v {} "$BUILD_DIR/lib/" \;

# --- eSpeak-ng Data Download ---
# Using the bundled data from a real model archive guarantees phontab and all required binary files are present
echo "Ensuring espeak-ng-data is ready..."
curl -SL "https://github.com/k2-fsa/sherpa-onnx/releases/download/tts-models/vits-piper-en_GB-alan-medium.tar.bz2" -o espeak_bootstrap.tar.bz2

mkdir -p "$(dirname "$ESPEAK_DIR")"
tar -xf espeak_bootstrap.tar.bz2 -C "$(dirname "$ESPEAK_DIR")" \
    --strip-components=1 \
    --wildcards --no-anchored 'espeak-ng-data'

rm -f espeak_bootstrap.tar.bz2

if [ ! -f "$ESPEAK_DIR/phontab" ]; then
    echo "ERROR: espeak-ng-data/phontab not found! Cannot proceed."
    exit 1
fi
echo "espeak-ng-data OK (phontab present)"

echo "Shared Sherpa-ONNX Engine compiled successfully."
