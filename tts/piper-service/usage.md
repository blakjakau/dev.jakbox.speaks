# Piper TTS Service Usage Guide

High-performance, low-latency, gapless Text-to-Speech (TTS) service using the Piper engine. Designed for real-time applications requiring sentence-level streaming and synchronization.

- **Default Port**: `4410`
- **Audio Format**: Raw 16-bit Signed PCM, Mono, Little-Endian.
- **Sample Rate**: Dynamic (determined by the loaded `.onnx` model, typically 22050Hz or 16000Hz).

---

## 🚀 Core Endpoints

### 1. `GET /health`
Returns runtime metrics and engine heartbeat.
```json
{
  "status": "ok",
  "uptime_seconds": 1240.5,
  "memory_usage_bytes": 15728640,
  "goroutines": 8
}
```

### 2. `GET /status`
Returns information about the active model and the internal hot-RAM cache.
```json
{
  "service_type": "piper",
  "active_model": "alan-medium.onnx",
  "cached_models": ["alan-medium.onnx", "maddy-medium.onnx"],
  "total_cache_size_estimate": 12345678,
  "metrics": {
    "total_inbound_bytes": 1024,
    "total_outbound_bytes": 2048000
  }
}
```

### 3. `POST /models`
Switches the active voice model.
- **Request Body**: `{ "model": "maddy-medium.onnx" }`
- **Response**: `{ "status": "success" }`

### 4. `POST /tts`
Standard HTTP synthesis. Blocks until generated and returns a WAV file.
- **Request Body**: 
```json
{ 
  "text": "Hello world", 
  "model": "optional_model_name.onnx",
  "length_scale": 1.0,
  "noise_scale": 0.667,
  "noise_w": 0.8
}
```
- **Response**: `audio/wav` binary stream.

---

## 🌊 WebSocket Streaming Protocol (`/stream`)

The WebSocket endpoint is designed for maximum throughput and real-time UI synchronization.

### Input Message (JSON)
The client sends a JSON payload to initiate or continue synthesis:
```json
{
  "text": "The quick brown fox. It jumps over the dog.",
  "model": "alan-medium.onnx",
  "annotated": true,
  "length_scale": 1.0,
  "noise_scale": 0.667,
  "noise_w": 0.8,
  "variance": 0.05,
  "cmd": ""
}
```

- `annotated`: If `true`, the server will send `{"type": "text"}` frames before each audio segment.
- `variance`: Adds subtle per-sentence randomization to synthesis parameters for more natural variety.
- `cmd`: Optional diagnostic commands (`"health"`, `"status"`).

### Server Packet Sequence
The server interleaves JSON metadata with raw Binary audio chunks.

1.  **`{"type": "start", "sampleRate": 22050}`**: (JSON) Configures the client's audio clock.
2.  **`{"type": "text", "text": "The quick..."}`**: (JSON) Sent if `annotated: true`.
3.  **`[Raw PCM Binary]`**: (Binary) Audio chunks for the sentence.
4.  **`{"type": "end"}`**: (JSON) Sent when the request is complete.

---

## 💻 Code Sample (JavaScript)
```javascript
const socket = new WebSocket('ws://localhost:4410/stream');
socket.binaryType = 'arraybuffer';

socket.onmessage = (event) => {
    if (typeof event.data === 'string') {
        const meta = JSON.parse(event.data);
        if (meta.type === 'start') {
            initAudio(meta.sampleRate);
        } else if (meta.type === 'text') {
            updateUI(meta.text);
        }
    } else {
        playPCM(event.data);
    }
};

socket.onopen = () => {
    socket.send(JSON.stringify({ text: "Hello from Piper!", annotated: true }));
};
```
