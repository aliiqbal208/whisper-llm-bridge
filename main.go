package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

// Configuration variables
var (
	whisperURL     = getEnv("WHISPER_URL", "http://whisper:9000")
	ollamaURL      = getEnv("OLLAMA_URL", "http://ollama:11434")
	maxConcurrent  = getEnvAsInt("MAX_CONCURRENT_REQUESTS", 50)
	serverPort     = getEnv("SERVER_PORT", "8080")
	requestTimeout = getEnvAsInt("REQUEST_TIMEOUT", 300) // seconds
)

// Semaphore for limiting concurrent requests
var semaphore chan struct{}

// Response structures
type WhisperResponse struct {
	Text     string `json:"text"`
	Segments []any  `json:"segments"`
	Language string `json:"language"`
}

type OllamaRequest struct {
	Model  string `json:"model"`
	Prompt string `json:"prompt"`
	Stream bool   `json:"stream"`
}

type OllamaResponse struct {
	Model    string `json:"model"`
	Response string `json:"response"`
	Finished bool   `json:"done"`
}

type CombinedResponse struct {
	Transcription string `json:"transcription"`
	Response      string `json:"response"`
	ProcessTime   int64  `json:"process_time_ms"`
	Model         string `json:"model"`
}

func main() {
	// Initialize semaphore for controlling concurrency
	semaphore = make(chan struct{}, maxConcurrent)

	// Set up HTTP server with sensible timeouts
	server := &http.Server{
		Addr:         ":" + serverPort,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: time.Duration(requestTimeout+30) * time.Second,
		Handler:      setupRoutes(),
	}

	log.Printf("Starting Whisper-Ollama bridge on port %s", serverPort)
	log.Printf("Whisper URL: %s", whisperURL)
	log.Printf("Ollama URL: %s", ollamaURL)
	log.Printf("Max concurrent requests: %d", maxConcurrent)

	log.Fatal(server.ListenAndServe())
}

func setupRoutes() http.Handler {
	mux := http.NewServeMux()

	// Health check endpoint
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})

	// Main processing endpoint
	mux.HandleFunc("/process", processAudioHandler)

	// Add logging middleware
	return logMiddleware(mux)
}

// Process audio handler
func processAudioHandler(w http.ResponseWriter, r *http.Request) {
	startTime := time.Now()

	// Acquire semaphore slot or reject if too many concurrent requests
	select {
	case semaphore <- struct{}{}:
		defer func() { <-semaphore }()
	default:
		http.Error(w, "Server is at capacity, please try again later", http.StatusServiceUnavailable)
		return
	}

	// Only accept POST
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Set timeout for the entire request processing
	ctx := r.Context()
	var cancel context.CancelFunc
	ctx, cancel = context.WithTimeout(ctx, time.Duration(requestTimeout)*time.Second)
	defer cancel()

	// Get multipart form
	err := r.ParseMultipartForm(32 << 20) // 32MB max memory
	if err != nil {
		http.Error(w, "Failed to parse form: "+err.Error(), http.StatusBadRequest)
		return
	}

	// Get form values
	model := r.FormValue("model")
	if model == "" {
		model = "llama3" // Default model
	}

	prompt := r.FormValue("prompt")
	if prompt == "" {
		prompt = "Process this transcription:"
	}

	// Get the audio file
	file, handler, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "Failed to get audio file: "+err.Error(), http.StatusBadRequest)
		return
	}
	defer file.Close()

	// Create temp file to store the uploaded file
	tempFile, err := os.CreateTemp("", "upload-*."+filepath.Ext(handler.Filename))
	if err != nil {
		http.Error(w, "Failed to create temp file: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer os.Remove(tempFile.Name())
	defer tempFile.Close()

	// Copy uploaded file to temp file
	_, err = io.Copy(tempFile, file)
	if err != nil {
		http.Error(w, "Failed to write temp file: "+err.Error(), http.StatusInternalServerError)
		return
	}
	tempFile.Close() // Close to ensure all data is written

	// Transcribe audio with Whisper
	transcription, err := transcribeWithWhisper(tempFile.Name())
	if err != nil {
		http.Error(w, "Transcription failed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Process with Ollama
	response, err := processWithOllama(model, prompt, transcription)
	if err != nil {
		// Return transcription even if Ollama processing fails
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(CombinedResponse{
			Transcription: transcription,
			Response:      "Ollama processing failed: " + err.Error(),
			ProcessTime:   time.Since(startTime).Milliseconds(),
			Model:         model,
		})
		return
	}

	// Return combined response
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(CombinedResponse{
		Transcription: transcription,
		Response:      response,
		ProcessTime:   time.Since(startTime).Milliseconds(),
		Model:         model,
	})
}

// Transcribe audio with Whisper
func transcribeWithWhisper(filePath string) (string, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return "", fmt.Errorf("failed to open file: %w", err)
	}
	defer file.Close()

	// Create multipart request
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)

	part, err := writer.CreateFormFile("audio_file", filepath.Base(filePath))
	if err != nil {
		return "", fmt.Errorf("failed to create form file: %w", err)
	}

	_, err = io.Copy(part, file)
	if err != nil {
		return "", fmt.Errorf("failed to copy file: %w", err)
	}

	// Close multipart writer
	err = writer.Close()
	if err != nil {
		return "", fmt.Errorf("failed to close writer: %w", err)
	}

	// Create request
	client := &http.Client{
		Timeout: time.Duration(requestTimeout) * time.Second,
	}

	req, err := http.NewRequest("POST", whisperURL+"/asr", body)
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", writer.FormDataContentType())

	// Send request
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("whisper returned non-200 status: %d, body: %s", resp.StatusCode, string(bodyBytes))
	}

	// Read response
	var whisperResp WhisperResponse
	err = json.NewDecoder(resp.Body).Decode(&whisperResp)
	if err != nil {
		return "", fmt.Errorf("failed to decode response: %w", err)
	}

	return whisperResp.Text, nil
}

// Process transcription with Ollama
func processWithOllama(model, prompt, transcription string) (string, error) {
	// Prepare request
	ollamaReq := OllamaRequest{
		Model:  model,
		Prompt: fmt.Sprintf("%s\n\nTranscription: %s", prompt, transcription),
		Stream: false,
	}

	reqBody, err := json.Marshal(ollamaReq)
	if err != nil {
		return "", fmt.Errorf("failed to marshal request: %w", err)
	}

	// Create request
	client := &http.Client{
		Timeout: time.Duration(requestTimeout) * time.Second,
	}

	req, err := http.NewRequest("POST", ollamaURL+"/api/generate", bytes.NewBuffer(reqBody))
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")

	// Send request
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("ollama returned non-200 status: %d, body: %s", resp.StatusCode, string(bodyBytes))
	}

	// Read response
	var ollamaResp OllamaResponse
	err = json.NewDecoder(resp.Body).Decode(&ollamaResp)
	if err != nil {
		return "", fmt.Errorf("failed to decode response: %w", err)
	}

	return ollamaResp.Response, nil
}

// Logging middleware
func logMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		// Create a custom response writer to capture status code
		rw := &responseWriter{
			ResponseWriter: w,
			statusCode:     http.StatusOK,
		}

		next.ServeHTTP(rw, r)

		// Log request
		log.Printf(
			"%s %s %d %s",
			r.Method,
			r.RequestURI,
			rw.statusCode,
			time.Since(start),
		)
	})
}

// Custom response writer to capture status code
type responseWriter struct {
	http.ResponseWriter
	statusCode int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}

// Helper functions for environment variables
func getEnv(key, fallback string) string {
	if value, exists := os.LookupEnv(key); exists {
		return value
	}
	return fallback
}

func getEnvAsInt(key string, fallback int) int {
	if value, exists := os.LookupEnv(key); exists {
		if intVal, err := strconv.Atoi(value); err == nil {
			return intVal
		}
	}
	return fallback
}
