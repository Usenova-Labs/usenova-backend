package controlplane

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sirupsen/logrus"
)

func TestClient_Register(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel) // Reduce noise in tests

	tests := []struct {
		name           string
		serverResponse func(w http.ResponseWriter, r *http.Request)
		expectError    bool
		expectedID     string
	}{
		{
			name: "successful registration",
			serverResponse: func(w http.ResponseWriter, r *http.Request) {
				// Verify request
				if r.Method != "POST" {
					t.Errorf("Expected POST, got %s", r.Method)
				}
				if r.URL.Path != "/api/v1/agents/register" {
					t.Errorf("Expected /api/v1/agents/register, got %s", r.URL.Path)
				}

				// Parse request body
				var req RegisterRequest
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					t.Errorf("Failed to decode request: %v", err)
				}

				// Verify request contains expected fields
				if req.Hostname == "" {
					t.Error("Expected hostname in request")
				}
				if req.Version == "" {
					t.Error("Expected version in request")
				}

				// Send success response
				w.Header().Set("Content-Type", "application/json")
				response := RegisterResponse{
					AgentID: "agent-12345",
					Message: "Registration successful",
				}
				json.NewEncoder(w).Encode(response)
			},
			expectError: false,
			expectedID:  "agent-12345",
		},
		{
			name: "server error",
			serverResponse: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusInternalServerError)
				w.Write([]byte(`{"error": "Internal server error"}`))
			},
			expectError: true,
		},
		{
			name: "invalid token",
			serverResponse: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusUnauthorized)
				w.Write([]byte(`{"error": "Invalid token"}`))
			},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create test server
			server := httptest.NewServer(http.HandlerFunc(tt.serverResponse))
			defer server.Close()

			// Create client
			client := NewClient(server.URL, "test-token", log)

			// Make request
			ctx := context.Background()
			response, err := client.Register(ctx, "test-agent-id", "test-version")

			// Check results
			if tt.expectError {
				if err == nil {
					t.Error("Expected error, got nil")
				}
			} else {
				if err != nil {
					t.Errorf("Unexpected error: %v", err)
				}
				if response.AgentID != tt.expectedID {
					t.Errorf("Expected agent ID %s, got %s", tt.expectedID, response.AgentID)
				}
			}
		})
	}
}

func TestClient_SendHeartbeat(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)

	t.Run("successful heartbeat", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Verify request
			if r.Method != "POST" {
				t.Errorf("Expected POST, got %s", r.Method)
			}

			expectedPath := "/api/v1/agents/test-agent-123/heartbeat"
			if r.URL.Path != expectedPath {
				t.Errorf("Expected %s, got %s", expectedPath, r.URL.Path)
			}

			// Verify Authorization header
			auth := r.Header.Get("Authorization")
			if auth != "Bearer test-token" {
				t.Errorf("Expected 'Bearer test-token', got '%s'", auth)
			}

			// Parse and verify request body
			var req HeartbeatRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Errorf("Failed to decode request: %v", err)
			}

			// Verify status fields
			if req.Status.Uptime <= 0 {
				t.Error("Expected positive uptime")
			}
			if req.Status.RunningServices < 0 {
				t.Error("Expected non-negative running services count")
			}

			// Send success response
			w.Header().Set("Content-Type", "application/json")
			response := HeartbeatResponse{
				Status:  "ok",
				Message: "Heartbeat received",
			}
			json.NewEncoder(w).Encode(response)
		}))
		defer server.Close()

		// Create client and set agent ID (simulate registration)
		client := NewClient(server.URL, "test-token", log)
		client.agentID = "test-agent-123"

		// Send heartbeat
		ctx := context.Background()
		err := client.SendHeartbeat(ctx)
		if err != nil {
			t.Errorf("Unexpected error: %v", err)
		}
	})

	t.Run("heartbeat without registration", func(t *testing.T) {
		client := NewClient("http://localhost:0", "test-token", log)
		// Don't set agentID - simulate unregistered agent

		ctx := context.Background()
		err := client.SendHeartbeat(ctx)
		if err == nil {
			t.Error("Expected error for unregistered agent")
		}
	})
}

func TestClient_Authentication(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Check Authorization header
		auth := r.Header.Get("Authorization")
		expectedAuth := "Bearer my-secret-token"
		if auth != expectedAuth {
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte(`{"error": "Invalid or missing token"}`))
			return
		}

		// Check User-Agent header
		userAgent := r.Header.Get("User-Agent")
		if userAgent == "" {
			t.Error("Expected User-Agent header")
		}

		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(RegisterResponse{
			AgentID: "success-agent",
			Message: "Authenticated successfully",
		})
	}))
	defer server.Close()

	client := NewClient(server.URL, "my-secret-token", log)
	ctx := context.Background()

	response, err := client.Register(ctx, "test-id", "test-version")
	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}
	if response.AgentID != "success-agent" {
		t.Errorf("Expected 'success-agent', got %s", response.AgentID)
	}
}

// TestClient_SessionTokenAfterRegister verifies the agent stores the token
// issued at registration and sends it on later requests.
func TestClient_SessionTokenAfterRegister(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)

	var registerAuth, commandAuth string
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/agents/register", func(w http.ResponseWriter, r *http.Request) {
		registerAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(RegisterResponse{
			AgentID:      "agent-42",
			SessionToken: "session-abc",
		})
	})
	mux.HandleFunc("/api/v1/agents/agent-42/commands/next", func(w http.ResponseWriter, r *http.Request) {
		commandAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(Command{
			ID:   "cmd-1",
			Type: CommandTypeStop,
		})
	})

	server := httptest.NewServer(mux)
	defer server.Close()

	client := NewClient(server.URL, "bootstrap-token", log)
	ctx := context.Background()

	if _, err := client.Register(ctx, "", "1.0.0"); err != nil {
		t.Fatalf("register: %v", err)
	}
	if _, err := client.FetchCommand(ctx); err != nil {
		t.Fatalf("fetch command: %v", err)
	}

	if registerAuth != "Bearer bootstrap-token" {
		t.Errorf("register Authorization = %q, want bootstrap token", registerAuth)
	}
	if client.GetAgentID() != "agent-42" {
		t.Errorf("agent ID = %q, want agent-42", client.GetAgentID())
	}
	if commandAuth != "Bearer session-abc" {
		t.Errorf("command Authorization = %q, want session token", commandAuth)
	}
}

// TestClient_FetchCommand verifies the agent asks the control plane for the
// next command of its own queue.
func TestClient_FetchCommand(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)

	var requestedPath string
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/agents/register", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(RegisterResponse{AgentID: "agent-7", SessionToken: "token"})
	})
	mux.HandleFunc("/api/v1/agents/agent-7/commands/next", func(w http.ResponseWriter, r *http.Request) {
		requestedPath = r.URL.Path
		if r.Method != http.MethodGet {
			t.Errorf("expected GET, got %s", r.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(Command{
			ID:      "cmd-1",
			Type:    CommandTypeDeploy,
			Payload: json.RawMessage(`{"name":"api","image":"nginx"}`),
		})
	})

	server := httptest.NewServer(mux)
	defer server.Close()

	client := NewClient(server.URL, "bootstrap-token", log)
	ctx := context.Background()

	if _, err := client.Register(ctx, "", "1.0.0"); err != nil {
		t.Fatalf("register: %v", err)
	}

	command, err := client.FetchCommand(ctx)
	if err != nil {
		t.Fatalf("fetch command: %v", err)
	}
	if command == nil {
		t.Fatal("expected a command")
	}
	if command.ID != "cmd-1" || command.Type != CommandTypeDeploy {
		t.Errorf("unexpected command: %+v", command)
	}
	if requestedPath != "/api/v1/agents/agent-7/commands/next" {
		t.Errorf("unexpected path %q", requestedPath)
	}
}

// TestClient_FetchCommandNoPending verifies that an empty response (204) means
// "no command pending" and not an error.
func TestClient_FetchCommandNoPending(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)

	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/agents/register", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(RegisterResponse{AgentID: "agent-7", SessionToken: "token"})
	})
	mux.HandleFunc("/api/v1/agents/agent-7/commands/next", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})

	server := httptest.NewServer(mux)
	defer server.Close()

	client := NewClient(server.URL, "bootstrap-token", log)
	ctx := context.Background()

	if _, err := client.Register(ctx, "", "1.0.0"); err != nil {
		t.Fatalf("register: %v", err)
	}

	command, err := client.FetchCommand(ctx)
	if err != nil {
		t.Fatalf("fetch command: %v", err)
	}
	if command != nil {
		t.Errorf("expected no command, got %+v", command)
	}
}

// TestClient_ReportCommandResult verifies the outcome of a command is
// reported back to the control plane.
func TestClient_ReportCommandResult(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)

	var reported CommandResult
	var reportedPath string
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/agents/register", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(RegisterResponse{AgentID: "agent-7", SessionToken: "token"})
	})
	mux.HandleFunc("/api/v1/agents/agent-7/commands/cmd-1/result", func(w http.ResponseWriter, r *http.Request) {
		reportedPath = r.URL.Path
		if err := json.NewDecoder(r.Body).Decode(&reported); err != nil {
			t.Errorf("decode result: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{}`))
	})

	server := httptest.NewServer(mux)
	defer server.Close()

	client := NewClient(server.URL, "bootstrap-token", log)
	ctx := context.Background()

	if _, err := client.Register(ctx, "", "1.0.0"); err != nil {
		t.Fatalf("register: %v", err)
	}

	err := client.ReportCommandResult(ctx, CommandResult{
		CommandID: "cmd-1",
		Status:    CommandStatusSucceeded,
		Message:   "service api deployed",
	})
	if err != nil {
		t.Fatalf("report result: %v", err)
	}

	if reportedPath != "/api/v1/agents/agent-7/commands/cmd-1/result" {
		t.Errorf("unexpected path %q", reportedPath)
	}
	if reported.Status != CommandStatusSucceeded || reported.CommandID != "cmd-1" {
		t.Errorf("unexpected result: %+v", reported)
	}
}
