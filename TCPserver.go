// Basic Concurrent HTTP Server
// 
// This program demonstrates a simple TCP server that handles HTTP requests
// concurrently using Go's goroutines. Each incoming connection is processed
// in its own goroutine, allowing multiple clients to connect simultaneously.

package main

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Server represents our HTTP server with configuration
type Server struct {
	address     string
	listener    net.Listener
	shutdown    chan struct{}
	connections sync.WaitGroup
}

// NewServer creates a new server instance
func NewServer(address string) *Server {
	return &Server{
		address:  address,
		shutdown: make(chan struct{}),
	}
}

// handleConnection processes an individual client connection with improvements
func (s *Server) handleConnection(conn net.Conn) {
	defer s.connections.Done() // Signal completion when done
	defer conn.Close()         // Ensure connection is always closed
	
	// Set connection timeout to prevent hanging connections
	conn.SetDeadline(time.Now().Add(30 * time.Second))
	
	log.Printf("New connection from: %s", conn.RemoteAddr())
	
	// Use bufio.Reader for better parsing
	reader := bufio.NewReader(conn)
	
	// Read the first line to get HTTP method and path
	firstLine, err := reader.ReadLine()
	if err != nil {
		log.Printf("Error reading request from %s: %v", conn.RemoteAddr(), err)
		return
	}
	
	// Parse HTTP request line (e.g., "GET / HTTP/1.1")
	parts := strings.Fields(string(firstLine))
	if len(parts) < 3 {
		s.sendBadRequest(conn)
		return
	}
	
	method := parts[0]
	path := parts[1]
	
	// Read headers (basic parsing)
	headers := make(map[string]string)
	for {
		line, err := reader.ReadLine()
		if err != nil || len(line) == 0 {
			break // End of headers
		}
		
		headerLine := string(line)
		if colonIndex := strings.Index(headerLine, ":"); colonIndex > 0 {
			key := strings.TrimSpace(headerLine[:colonIndex])
			value := strings.TrimSpace(headerLine[colonIndex+1:])
			headers[strings.ToLower(key)] = value
		}
	}
	
	// Route the request
	s.routeRequest(conn, method, path, headers)
	
	log.Printf("Connection closed for: %s", conn.RemoteAddr())
}

// routeRequest handles different endpoints
func (s *Server) routeRequest(conn net.Conn, method, path string, headers map[string]string) {
	switch {
	case path == "/" && method == "GET":
		s.handleRoot(conn)
	case path == "/health" && method == "GET":
		s.handleHealth(conn)
	case strings.HasPrefix(path, "/slow") && method == "GET":
		s.handleSlow(conn)
	case path == "/headers" && method == "GET":
		s.handleHeaders(conn, headers)
	default:
		s.handleNotFound(conn)
	}
}

// Route handlers
func (s *Server) handleRoot(conn net.Conn) {
	response := "HTTP/1.1 200 OK\r\n" +
		"Content-Type: text/plain\r\n" +
		"Content-Length: 13\r\n" +
		"\r\n" +
		"Hello, World!"
	conn.Write([]byte(response))
}

func (s *Server) handleHealth(conn net.Conn) {
	response := "HTTP/1.1 200 OK\r\n" +
		"Content-Type: application/json\r\n" +
		"Content-Length: 15\r\n" +
		"\r\n" +
		`{"status":"ok"}`
	conn.Write([]byte(response))
}

func (s *Server) handleSlow(conn net.Conn) {
	// Simulate slow processing
	time.Sleep(5 * time.Second)
	response := "HTTP/1.1 200 OK\r\n" +
		"Content-Type: text/plain\r\n" +
		"Content-Length: 19\r\n" +
		"\r\n" +
		"Slow response done!"
	conn.Write([]byte(response))
}

func (s *Server) handleHeaders(conn net.Conn, headers map[string]string) {
	body := "Received headers:\n"
	for key, value := range headers {
		body += fmt.Sprintf("%s: %s\n", key, value)
	}
	
	response := fmt.Sprintf("HTTP/1.1 200 OK\r\n"+
		"Content-Type: text/plain\r\n"+
		"Content-Length: %d\r\n"+
		"\r\n"+
		"%s", len(body), body)
	conn.Write([]byte(response))
}

func (s *Server) handleNotFound(conn net.Conn) {
	response := "HTTP/1.1 404 Not Found\r\n" +
		"Content-Type: text/plain\r\n" +
		"Content-Length: 9\r\n" +
		"\r\n" +
		"Not Found"
	conn.Write([]byte(response))
}

func (s *Server) sendBadRequest(conn net.Conn) {
	response := "HTTP/1.1 400 Bad Request\r\n" +
		"Content-Type: text/plain\r\n" +
		"Content-Length: 11\r\n" +
		"\r\n" +
		"Bad Request"
	conn.Write([]byte(response))
}

// Start begins accepting connections
func (s *Server) Start() error {
	var err error
	s.listener, err = net.Listen("tcp", s.address)
	if err != nil {
		return fmt.Errorf("failed to start server: %w", err)
	}
	
	log.Printf("Server starting on %s", s.address)
	
	// Accept connections in a separate goroutine
	go func() {
		for {
			select {
			case <-s.shutdown:
				return
			default:
				conn, err := s.listener.Accept()
				if err != nil {
					// Check if we're shutting down
					select {
					case <-s.shutdown:
						return
					default:
						log.Printf("Failed to accept connection: %v", err)
						continue
					}
				}
				
				s.connections.Add(1)
				go s.handleConnection(conn)
			}
		}
	}()
	
	return nil
}

// Stop gracefully shuts down the server
func (s *Server) Stop(ctx context.Context) error {
	log.Println("Shutting down server...")
	
	// Signal shutdown
	close(s.shutdown)
	
	// Close listener
	if s.listener != nil {
		s.listener.Close()
	}
	
	// Wait for connections to finish or timeout
	done := make(chan struct{})
	go func() {
		s.connections.Wait()
		close(done)
	}()
	
	select {
	case <-done:
		log.Println("All connections closed")
	case <-ctx.Done():
		log.Println("Shutdown timeout, forcing close")
	}
	
	return nil
}

func main() {
	server := NewServer(":3000")
	
	// Start server
	if err := server.Start(); err != nil {
		log.Fatal(err)
	}
	
	// Wait for interrupt signal for graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan
	
	// shutdown with 30-second timeout
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	
	if err := server.Stop(ctx); err != nil {
		log.Printf("Server shutdown error: %v", err)
	}
	
	log.Println("Server stopped")
}
