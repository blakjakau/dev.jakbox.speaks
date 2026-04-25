# Lab Development Log: Kokoro TTS Service

## 📝 Overview
This document summarizes the technical journey of building a portable, hardware-accelerated Kokoro TTS service capable of running across a heterogeneous hardware lab (Ampere, Pascal, and Intel architectures).

## 🚀 The Pascal Paradox (Solved)
The primary challenge was initializing the service on **NVIDIA Pascal (GTX 1070/1080)** GPUs. Modern ONNX Runtime builds (1.17+) paired with V580+ drivers natively "fail" on Pascal due to the removal of legacy SM61 kernels from standard CUDA 11/12 math library distributions.

### The Solution: The "Pure CUDA 12 Stack"
We bypassed the hardware limitations by shimming a custom, isolated library stack:
1. **Engine:** Official Microsoft ONNX Runtime 1.17.1 (Specially the `cuda12` tgz variant).
2. **Drivers:** Official NVIDIA `nvidia-*-cu12` PyPI wheels (which surprisingly contain more robust backward-compatible kernels than many cloud distributions).
3. **Shim:** Restoration of `libcudnn.so.8` (required by ONNX 1.17.1 even in CUDA 12 mode).
4. **Resilience:** Relative-path symlinking inside the `/build` folder to ensure the entire directory is portable across systems.

## 📊 Performance Metrics
*Measured on the utterance: "Let me know what your real-time processing latency (Time To First Audio) feels like when speaking natively to the OpenVINO provider now!"*

| Hardware | Provider | TTFA (Time to First Audio) | Status |
| :--- | :--- | :--- | :--- |
| **RTX 3060ti (Ampere)** | CUDA 12 | **475ms** | 🟢 Ideal |
| **GTX 1080 (Pascal)** | CUDA 12 (Shimmed) | **850ms** | 🟢 Stable |
| **Intel 580 (UHD)** | OpenVINO (CPU) | **2600ms** | 🟡 Usable for background |

## 🛠 Current Configuration
- **Port:** `4411`
- **Output:** Streaming 24kHz Mono PCM via WebSocket (`/stream`)
- **Key Files:**
  - `main.go`: Includes dynamic thread-scaling for CPU and absolute path asset resolution.
  - `setup.sh`: Idempotent script that builds the CUDA 12 + cuDNN shim stack.
  - `launch.sh`: Pre-loads local Sherpa-ONNX binaries and selects the best GPU.

## ⏭ Next Steps: The Quantization Goal
To bridge the gap between the 1080 (850ms) and the Intel UHD (2600ms), we need to reduce the math complexity for non-GPU nodes.

### Objective: 1s Latency on CPU
- **Project:** `kokoro-quantizer`
- **Mechanism:** Convert the base `model.onnx` (FP32) into an **OpenVINO IR (Int8)** or **ONNX-Quantized (Int8)** format.
- **Tools:** Use `optimum-intel` or `nncf` (OpenVINO Compression Framework) to generate an Int8 model that the OpenVINO provider can consume with 2x-3x speedup.
- **Challenge:** Ensure the 82M parameter model doesn't lose its "emotive" quality during the weight compression.

---
**Status:** Stable / GPU Verified. 
**Handover Note:** If resuming this project, the core logic is in `main.go` and the library magic is in `setup.sh`. The GPU selection logic in `launch.sh` is now V580-aware.
