package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/byoi-platform/cli/internal/client"
	"github.com/byoi-platform/cli/internal/controlplane"
	"github.com/spf13/cobra"
)

var (
	agentURL        string
	controlPlaneURL string
	apiToken        string
	output          string
	timeout         time.Duration
)

func rootCmd() *cobra.Command {
	c := &cobra.Command{
		Use:     "nova",
		Short:   "Nova CLI",
		Version: "dev",
	}
	c.PersistentFlags().StringVar(&agentURL, "agent-url", getEnvMulti("NOVA_AGENT_URL", "USENOVA_AGENT_URL", "http://127.0.0.1:9090"), "Agent local API URL")
	c.PersistentFlags().StringVar(&controlPlaneURL, "control-plane-url", getEnvMulti("NOVA_CONTROL_PLANE_URL", "USENOVA_CONTROL_PLANE_URL", "http://127.0.0.1:8080"), "Control plane API URL")
	c.PersistentFlags().StringVar(&apiToken, "token", getEnvMulti("NOVA_API_TOKEN", "USENOVA_API_TOKEN", ""), "Agent API token (Bearer)")
	c.PersistentFlags().StringVarP(&output, "output", "o", "text", "Output format (text|json)")
	c.PersistentFlags().DurationVar(&timeout, "timeout", client.DefaultTimeout, "HTTP request timeout (a deploy that exceeds it is reconciled via service status)")
	// Fail-soft early: an empty token only surfaces as a 401 from the agent
	// today. Warn at parse time so users see the misconfiguration before any
	// request is attempted.
	c.PersistentPreRunE = func(cmd *cobra.Command, args []string) error {
		if cmdHasAncestor(cmd, "stellar") {
			return nil
		}
		if apiToken == "" {
			fmt.Fprintln(os.Stderr, "warning: no API token configured (--token, NOVA_API_TOKEN, or USENOVA_API_TOKEN); the agent requires a bearer token and will reject requests")
		}
		return nil
	}
	c.AddCommand(agentCmd(), deployCmd(), statusCmd(), logsCmd(), stopCmd(), removeCmd(), stellarCmd())
	return c
}

// cmdHasAncestor reports whether name appears anywhere in the command's
// ancestor chain (used to scope persistent-flag warnings to a command tree).
func cmdHasAncestor(cmd *cobra.Command, name string) bool {
	for p := cmd; p != nil; p = p.Parent() {
		if p.Name() == name {
			return true
		}
	}
	return false
}

func Root() *cobra.Command {
	return rootCmd()
}

// getEnvMulti returns the first non-empty environment variable in names, or
// def when none are set. The lookup order encodes the preference:
// NOVA_* before USENOVA_* before the default.
func getEnvMulti(names ...string) string {
	for _, n := range names {
		if v := os.Getenv(n); v != "" {
			return v
		}
	}
	return names[len(names)-1]
}

func newClient() (*client.Client, error) {
	return client.New(agentURL, apiToken, timeout)
}

func newControlPlaneClient() (*controlplane.Client, error) {
	return controlplane.New(controlPlaneURL, apiToken, controlplane.DefaultTimeout)
}

func printJSON(v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(b))
	return nil
}

func maybeJSON(v any, err error) error {
	if err != nil {
		return err
	}
	if output == "json" {
		return printJSON(v)
	}
	return nil
}

func run(ctx context.Context, f func(*client.Client, context.Context) (any, error)) error {
	cl, err := newClient()
	if err != nil {
		return err
	}
	v, err := f(cl, ctx)
	if output == "json" {
		if err != nil {
			return err
		}
		return printJSON(v)
	}
	if err != nil {
		return err
	}
	// default: if non-nil render simple; commands may override
	_ = v
	return nil
}
