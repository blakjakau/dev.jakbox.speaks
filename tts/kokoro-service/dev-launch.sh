#!/bin/bash

# Get the absolute path of the directory containing this script
SCRIPT_DIR="$( cd "$( dirname "${BASH_SOURCE[0]}" )" &> /dev/null && pwd )"
BUILD_DIR="$SCRIPT_DIR/build"
BUILD_LIB="$BUILD_DIR/lib"

echo "Starting Kokoro Service in DEV mode (go run)..."
echo "Root Dir: $SCRIPT_DIR"

# --- Provider Selection ---
SELECTED_PROVIDER="cuda"
if [ -f "$BUILD_DIR/provider.cfg" ]; then
    SELECTED_PROVIDER=$(cat "$BUILD_DIR/provider.cfg")
fi
echo "Target Provider: $SELECTED_PROVIDER"

# --- CUDA Library Detection ---
NV_LIBS=""

if [ "$SELECTED_PROVIDER" == "cuda" ]; then
    # 1. Check System Paths
    SYSTEM_CUDA="/usr/local/cuda/lib64"
    if [ -d "$SYSTEM_CUDA" ]; then
        echo "Detected System CUDA at $SYSTEM_CUDA"
        NV_LIBS="$NV_LIBS:$SYSTEM_CUDA"
    fi

    # 2. Check Local Build Directory
    LOCAL_NV_BASE="$BUILD_LIB/nvidia"
    if [ -d "$LOCAL_NV_BASE" ]; then
        echo "Detected NVIDIA libs in local build directory: $LOCAL_NV_BASE"
        # Dynamically find all 'lib' subdirectories to avoid missing dependencies like nvjitlink
        for lib_dir in $(find "$LOCAL_NV_BASE" -type d -name "lib"); do
            NV_LIBS="$NV_LIBS:$lib_dir"
        done
    fi
elif [ "$SELECTED_PROVIDER" == "openvino" ]; then
    echo "Using OpenVINO provider. Libraries expected in $BUILD_LIB"
    export OPENVINO_LIBPATH="$BUILD_LIB"
fi

# 3. Final LD_LIBRARY_PATH configuration
OLLAMA_CUDA="/usr/local/lib/ollama/cuda_v12"
if [ -d "$OLLAMA_CUDA" ] && [ "$SELECTED_PROVIDER" == "cuda" ]; then
    export LD_LIBRARY_PATH="$OLLAMA_CUDA:$BUILD_LIB$NV_LIBS:$LD_LIBRARY_PATH"
else
    export LD_LIBRARY_PATH="$BUILD_LIB$NV_LIBS:$LD_LIBRARY_PATH"
fi

# Dynamically select the best GPU (prevents crashes on fragmented/display GPUs)
if command -v nvidia-smi &> /dev/null; then
    # Sort GPUs by free memory (descending) and pick the index of the best one
    BEST_GPU=$(nvidia-smi --query-gpu=index,memory.free --format=csv,noheader,nounits | sort -t ',' -k2 -nr | head -n 1 | awk -F ',' '{print $1}')
    if [ -n "$BEST_GPU" ]; then
        export CUDA_VISIBLE_DEVICES=$BEST_GPU
        echo "Dynamically selected best GPU (Most Free VRAM): $BEST_GPU"
    fi
fi

# 4. LD_PRELOAD Overlay
# The Go sherpa-onnx module has a hardcoded RPATH to its precompiled binaries.
# To force it to use our locally-built OpenVINO/Legacy-CUDA binaries, we MUST preload them.
if [ -f "$BUILD_LIB/libsherpa-onnx-c-api.so" ]; then
    export LD_PRELOAD="$BUILD_LIB/libsherpa-onnx-c-api.so:$BUILD_LIB/libsherpa-onnx-cxx-api.so"
    echo "Preloaded locally-built Sherpa-ONNX engine for dev."
fi

# Navigate to the service root
cd "$SCRIPT_DIR"

# Ensure the 'models' symlink is setup correctly for use from root if it's not already
if [ -L "models" ] && [ "$(readlink models)" == "build/models/kokoro-multi-lang-v1_0" ]; then
    echo "Fixing models symlink for root execution..."
    rm models
    mkdir -p models
    ln -s ../build/models/kokoro-multi-lang-v1_0 models/kokoro-multi-lang-v1_0
fi

# Run via go run to ensure latest code changes are picked up
exec go run . --provider "$SELECTED_PROVIDER"
