// Mock Control Plane server for testing - run with: go run test/mock-control-plane.go
package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/byoi-platform/agent/internal/controlplane"
)

func main() {
	fmt.Println("🚀 Starting Mock Control Plane Server...")
	fmt.Println("   URL: http://localhost:8080")
	fmt.Println("   Endpoints:")
	fmt.Println("     POST /api/v1/agents/register")
	fmt.Println("     POST /api/v1/agents/{id}/heartbeat")
	fmt.Println()

	http.HandleFunc("/api/v1/agents/register", handleRegister)
	http.HandleFunc("/api/v1/agents/", handleAgentRequests)
	http.HandleFunc("/", handleRoot)

	fmt.Println("🎯 To test the agent:")
	fmt.Println("   export CONTROL_PLANE_URL=http://localhost:8080")
	fmt.Println("   export AGENT_TOKEN=test-token-123")
	fmt.Println("   go run main.go")
	fmt.Println()

	log.Fatal(http.ListenAndServe(":8080", nil))
}

func handleRoot(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	response := map[string]interface{}{
		"service": "Mock Usenova Control Plane",
		"version": "1.0.0",
		"time":    time.Now().Format(time.RFC3339),
		"endpoints": []string{
			"POST /api/v1/agents/register",
			"POST /api/v1/agents/{id}/heartbeat",
		},
	}
	json.NewEncoder(w).Encode(response)
}

func handleRegister(w http.ResponseWriter, r *http.Request) {
	log.Printf("📝 Registration request from %s", r.RemoteAddr)

	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Check Authorization header
	auth := r.Header.Get("Authorization")
	if auth != "Bearer test-token-123" {
		log.Printf("❌ Invalid token: %s", auth)
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]string{
			"error": "Invalid or missing authorization token",
		})
		return
	}

	// Parse request
	var req controlplane.RegisterRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		log.Printf("❌ Invalid request body: %v", err)
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	// Log registration details
	log.Printf("✅ Agent Registration:")
	log.Printf("   Agent ID: %s", req.AgentID)
	log.Printf("   Hostname: %s", req.Hostname)
	log.Printf("   Version: %s", req.Version)
	log.Printf("   Docker Available: %v", req.Capabilities.Docker)

	// Send success response
	w.Header().Set("Content-Type", "application/json")
	response := controlplane.RegisterResponse{
		AgentID: req.AgentID, // Echo back the same ID
		Message: fmt.Sprintf("Agent %s registered successfully", req.Hostname),
	}
	json.NewEncoder(w).Encode(response)
}

func handleAgentRequests(w http.ResponseWriter, r *http.Request) {
	// Parse agent ID from URL path
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/agents/")
	parts := strings.Split(path, "/")

	if len(parts) != 2 || parts[1] != "heartbeat" {
		http.NotFound(w, r)
		return
	}

	agentID := parts[0]

	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Check Authorization
	auth := r.Header.Get("Authorization")
	if auth != "Bearer test-token-123" {
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]string{
			"error": "Invalid authorization token",
		})
		return
	}

	// Parse heartbeat
	var req controlplane.HeartbeatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	// Log heartbeat (less verbose)
	uptimeSeconds := int(req.Status.Uptime.Seconds())
	log.Printf("💓 Heartbeat from %s: uptime=%ds, services=%d",
		agentID, uptimeSeconds, req.Status.RunningServices)

	// Send success response
	w.Header().Set("Content-Type", "application/json")
	response := controlplane.HeartbeatResponse{
		Status:  "ok",
		Message: "Heartbeat received",
	}
	json.NewEncoder(w).Encode(response)
}
