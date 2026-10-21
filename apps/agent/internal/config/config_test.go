package config

import (
	"os"
	"testing"
	"time"
)

func TestLoad(t *testing.T) {
	// Clean up environment
	defer func() {
		os.Unsetenv("CONTROL_PLANE_URL")
		os.Unsetenv("AGENT_TOKEN")
		os.Unsetenv("AGENT_ID")
		os.Unsetenv("HEARTBEAT_INTERVAL")
		os.Unsetenv("COMMAND_POLL_INTERVAL")
	}()

	t.Run("missing required config", func(t *testing.T) {
		_, err := Load("test-version")
		if err == nil {
			t.Error("Expected error for missing CONTROL_PLANE_URL")
		}
	})

	t.Run("missing agent token", func(t *testing.T) {
		os.Setenv("CONTROL_PLANE_URL", "http://localhost:8080")
		_, err := Load("test-version")
		if err == nil {
			t.Error("Expected error for missing AGENT_TOKEN")
		}
	})

	t.Run("valid minimal config", func(t *testing.T) {
		os.Setenv("CONTROL_PLANE_URL", "http://localhost:8080")
		os.Setenv("AGENT_TOKEN", "test-token")

		cfg, err := Load("test-version")
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}

		if cfg.ControlPlaneURL != "http://localhost:8080" {
			t.Errorf("Expected ControlPlaneURL to be 'http://localhost:8080', got %s", cfg.ControlPlaneURL)
		}

		if cfg.AgentToken != "test-token" {
			t.Errorf("Expected AgentToken to be 'test-token', got %s", cfg.AgentToken)
		}

		if cfg.HeartbeatInterval != 30*time.Second {
			t.Errorf("Expected default HeartbeatInterval to be 30s, got %v", cfg.HeartbeatInterval)
		}

		if cfg.AgentID == "" {
			t.Error("Expected AgentID to be generated")
		}

		if cfg.Version != "test-version" {
			t.Errorf("Expected Version to be 'test-version', got %s", cfg.Version)
		}
	})

	t.Run("custom heartbeat interval", func(t *testing.T) {
		os.Setenv("CONTROL_PLANE_URL", "http://localhost:8080")
		os.Setenv("AGENT_TOKEN", "test-token")
		os.Setenv("HEARTBEAT_INTERVAL", "1m")

		cfg, err := Load("test-version")
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}

		if cfg.HeartbeatInterval != time.Minute {
			t.Errorf("Expected HeartbeatInterval to be 1m, got %v", cfg.HeartbeatInterval)
		}
	})

	t.Run("heartbeat interval as seconds", func(t *testing.T) {
		os.Setenv("CONTROL_PLANE_URL", "http://localhost:8080")
		os.Setenv("AGENT_TOKEN", "test-token")
		os.Setenv("HEARTBEAT_INTERVAL", "45")

		cfg, err := Load("test-version")
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}

		if cfg.HeartbeatInterval != 45*time.Second {
			t.Errorf("Expected HeartbeatInterval to be 45s, got %v", cfg.HeartbeatInterval)
		}
	})

	t.Run("invalid heartbeat interval", func(t *testing.T) {
		os.Setenv("CONTROL_PLANE_URL", "http://localhost:8080")
		os.Setenv("AGENT_TOKEN", "test-token")
		os.Setenv("HEARTBEAT_INTERVAL", "invalid")

		_, err := Load("test-version")
		if err == nil {
			t.Error("Expected error for invalid HEARTBEAT_INTERVAL")
		}
	})

	t.Run("heartbeat interval bounds", func(t *testing.T) {
		os.Setenv("CONTROL_PLANE_URL", "http://localhost:8080")
		os.Setenv("AGENT_TOKEN", "test-token")

		// Too short
		os.Setenv("HEARTBEAT_INTERVAL", "1s")
		_, err := Load("test-version")
		if err == nil {
			t.Error("Expected error for heartbeat interval too short")
		}

		// Too long
		os.Setenv("HEARTBEAT_INTERVAL", "15m")
		_, err = Load("test-version")
		if err == nil {
			t.Error("Expected error for heartbeat interval too long")
		}
	})

	t.Run("default command poll interval", func(t *testing.T) {
		os.Setenv("CONTROL_PLANE_URL", "http://localhost:8080")
		os.Setenv("AGENT_TOKEN", "test-token")
		os.Unsetenv("HEARTBEAT_INTERVAL")
		os.Unsetenv("COMMAND_POLL_INTERVAL")

		cfg, err := Load("test-version")
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}

		if cfg.CommandPollInterval != 10*time.Second {
			t.Errorf("Expected default CommandPollInterval to be 10s, got %v", cfg.CommandPollInterval)
		}
	})

	t.Run("custom command poll interval", func(t *testing.T) {
		os.Setenv("CONTROL_PLANE_URL", "http://localhost:8080")
		os.Setenv("AGENT_TOKEN", "test-token")
		os.Unsetenv("HEARTBEAT_INTERVAL")
		os.Setenv("COMMAND_POLL_INTERVAL", "2s")

		cfg, err := Load("test-version")
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}

		if cfg.CommandPollInterval != 2*time.Second {
			t.Errorf("Expected CommandPollInterval to be 2s, got %v", cfg.CommandPollInterval)
		}
	})

	t.Run("invalid command poll interval", func(t *testing.T) {
		os.Setenv("CONTROL_PLANE_URL", "http://localhost:8080")
		os.Setenv("AGENT_TOKEN", "test-token")
		os.Unsetenv("HEARTBEAT_INTERVAL")
		os.Setenv("COMMAND_POLL_INTERVAL", "not-a-duration")

		if _, err := Load("test-version"); err == nil {
			t.Error("Expected error for invalid COMMAND_POLL_INTERVAL")
		}
	})

	t.Run("command poll interval too short", func(t *testing.T) {
		os.Setenv("CONTROL_PLANE_URL", "http://localhost:8080")
		os.Setenv("AGENT_TOKEN", "test-token")
		os.Unsetenv("HEARTBEAT_INTERVAL")
		os.Setenv("COMMAND_POLL_INTERVAL", "100ms")

		if _, err := Load("test-version"); err == nil {
			t.Error("Expected error for command poll interval too short")
		}
	})
}
