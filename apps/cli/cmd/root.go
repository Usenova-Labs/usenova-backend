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
	c.PersistentFlags().StringVar(&agentURL, "agent-url", getEnv("USENOVA_AGENT_URL", "http://127.0.0.1:9090"), "Agent local API URL")
	c.PersistentFlags().StringVar(&controlPlaneURL, "control-plane-url", getEnv("USENOVA_CONTROL_PLANE_URL", "http://127.0.0.1:8080"), "Control plane API URL")
	c.PersistentFlags().StringVar(&apiToken, "token", getEnv("USENOVA_API_TOKEN", ""), "Agent API token (Bearer)")
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
			fmt.Fprintln(os.Stderr, "warning: no API token configured (--token or USENOVA_API_TOKEN); the agent requires a bearer token and will reject requests")
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

func getEnv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func newClient() (*client.Client, error) {
	return client.New(agentURL, apiToken, timeout)
}

func newControlPlaneClient() *controlplane.Client {
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
