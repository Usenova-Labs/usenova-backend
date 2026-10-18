package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/byoi-platform/cli/internal/client"
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
			cl, err := newClient()
			if err != nil {
				return err
			}
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
			if err := client.ValidateServiceName(name); err != nil {
				return err
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
			cl, err := newClient()
			if err != nil {
				return err
			}
			res, err := cl.Deploy(ctx, spec)
			if err != nil {
				return reconcileDeploy(ctx, cl, name, err)
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
	c.Flags().StringArrayVar(&envs, "env", nil, "env KEY=VALUE (repeatable)")
	c.Flags().StringVar(&healthPath, "health-path", "", "health check HTTP path")
	c.Flags().IntVar(&healthPort, "health-port", 0, "health check port (container or host mapping target)")
	c.MarkFlagRequired("name")
	c.MarkFlagRequired("image")
	return c
}

// reconcileDeploy handles a deploy call that failed on the client timeout.
// The agent runs the deployment on its own context, so it may still be in
// flight; query the service state instead of reporting a bare timeout.
func reconcileDeploy(ctx context.Context, cl *client.Client, name string, err error) error {
	if !client.IsTimeout(err) {
		return err
	}
	svc, gerr := cl.GetService(ctx, name)
	if gerr != nil {
		return err
	}
	if output == "json" {
		if jerr := printJSON(map[string]any{
			"warning": fmt.Sprintf("deploy request timed out after %s; service state follows", timeout),
			"service": svc,
		}); jerr != nil {
			return jerr
		}
	} else {
		b, _ := json.Marshal(svc)
		fmt.Printf("Deploy request timed out after %s; service %q is still managed:\n%s\n", timeout, name, b)
	}
	return fmt.Errorf("deploy not confirmed within %s; check status with: status %s", timeout, name)
}

// formatPorts renders a host:container port map as a stable, comma-separated
// list ("8080:80,443:443"), or "-" when unset.
func formatPorts(ports map[string]string) string {
	if len(ports) == 0 {
		return "-"
	}
	hosts := make([]string, 0, len(ports))
	for host := range ports {
		hosts = append(hosts, host)
	}
	sort.Strings(hosts)
	pairs := make([]string, 0, len(hosts))
	for _, host := range hosts {
		pairs = append(pairs, host+":"+ports[host])
	}
	return strings.Join(pairs, ",")
}

// printServiceTable renders services as an aligned table in text mode.
func printServiceTable(services []client.Service) {
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tSTATE\tIMAGE\tPORTS\tRESTARTS")
	for _, s := range services {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%d\n",
			s.Spec.Name, s.State, s.Spec.Image, formatPorts(s.Spec.Ports), s.RestartCount)
	}
	_ = tw.Flush()
}

// printServiceDetail renders one service as aligned label/value lines.
func printServiceDetail(s *client.Service) {
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	defer func() { _ = tw.Flush() }()
	row := func(k, v string) { fmt.Fprintf(tw, "%s\t%s\n", k, v) }

	row("NAME", s.Spec.Name)
	row("STATE", s.State)
	row("DESIRED", s.DesiredState)
	row("IMAGE", s.Spec.Image)
	row("PORTS", formatPorts(s.Spec.Ports))
	if len(s.Spec.Env) > 0 {
		row("ENV", strings.Join(s.Spec.Env, ","))
	}
	if s.Spec.RestartPolicy != "" {
		row("RESTART", s.Spec.RestartPolicy)
	}
	if hc := s.Spec.HealthCheck; hc != nil {
		h := hc.Type
		if hc.Port > 0 {
			h += fmt.Sprintf(" port=%d", hc.Port)
		}
		if hc.Path != "" {
			h += fmt.Sprintf(" path=%s", hc.Path)
		}
		row("HEALTH", h)
	}
	row("RESTARTS", fmt.Sprintf("%d", s.RestartCount))
	if !s.DeployedAt.IsZero() {
		row("DEPLOYED", s.DeployedAt.Format(time.RFC3339))
	}
	if s.ContainerID != "" {
		row("CONTAINER", s.ContainerID)
	}
}

func statusCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "status [name]",
		Short: "List services or get one service",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			cl, err := newClient()
			if err != nil {
				return err
			}
			if len(args) == 0 {
				lr, err := cl.ListServices(ctx)
				if err != nil {
					return err
				}
				if output == "json" {
					return printJSON(lr)
				}
				if len(lr.Services) == 0 {
					fmt.Println("No services.")
					return nil
				}
				printServiceTable(lr.Services)
				return nil
			}
			res, err := cl.GetService(ctx, args[0])
			if err != nil {
				return err
			}
			if output == "json" {
				return printJSON(res)
			}
			printServiceDetail(res)
			return nil
		},
	}
	return c
}

func logsCmd() *cobra.Command {
	// TODO(agent API): implement once the agent exposes
	// GET /services/{name}/logs (follow, tail, timestamps).
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
			cl, err := newClient()
			if err != nil {
				return err
			}
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
			cl, err := newClient()
			if err != nil {
				return err
			}
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
