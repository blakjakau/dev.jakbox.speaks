#!/bin/bash

# Get the absolute path of the directory containing this script
SCRIPT_DIR="$( cd "$( dirname "${BASH_SOURCE[0]}" )" &> /dev/null && pwd )"
SHARED_ENGINE_DIR="$SCRIPT_DIR/../sherpa-engine/build"
BUILD_LIB="$SHARED_ENGINE_DIR/lib"

echo "Starting Piper Service..."
echo "Build Dir: $SCRIPT_DIR"

# --- Provider Selection ---
SELECTED_PROVIDER="cpu"
if [ -f "$SCRIPT_DIR/provider.cfg" ]; then
    SELECTED_PROVIDER=$(cat "$SCRIPT_DIR/provider.cfg")
fi
echo "Target Provider: $SELECTED_PROVIDER"

# --- CUDA Library Detection ---
NV_LIBS=""

if [ "$SELECTED_PROVIDER" == "cuda" ]; then
    SYSTEM_CUDA="/usr/local/cuda/lib64"
    if [ -d "$SYSTEM_CUDA" ]; then
        echo "Detected System CUDA at $SYSTEM_CUDA"
        NV_LIBS="$NV_LIBS:$SYSTEM_CUDA"
    fi

    LOCAL_NV_BASE="$BUILD_LIB/nvidia"
    if [ -d "$LOCAL_NV_BASE" ]; then
        echo "Detected NVIDIA libs in local build directory: $LOCAL_NV_BASE"
        for lib_dir in $(find "$LOCAL_NV_BASE" -type d -name "lib"); do
            NV_LIBS="$NV_LIBS:$lib_dir"
        done
    fi
elif [ "$SELECTED_PROVIDER" == "openvino" ]; then
    echo "Using OpenVINO provider. Libraries expected in $BUILD_LIB"
    export OPENVINO_LIBPATH="$BUILD_LIB"
fi

# --- Library Path Configuration ---
OLLAMA_CUDA="/usr/local/lib/ollama/cuda_v12"
if [ -d "$OLLAMA_CUDA" ] && [ "$SELECTED_PROVIDER" == "cuda" ]; then
    export LD_LIBRARY_PATH="$OLLAMA_CUDA:$BUILD_LIB$NV_LIBS:$LD_LIBRARY_PATH"
else
    export LD_LIBRARY_PATH="$BUILD_LIB$NV_LIBS:$LD_LIBRARY_PATH"
fi

# Dynamically select the best GPU
if command -v nvidia-smi &> /dev/null; then
    BEST_GPU=$(nvidia-smi --query-gpu=index,memory.free --format=csv,noheader,nounits | sort -t ',' -k2 -nr | head -n 1 | awk -F ',' '{print $1}')
    if [ -n "$BEST_GPU" ]; then
        export CUDA_VISIBLE_DEVICES=$BEST_GPU
        echo "Dynamically selected best GPU (Most Free VRAM): $BEST_GPU"
    fi
fi

# --- LD_PRELOAD Overlay ---
# Force use of locally built sherpa-onnx binaries
if [ -f "$BUILD_LIB/libsherpa-onnx-c-api.so" ]; then
    export LD_PRELOAD="$BUILD_LIB/libsherpa-onnx-c-api.so:$BUILD_LIB/libsherpa-onnx-cxx-api.so"
    echo "Preloaded locally-built Sherpa-ONNX engine."
fi

# --- Run ---
cd "$SCRIPT_DIR"
exec ./piper-service --provider "$SELECTED_PROVIDER" --espeak "../sherpa-engine/espeak-ng-data" --models "models"
