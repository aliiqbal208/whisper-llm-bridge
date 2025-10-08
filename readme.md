# Whisper-Ollama-Go

A high-performance Go bridge service connecting [Whisper](https://github.com/openai/whisper) (speech-to-text) and [Ollama](https://ollama.com/) (LLM inference) via a fast HTTP API. This project is optimized for low latency and high concurrency, outperforming Python alternatives.

## Architecture

- **Whisper Service**: Local instance for speech recognition
- **Ollama Service**: Local instance for LLM inference
- **Go Bridge Server**: Connects Whisper and Ollama, exposes API
- **Client**: Sends audio files to the bridge

## Features

- Fast HTTP API for audio transcription and LLM processing
- Concurrency control for high throughput
- Docker Compose orchestration for all services
- Health check endpoint (`/health`)
- Main processing endpoint (`/process`)
- Example clients in Python, JavaScript, and shell

## Quick Start

### Prerequisites

- Docker & Docker Compose
- Go 1.18+ (for local builds)
- GPU recommended for Whisper

### Build and Run

1. **Clone the repository**
   ```sh
   git clone <your-repo-url>
   cd whisper-ollama-go
   ```

2. **Pull Ollama models**
   ```sh
   docker-compose up -d ollama
   docker exec -it $(docker ps -q -f name=ollama) ollama pull llama3
   ```

3. **Start all services**
   ```sh
   docker-compose up -d
   ```

### API Usage

#### `/process` endpoint

- **Method:** POST
- **Form fields:**
  - `file`: Audio file (e.g., mp3, wav)
  - `prompt`: Prompt for LLM (optional)
  - `model`: LLM model name (optional, default: `llama3`)

**Example (curl):**
```sh
curl -X POST \
  -F "file=@recording.mp3" \
  -F "prompt=Summarize this transcription" \
  -F "model=llama3" \
  http://localhost:8080/process
```

**Response:**
```json
{
  "transcription": "...",
  "response": "...",
  "process_time_ms": 1234,
  "model": "llama3"
}
```

#### `/health` endpoint

- **Method:** GET
- **Response:** `OK`

## Performance Tuning

- System and Docker optimizations are described in [SampleImplementation.txt](SampleImplementation.txt).
- Includes benchmarking scripts and advanced scaling tips.

## Client Examples

- Python, JavaScript, and shell scripts are provided in [SampleImplementation.txt](SampleImplementation.txt).

## License

MIT

---

For advanced usage, optimizations, and client code, see [SampleImplementation.txt](SampleImplementation.txt).