package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

func agentCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "agent",
		Short: "Agent operations",
	}
	c.AddCommand(&cobra.Command{
		Use:   "status",
		Short: "Ping local agent API",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			cl := newClient()
			lr, err := cl.ListServices(ctx)
			if err != nil {
				return err
			}
			if output == "json" {
				return printJSON(lr)
			}
			fmt.Printf("Agent API: %s (ok)\n", agentURL)
			fmt.Printf("Running services: %d\n", lr.Running)
			return nil
		},
	})
	return c
}

func deployCmd() *cobra.Command {
	var name, image, restart string
	var ports []string
	var envs []string
	var healthPath string
	var healthPort int
	c := &cobra.Command{
		Use:   "deploy",
		Short: "Deploy a service to local agent",
		RunE: func(cmd *cobra.Command, args []string) error {
			if name == "" || image == "" {
				return fmt.Errorf("--name and --image required")
			}
			spec := map[string]any{
				"name":  name,
				"image": image,
			}
			if restart != "" {
				spec["restart_policy"] = restart
			}
			if len(ports) > 0 {
				pm := map[string]string{}
				for _, p := range ports {
					parts := strings.Split(p, ":")
					if len(parts) != 2 {
						return fmt.Errorf("invalid --port %q (expect host:container)", p)
					}
					pm[parts[0]] = parts[1]
				}
				spec["ports"] = pm
			}
			if len(envs) > 0 {
				spec["env"] = envs
			}
			if healthPath != "" || healthPort > 0 {
				hc := map[string]any{}
				if healthPath != "" {
					hc["path"] = healthPath
				}
				if healthPort > 0 {
					hc["port"] = healthPort
					hc["type"] = "http"
				}
				if _, ok := hc["type"]; !ok {
					hc["type"] = "tcp"
				}
				spec["health_check"] = hc
			}
			ctx := context.Background()
			cl := newClient()
			res, err := cl.Deploy(ctx, spec)
			if err != nil {
				return err
			}
			if output == "json" {
				return printJSON(res)
			}
			fmt.Printf("Deployed %s\n", name)
			return nil
		},
	}
	c.Flags().StringVar(&name, "name", "", "service name")
	c.Flags().StringVar(&image, "image", "", "docker image")
	c.Flags().StringVar(&restart, "restart", "", "restart policy (no|always|on-failure|unless-stopped)")
	c.Flags().StringSliceVar(&ports, "port", nil, "port mapping host:container (repeatable)")
	c.Flags().StringSliceVar(&envs, "env", nil, "env KEY=VALUE (repeatable)")
	c.Flags().StringVar(&healthPath, "health-path", "", "health check HTTP path")
	c.Flags().IntVar(&healthPort, "health-port", 0, "health check port (container or host mapping target)")
	c.MarkFlagRequired("name")
	c.MarkFlagRequired("image")
	return c
}

func statusCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "status [name]",
		Short: "List services or get one service",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			cl := newClient()
			if len(args) == 0 {
				lr, err := cl.ListServices(ctx)
				if err != nil {
					return err
				}
				if output == "json" {
					return printJSON(lr)
				}
				fmt.Printf("Running: %d\n", lr.Running)
				for i, s := range lr.Services {
					b, _ := json.Marshal(s)
					if i > 0 {
						fmt.Println("---")
					}
					fmt.Println(string(b))
				}
				return nil
			}
			res, err := cl.GetService(ctx, args[0])
			if err != nil {
				return err
			}
			if output == "json" {
				return printJSON(res)
			}
			b, _ := json.MarshalIndent(res, "", "  ")
			fmt.Println(string(b))
			return nil
		},
	}
	return c
}

func logsCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "logs <name>",
		Short: "Fetch logs for a service (stub if not exposed)",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return fmt.Errorf("service name required")
			}
			if output == "json" {
				return printJSON(map[string]string{"message": "logs endpoint not exposed by agent API yet"})
			}
			fmt.Println("logs endpoint not exposed by agent API yet")
			return nil
		},
	}
	return c
}

func stopCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "stop <name>",
		Short: "Stop a service",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return fmt.Errorf("service name required")
			}
			ctx := context.Background()
			cl := newClient()
			if err := cl.Stop(ctx, args[0]); err != nil {
				return err
			}
			if output == "json" {
				return printJSON(map[string]string{"status": "stopped", "name": args[0]})
			}
			fmt.Printf("Stopped %s\n", args[0])
			return nil
		},
	}
	return c
}

func removeCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "remove <name>",
		Short: "Remove a service",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return fmt.Errorf("service name required")
			}
			ctx := context.Background()
			cl := newClient()
			if err := cl.Remove(ctx, args[0]); err != nil {
				return err
			}
			if output == "json" {
				return printJSON(map[string]string{"status": "removed", "name": args[0]})
			}
			fmt.Printf("Removed %s\n", args[0])
			return nil
		},
	}
	return c
}
