package cmd

import (
	"os"
	"testing"
)

// allEnvVars is the complete set of env vars the flag defaults read from.
// Tests set and clear these to verify the preference order.
var allEnvVars = []string{
	"NOVA_AGENT_URL",
	"USENOVA_AGENT_URL",
	"NOVA_CONTROL_PLANE_URL",
	"USENOVA_CONTROL_PLANE_URL",
	"NOVA_API_TOKEN",
	"USENOVA_API_TOKEN",
}

func clearEnv(t *testing.T) {
	t.Helper()
	for _, k := range allEnvVars {
		t.Setenv(k, "")
		os.Unsetenv(k)
	}
}

func TestGetEnvMulti(t *testing.T) {
	t.Run("returns default when nothing set", func(t *testing.T) {
		clearEnv(t)
		got := getEnvMulti("NOVA_AGENT_URL", "USENOVA_AGENT_URL", "http://default:9090")
		if got != "http://default:9090" {
			t.Errorf("got %q, want default", got)
		}
	})

	t.Run("first name wins", func(t *testing.T) {
		t.Setenv("NOVA_AGENT_URL", "http://nova:9090")
		t.Setenv("USENOVA_AGENT_URL", "http://usenova:9090")
		got := getEnvMulti("NOVA_AGENT_URL", "USENOVA_AGENT_URL", "http://default:9090")
		if got != "http://nova:9090" {
			t.Errorf("got %q, want http://nova:9090 (NOVA_ preferred over USENOVA_)", got)
		}
	})

	t.Run("falls back to second name", func(t *testing.T) {
		t.Setenv("USENOVA_AGENT_URL", "http://usenova:9090")
		got := getEnvMulti("NOVA_AGENT_URL", "USENOVA_AGENT_URL", "http://default:9090")
		if got != "http://usenova:9090" {
			t.Errorf("got %q, want http://usenova:9090 (USENOVA_ fallback)", got)
		}
	})

	t.Run("falls back to default when first is empty", func(t *testing.T) {
		t.Setenv("NOVA_AGENT_URL", "")
		t.Setenv("USENOVA_AGENT_URL", "")
		got := getEnvMulti("NOVA_AGENT_URL", "USENOVA_AGENT_URL", "http://default:9090")
		if got != "http://default:9090" {
			t.Errorf("got %q, want default when env values are empty", got)
		}
	})
}

func TestFlagDefaults(t *testing.T) {
	// Save and restore the package-level flag variables so concurrent
	// tests don't see stale values.
	savedAgent := agentURL
	savedCP := controlPlaneURL
	savedToken := apiToken
	defer func() {
		agentURL = savedAgent
		controlPlaneURL = savedCP
		apiToken = savedToken
	}()

	t.Run("defaults when no env set", func(t *testing.T) {
		clearEnv(t)
		c := rootCmd()
		agentFlag := c.PersistentFlags().Lookup("agent-url")
		cpFlag := c.PersistentFlags().Lookup("control-plane-url")
		tokenFlag := c.PersistentFlags().Lookup("token")

		if agentFlag.DefValue != "http://127.0.0.1:9090" {
			t.Errorf("agent-url default = %q, want http://127.0.0.1:9090", agentFlag.DefValue)
		}
		if cpFlag.DefValue != "http://127.0.0.1:8080" {
			t.Errorf("control-plane-url default = %q, want http://127.0.0.1:8080", cpFlag.DefValue)
		}
		if tokenFlag.DefValue != "" {
			t.Errorf("token default = %q, want empty", tokenFlag.DefValue)
		}
	})

	t.Run("NOVA_ preferred over USENOVA_", func(t *testing.T) {
		clearEnv(t)
		t.Setenv("NOVA_AGENT_URL", "http://nova-agent:9090")
		t.Setenv("USENOVA_AGENT_URL", "http://usenova-agent:9090")
		t.Setenv("NOVA_CONTROL_PLANE_URL", "http://nova-cp:8080")
		t.Setenv("USENOVA_CONTROL_PLANE_URL", "http://usenova-cp:8080")
		t.Setenv("NOVA_API_TOKEN", "nova-token")
		t.Setenv("USENOVA_API_TOKEN", "usenova-token")

		c := rootCmd()
		agentFlag := c.PersistentFlags().Lookup("agent-url")
		cpFlag := c.PersistentFlags().Lookup("control-plane-url")
		tokenFlag := c.PersistentFlags().Lookup("token")

		if agentFlag.DefValue != "http://nova-agent:9090" {
			t.Errorf("agent-url default = %q, want http://nova-agent:9090 (NOVA_ preferred)", agentFlag.DefValue)
		}
		if cpFlag.DefValue != "http://nova-cp:8080" {
			t.Errorf("control-plane-url default = %q, want http://nova-cp:8080 (NOVA_ preferred)", cpFlag.DefValue)
		}
		if tokenFlag.DefValue != "nova-token" {
			t.Errorf("token default = %q, want nova-token (NOVA_ preferred)", tokenFlag.DefValue)
		}
	})

	t.Run("falls back to USENOVA_ when NOVA_ unset", func(t *testing.T) {
		clearEnv(t)
		t.Setenv("USENOVA_AGENT_URL", "http://usenova-agent:9090")
		t.Setenv("USENOVA_CONTROL_PLANE_URL", "http://usenova-cp:8080")
		t.Setenv("USENOVA_API_TOKEN", "usenova-token")

		c := rootCmd()
		agentFlag := c.PersistentFlags().Lookup("agent-url")
		cpFlag := c.PersistentFlags().Lookup("control-plane-url")
		tokenFlag := c.PersistentFlags().Lookup("token")

		if agentFlag.DefValue != "http://usenova-agent:9090" {
			t.Errorf("agent-url default = %q, want http://usenova-agent:9090 (USENOVA_ fallback)", agentFlag.DefValue)
		}
		if cpFlag.DefValue != "http://usenova-cp:8080" {
			t.Errorf("control-plane-url default = %q, want http://usenova-cp:8080 (USENOVA_ fallback)", cpFlag.DefValue)
		}
		if tokenFlag.DefValue != "usenova-token" {
			t.Errorf("token default = %q, want usenova-token (USENOVA_ fallback)", tokenFlag.DefValue)
		}
	})

	t.Run("flag overrides env", func(t *testing.T) {
		clearEnv(t)
		t.Setenv("NOVA_API_TOKEN", "env-token")

		c := rootCmd()
		// Simulate --token cli-token on the command line.
		if err := c.PersistentFlags().Set("token", "cli-token"); err != nil {
			t.Fatalf("failed to set --token: %v", err)
		}
		if apiToken != "cli-token" {
			t.Errorf("apiToken = %q, want cli-token (flag must override env)", apiToken)
		}
	})
}
