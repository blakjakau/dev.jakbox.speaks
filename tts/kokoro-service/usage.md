# Kokoro TTS Service Usage Guide

High-performance, native Go Text-to-Speech (TTS) service using Sherpa-ONNX and the Kokoro-82M model. Features zero-shot voice cloning, dynamic voice mixing, and an experimental voice mapper.

- **Default Port**: `4411`
- **Audio Format**: Raw 16-bit Signed PCM, Mono, Little-Endian.
- **Sample Rate**: 24000Hz (standard for Kokoro v1.0).

---

## 🚀 Core Endpoints

### 1. `GET /health`
Returns runtime metrics and engine heartbeat.

### 2. `GET /models`
Returns an array of available voice names, including built-in Kokoro voices and custom mixes.

### 3. `GET /status`
Returns service status, active model, and hardware provider info.

---

## 🌊 WebSocket & Command Line Options

### Command Line Flags
The service now supports modular hardware acceleration via the `--provider` flag:
- `cuda`: NVIDIA GPU (Requires libraries in `build/lib/nvidia`).
- `openvino`: Intel iGPU/CPU (Experimental, fetched via setup).
- `vulkan`: Generic GPU (Currently experimental).
- `cpu`: CPU only.

**Usage**: `./kokoro-service --provider cuda`

---

## 🌊 WebSocket Streaming Protocol (`/stream`)

The primary interface for real-time synthesis. Supports standard synthesis and dynamic voice mixing.

### Standard Synthesis Request
```json
{
  "text": "Hello world",
  "model": "af_bella",
  "length_scale": 1.0,
  "annotated": true
}
```

### Voice Mixing Request (`type: "mix_request"`)
Dynamically create a new voice by blending existing ones.
```json
{
  "type": "mix_request",
  "text": "Testing the mixed voice.",
  "weights": {
    "af_bella": 0.5,
    "am_adam": 0.5
  },
  "method": "linear"
}
```
- `method`: `"linear"`, `"slerp"`, or `"eq"`.

### Server Packet Sequence
1.  **`{"type": "start", "sampleRate": 24000}`**: Initial sync frame.
2.  **`{"type": "text", "text": "...", "audio_bytes": 1234}`**: Metadata for the upcoming audio chunk.
3.  **`[Raw PCM Binary]`**: Binary audio data.
4.  **`{"type": "end"}`**: Stream completion.

---

## 🎨 Advanced Voice Features

### 1. Voice Mixing (`POST /api/mix/preview`)
Generate a preview WAV for a custom mix.
- **Payload**: `{ "text": "...", "weights": {...}, "method": "linear" }`
- **Response**: `audio/wav` file.

### 2. Voice Saving (`POST /api/mix/save`)
Persist a custom mix to a specific slot.
- **Payload**: `{ "name": "MyCustomVoice", "slot": "af_heart" }`

### 3. Experimental Voice Mapper (`POST /api/trainer/process`)
Create a voice embedding from reference audio files (Zero-shot cloning).
- **Multipart Form**:
    - `audio`: One or more WAV files.
    - `iterations`: Training loops (default: 5).
    - `seed`: Base voice for refinement.
- **Response**: Streams text progress, ends with `RESULT:{"voice_id":"sandbox_0"}`.

---

## 💻 Code Sample (JavaScript Wrapper)
```javascript
async function synthesizeStream(text, voice = 'bf_emma') {
    const ws = new WebSocket('ws://localhost:4411/stream');
    ws.binaryType = 'arraybuffer';
    
    ws.onopen = () => {
        ws.send(JSON.stringify({ text, model: voice }));
    };

    ws.onmessage = (e) => {
        if (typeof e.data === 'string') {
            const meta = JSON.parse(e.data);
            console.log("Metadata:", meta);
        } else {
            // Buffer or play binary PCM
            handleAudio(e.data);
        }
    };
}
```
