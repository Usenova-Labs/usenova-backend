package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/byoi-platform/cli/internal/client"
	"github.com/spf13/cobra"
)

var (
	agentURL string
	apiToken string
	output   string
)

func rootCmd() *cobra.Command {
	c := &cobra.Command{
		Use:     "usenova",
		Short:   "Usenova/BYOI CLI",
		Version: "dev",
	}
	c.PersistentFlags().StringVar(&agentURL, "agent-url", getEnv("USENOVA_AGENT_URL", "http://127.0.0.1:9090"), "Agent local API URL")
	c.PersistentFlags().StringVar(&apiToken, "token", getEnv("USENOVA_API_TOKEN", ""), "Agent API token (Bearer)")
	c.PersistentFlags().StringVarP(&output, "output", "o", "text", "Output format (text|json)")
	c.AddCommand(agentCmd(), deployCmd(), statusCmd(), logsCmd(), stopCmd(), removeCmd())
	return c
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

func newClient() *client.Client {
	return client.New(agentURL, apiToken)
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
	cl := newClient()
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
