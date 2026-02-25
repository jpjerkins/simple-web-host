package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// Allowed file extensions whitelist
var allowedExts = map[string]bool{
	".html":  true,
	".css":   true,
	".js":    true,
	".png":   true,
	".jpg":   true,
	".jpeg":  true,
	".gif":   true,
	".svg":   true,
	".webp":  true,
	".ico":   true,
	".json":  true,
	".txt":   true,
	".md":    true,
}

// Explicitly blocked extensions
var blockedExts = map[string]bool{
	".log": true,
}

type LogEntry struct {
	Timestamp string  `json:"timestamp"`
	Method    string  `json:"method"`
	Path      string  `json:"path"`
	Status    int     `json:"status"`
	Bytes     int64   `json:"bytes"`
	Duration  float64 `json:"duration_ms"`
}

// responseRecorder wraps http.ResponseWriter to capture status code and bytes
type responseRecorder struct {
	http.ResponseWriter
	statusCode   int
	bytesWritten int64
	wroteHeader  bool
}

func (rr *responseRecorder) WriteHeader(code int) {
	if !rr.wroteHeader {
		rr.statusCode = code
		rr.wroteHeader = true
		rr.ResponseWriter.WriteHeader(code)
	}
}

func (rr *responseRecorder) Write(p []byte) (int, error) {
	if !rr.wroteHeader {
		rr.WriteHeader(http.StatusOK)
	}
	n, err := rr.ResponseWriter.Write(p)
	rr.bytesWritten += int64(n)
	return n, err
}

type Server struct {
	wwwRoot        string
	logDir         string
	logger         *log.Logger
	location       *time.Location
	retentionHours int
}

func main() {
	wwwRoot := getEnv("WWW_ROOT", "/var/www")
	logDir := getEnv("LOG_DIR", "/var/log/app")
	retentionHours := 168 // 7 days

	// Load Central Time location
	loc, err := time.LoadLocation("America/Chicago")
	if err != nil {
		// Fallback: try constructing from fixed offset if timezone data unavailable
		// CST is UTC-6, CDT is UTC-5 (handles daylight saving via offset)
		loc = time.FixedZone("America/Chicago", -6*60*60)
		log.Printf("Warning: Could not load America/Chicago timezone, using fixed offset: %v", err)
	}

	server := &Server{
		wwwRoot:        wwwRoot,
		logDir:         logDir,
		location:       loc,
		retentionHours: retentionHours,
	}

	// Ensure log directory exists
	if err := os.MkdirAll(logDir, 0755); err != nil {
		log.Fatalf("Failed to create log directory: %v", err)
	}

	// Clean up old logs on startup
	server.cleanupOldLogs()

	// Start cleanup goroutine
	go server.periodicCleanup()

	mux := http.NewServeMux()
	mux.HandleFunc("/", server.handleRequest)

	addr := ":8080"
	log.Printf("Starting static file server on %s", addr)
	log.Printf("WWW root: %s", wwwRoot)
	log.Printf("Log directory: %s", logDir)
	log.Printf("Timezone: %s", loc.String())

	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}

func (s *Server) handleRequest(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	path := r.URL.Path
	
	// Wrap response writer to capture status
	wrapped := &responseRecorder{ResponseWriter: w, statusCode: http.StatusOK}

	// Log the request at the end
	defer func() {
		duration := time.Since(start).Seconds() * 1000
		entry := LogEntry{
			Timestamp: time.Now().In(s.location).Format(time.RFC3339),
			Method:    r.Method,
			Path:      path,
			Status:    wrapped.statusCode,
			Bytes:     wrapped.bytesWritten,
			Duration:  duration,
		}
		s.logRequest(entry)
	}()

	// Only allow GET and HEAD
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(wrapped, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	// Sanitize and validate path
	cleanPath, err := s.sanitizePath(path)
	if err != nil {
		http.Error(wrapped, "Forbidden", http.StatusForbidden)
		return
	}

	// Check if file exists and is not a directory
	info, err := os.Stat(cleanPath)
	if err != nil {
		if os.IsNotExist(err) {
			http.Error(wrapped, "Not Found", http.StatusNotFound)
		} else {
			http.Error(wrapped, "Internal Server Error", http.StatusInternalServerError)
		}
		return
	}

	if info.IsDir() {
		http.Error(wrapped, "Forbidden", http.StatusForbidden)
		return
	}

	// Validate extension
	ext := strings.ToLower(filepath.Ext(cleanPath))
	if blockedExts[ext] {
		http.Error(wrapped, "Forbidden", http.StatusForbidden)
		return
	}
	if !allowedExts[ext] {
		http.Error(wrapped, "Forbidden", http.StatusForbidden)
		return
	}

	// Serve the file
	http.ServeFile(wrapped, r, cleanPath)
}

func (s *Server) sanitizePath(p string) (string, error) {
	// Remove leading slash
	p = strings.TrimPrefix(p, "/")

	// Reject empty paths (would be directory)
	if p == "" || p == "." {
		return "", fmt.Errorf("directory access not allowed")
	}

	// Check for any path separators - only flat structure allowed
	if strings.Contains(p, "/") || strings.Contains(p, "\\") {
		return "", fmt.Errorf("subdirectories not allowed")
	}

	// Check for directory traversal attempts
	if strings.Contains(p, "..") {
		return "", fmt.Errorf("directory traversal detected")
	}

	// Clean the path
	clean := filepath.Clean(p)

	// Double-check after cleaning
	if strings.Contains(clean, "..") {
		return "", fmt.Errorf("directory traversal detected after clean")
	}

	// Join with www root
	fullPath := filepath.Join(s.wwwRoot, clean)

	// Verify the path is still within www root (final safety check)
	absWwwRoot, err := filepath.Abs(s.wwwRoot)
	if err != nil {
		return "", fmt.Errorf("failed to get absolute www root: %w", err)
	}
	absPath, err := filepath.Abs(fullPath)
	if err != nil {
		return "", fmt.Errorf("failed to get absolute path: %w", err)
	}

	if !strings.HasPrefix(absPath, absWwwRoot+string(filepath.Separator)) && absPath != absWwwRoot {
		return "", fmt.Errorf("path escapes www root")
	}

	return fullPath, nil
}

func (s *Server) logRequest(entry LogEntry) {
	logFile := s.currentLogFile()
	
	f, err := os.OpenFile(logFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		log.Printf("Failed to open log file %s: %v", logFile, err)
		return
	}
	defer f.Close()

	encoder := json.NewEncoder(f)
	if err := encoder.Encode(entry); err != nil {
		log.Printf("Failed to encode log entry: %v", err)
	}
}

func (s *Server) currentLogFile() string {
	now := time.Now().In(s.location)
	filename := now.Format("2006-01-02T15") + ".log"
	return filepath.Join(s.logDir, filename)
}

func (s *Server) cleanupOldLogs() {
	entries, err := os.ReadDir(s.logDir)
	if err != nil {
		log.Printf("Failed to read log directory for cleanup: %v", err)
		return
	}

	cutoff := time.Now().In(s.location).Add(-time.Duration(s.retentionHours) * time.Hour)

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		name := entry.Name()
		if !strings.HasSuffix(name, ".log") {
			continue
		}

		// Parse timestamp from filename
		t, err := time.ParseInLocation("2006-01-02T15.log", name, s.location)
		if err != nil {
			continue // Skip files that don't match pattern
		}

		if t.Before(cutoff) {
			path := filepath.Join(s.logDir, name)
			if err := os.Remove(path); err != nil {
				log.Printf("Failed to remove old log %s: %v", path, err)
			} else {
				log.Printf("Cleaned up old log: %s", name)
			}
		}
	}
}

func (s *Server) periodicCleanup() {
	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()

	for range ticker.C {
		s.cleanupOldLogs()
	}
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

// Prevent debug endpoints
func init() {
	// Disable profiling endpoints that Go's default mux might expose
	http.DefaultServeMux = http.NewServeMux()
	
	// Restrict file creation permissions
	umask := syscall.Umask(0)
	syscall.Umask(umask)
}
