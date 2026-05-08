package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode"
)

var idMap = map[string]string{
	"claude-2-5-geminiflash-20240101":     "gemini-2.5-flash",
	"claude-2-5-geminipro-20240101":       "gemini-2.5-pro",
	"claude-2-5-geminiflashlite-20240101": "gemini-2.5-flash-lite",
	"claude-3-1-geminipro-20240101":       "gemini-3.1-pro",
	"claude-3-0-geminiflash-20240101":     "gemini-3-flash",
}

// UpstreamModelResponse maps the incoming JSON structure from the target server
type UpstreamModelResponse struct {
	Data []struct {
		ID string `json:"id"`
	} `json:"data"`
}

// AnthropicModel maps the outgoing JSON structure that Claude expects
type AnthropicModel struct {
	Type        string `json:"type"`
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
	CreatedAt   string `json:"created_at"`
}

// DownstreamModelResponse wraps the Anthropic models
type DownstreamModelResponse struct {
	Data    []AnthropicModel `json:"data"`
	HasMore bool             `json:"has_more"`
}

// ProxyServer encapsulates the proxy configuration and handlers
type ProxyServer struct {
	targetURL *url.URL
	proxy     *httputil.ReverseProxy
}

// NewProxyServer creates a configured ReverseProxy
func NewProxyServer(target string) (*ProxyServer, error) {
	targetURL, err := url.Parse(strings.TrimRight(target, "/"))
	if err != nil {
		return nil, fmt.Errorf("invalid target URL: %w", err)
	}

	ps := &ProxyServer{
		targetURL: targetURL,
	}

	rp := &httputil.ReverseProxy{
		Director:       ps.director,
		ModifyResponse: ps.modifyResponse,
		ErrorHandler:   ps.errorHandler,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
	}
	ps.proxy = rp

	return ps, nil
}

func (ps *ProxyServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ps.proxy.ServeHTTP(w, r)
}

func (ps *ProxyServer) director(req *http.Request) {
	req.URL.Scheme = ps.targetURL.Scheme
	req.URL.Host = ps.targetURL.Host
	req.URL.Path = ps.targetURL.Path + req.URL.Path
	req.Host = ps.targetURL.Host

	// Avoid compressed responses so we can easily parse JSON streams
	req.Header.Del("Accept-Encoding")

	if strings.Contains(req.URL.Path, "/v1/messages") && req.Body != nil {
		ps.modifyMessageRequest(req)
	}
}

func (ps *ProxyServer) modifyMessageRequest(req *http.Request) {
	bodyBytes, err := io.ReadAll(req.Body)
	if err != nil {
		log.Printf("Failed to read request body: %v", err)
		return
	}
	defer req.Body.Close()

	// We use map[string]interface{} here to avoid dropping unmapped JSON fields (like messages, max_tokens, etc.)
	var reqJSON map[string]interface{}
	if err := json.Unmarshal(bodyBytes, &reqJSON); err != nil {
		// Not valid JSON, restore original body
		req.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))
		return
	}

	fakeID, ok := reqJSON["model"].(string)
	if !ok {
		// "model" key missing or not a string, restore original body
		req.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))
		return
	}

	if realID, exists := idMap[fakeID]; exists {
		reqJSON["model"] = realID
		log.Printf("🔄 Swapped model ID for completion: %s -> %s\n", fakeID, realID)

		newBodyBytes, err := json.Marshal(reqJSON)
		if err == nil {
			req.Body = io.NopCloser(bytes.NewBuffer(newBodyBytes))
			req.ContentLength = int64(len(newBodyBytes))
			req.Header.Set("Content-Length", strconv.Itoa(len(newBodyBytes)))
			return
		}
		log.Printf("Failed to marshal modified request: %v", err)
	}

	// Fallback to original body if no swap occurred or marshaling failed
	req.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))
}

func (ps *ProxyServer) modifyResponse(resp *http.Response) error {
	// Only intercept successful /v1/models requests
	if !strings.Contains(resp.Request.URL.Path, "/v1/models") || resp.StatusCode != http.StatusOK {
		return nil
	}

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response body: %w", err)
	}
	resp.Body.Close()

	var upstreamResp UpstreamModelResponse
	if err := json.Unmarshal(bodyBytes, &upstreamResp); err != nil {
		// Unexpected structure, return body unmodified
		resp.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))
		return nil
	}

	// Guarantee we initialize an empty slice rather than nil for JSON consistency
	anthropicModels := make([]AnthropicModel, 0)
	for _, item := range upstreamResp.Data {
		if item.ID == "" {
			continue
		}
		anthropicModels = append(anthropicModels, AnthropicModel{
			Type:        "model",
			ID:          mapToClaudeID(item.ID),
			DisplayName: titleCase(item.ID),
			CreatedAt:   "2024-01-01T00:00:00Z",
		})
	}

	downstreamResp := DownstreamModelResponse{
		Data:    anthropicModels,
		HasMore: false,
	}

	newBodyBytes, err := json.Marshal(downstreamResp)
	if err != nil {
		log.Printf("Failed to marshal downstream response: %v", err)
		resp.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))
		return nil
	}

	resp.Body = io.NopCloser(bytes.NewBuffer(newBodyBytes))
	resp.ContentLength = int64(len(newBodyBytes))
	resp.Header.Set("Content-Length", strconv.Itoa(len(newBodyBytes)))
	resp.Header.Set("Content-Type", "application/json")

	log.Println("✅ Served formatted models to Claude UI!")
	return nil
}

func (ps *ProxyServer) errorHandler(w http.ResponseWriter, r *http.Request, err error) {
	log.Printf("❌ Proxy error: %v", err)
	w.WriteHeader(http.StatusBadGateway)
}

func mapToClaudeID(originalID string) string {
	switch {
	case strings.Contains(originalID, "gemini-2.5-flash-lite"):
		return "claude-2-5-geminiflashlite-20240101"
	case strings.Contains(originalID, "gemini-2.5-flash"):
		return "claude-2-5-geminiflash-20240101"
	case strings.Contains(originalID, "gemini-2.5-pro"):
		return "claude-2-5-geminipro-20240101"
	case strings.Contains(originalID, "gemini-3.1-pro"):
		return "claude-3-1-geminipro-20240101"
	case strings.Contains(originalID, "gemini-3-flash"):
		return "claude-3-0-geminiflash-20240101"
	default:
		return fmt.Sprintf("claude-3-0-%s-20240101", strings.ReplaceAll(originalID, "-", ""))
	}
}

func titleCase(s string) string {
	var result []rune
	capitalizeNext := true
	for _, r := range s {
		if unicode.IsLetter(r) {
			if capitalizeNext {
				result = append(result, unicode.ToUpper(r))
				capitalizeNext = false
			} else {
				result = append(result, unicode.ToLower(r))
			}
		} else {
			result = append(result, r)
			capitalizeNext = true
		}
	}
	return string(result)
}

// --- Middlewares ---

// loggingResponseWriter captures the status code for logging purposes
type loggingResponseWriter struct {
	http.ResponseWriter
	statusCode int
}

func (lrw *loggingResponseWriter) WriteHeader(code int) {
	lrw.statusCode = code
	lrw.ResponseWriter.WriteHeader(code)
}

func loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		lrw := &loggingResponseWriter{w, http.StatusOK} // Default to 200

		next.ServeHTTP(lrw, r)

		log.Printf("[%s] %s %d (%v)", r.Method, r.URL.Path, lrw.statusCode, time.Since(start))
	})
}

func recoveryMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if err := recover(); err != nil {
				log.Printf("🚨 PANIC RECOVERED: %v", err)
				http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func main() {
	port := flag.Int("port", 8080, "Port to listen on")
	target := flag.String("target", "https://your-litellm-instance.com", "Target LiteLLM URL")
	flag.Parse()

	proxyServer, err := NewProxyServer(*target)
	if err != nil {
		log.Fatalf("Failed to initialize proxy: %v\n", err)
	}

	// Apply Middlewares (Recovery executes first, so it catches panics in Logging or the Proxy)
	handler := recoveryMiddleware(loggingMiddleware(proxyServer))

	addr := fmt.Sprintf("127.0.0.1:%d", *port)

	// Configure server with timeouts to prevent resource leaks (Slowloris attacks)
	srv := &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      15 * time.Minute, // High timeout because LLM generations can take several minutes
		IdleTimeout:       2 * time.Minute,
	}

	// Use fmt for static startup headers and instructions
	fmt.Printf("🚀 Claude Code UI Proxy running at http://%s\n", addr)
	fmt.Printf("📡 Forwarding traffic to: %s\n", proxyServer.targetURL.String())
	fmt.Printf("\nTo use this with Claude Code, run:\n")
	fmt.Printf("export ANTHROPIC_BASE_URL=http://%s/\n", addr)
	fmt.Printf("claude cowork\n")
	fmt.Printf("\nWaiting for requests...\n")

	// Use log for runtime server information
	log.SetFlags(log.Ldate | log.Ltime)

	// Start server in a background goroutine
	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Server startup failed: %v\n", err)
		}
	}()

	// Graceful Shutdown Setup
	quit := make(chan os.Signal, 1)
	// Listen for SIGINT (Ctrl+C) and SIGTERM (Docker/Kubernetes termination)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	// Block until a signal is received
	<-quit
	log.Println("Interrupt signal received, initiating graceful shutdown...")

	// Create a context with a timeout to allow active requests to finish (like long LLM generations)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		log.Fatalf("Server forced to shutdown abnormally: %v\n", err)
	}

	log.Println("Server exiting successfully")
}
