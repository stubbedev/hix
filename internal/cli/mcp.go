package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/stubbedev/phix/internal/nixmcp"
	"github.com/stubbedev/phix/internal/usage"
)

const mcpTokenEnv = "PHIX_MCP_TOKEN" //nolint:gosec

type transportValue struct{ transport *nixmcp.Transport }

func (v transportValue) String() string { return v.transport.String() }

func (v transportValue) Type() string { return "transport" }

func (v transportValue) Set(s string) error {
	t, err := nixmcp.ParseTransport(s)
	if err != nil {
		return err
	}
	*v.transport = t
	return nil
}

func (a *App) mcpCmd() (*cobra.Command, error) {
	transport := nixmcp.Stdio
	var endpoint nixmcp.Endpoint
	cmd := &cobra.Command{
		Use:   "mcp",
		Short: "Serve the Nix ecosystem MCP server over stdio, HTTP or SSE",
		Long: `Serve an MCP server for the Nix ecosystem: nixpkgs packages, NixOS, Home
Manager, nix-darwin and Nixvim options, flakes, FlakeHub, NixHub, channels,
the binary cache, store paths, the NixOS wiki, nix.dev and Noogle.

--transport picks how clients connect: stdio (default) for one client that
starts phix itself, http (MCP Streamable HTTP) or sse (the legacy HTTP+SSE
transport) for many clients sharing one server on --addr. Over http and sse a
client pins its flake directory per request with the X-Repo-Root header.

Environment:
  PHIX_MCP_TOKEN  with http or sse, require it as "Authorization: Bearer <token>"
                  or an X-Mcp-Token header`,
		Args:              noArgs,
		ValidArgsFunction: cobra.NoFileCompletions,
		RunE: func(cmd *cobra.Command, _ []string) error {
			endpoint.Token = os.Getenv(mcpTokenEnv)
			if transport == nixmcp.Stdio {
				for _, name := range []string{"addr", "path"} {
					if cmd.Flags().Changed(name) {
						return usage.Errorf("--%s only applies to --transport http or sse", name)
					}
				}
				if endpoint.Token != "" {
					return usage.Errorf("%s only applies to --transport http or sse; unset it for stdio", mcpTokenEnv)
				}
				return nixmcp.ServeStdio(cmd.Context(), a.console.Stdin, a.console.Stdout)
			}
			if endpoint.Addr == "" {
				return usage.Errorf("--addr: empty address")
			}
			if !cmd.Flags().Changed("path") {
				endpoint.Path = transport.DefaultPath()
			}
			if !strings.HasPrefix(endpoint.Path, "/") {
				return usage.Errorf("--path %q: must start with /", endpoint.Path)
			}
			return nixmcp.ServeNetwork(cmd.Context(), transport, endpoint)
		},
	}
	fs := cmd.Flags()
	fs.Var(transportValue{&transport}, "transport", "how clients connect: "+strings.Join(nixmcp.TransportNames(), ", "))
	fs.StringVar(&endpoint.Addr, "addr", nixmcp.DefaultAddr, "address to listen on for http and sse")
	fs.StringVar(&endpoint.Path, "path", "", fmt.Sprintf("endpoint path for http and sse (default %s for http, %s for sse)", nixmcp.StreamableHTTP.DefaultPath(), nixmcp.SSE.DefaultPath()))
	if err := cmd.RegisterFlagCompletionFunc("transport", cobra.FixedCompletions(nixmcp.TransportNames(), cobra.ShellCompDirectiveNoFileComp)); err != nil {
		return nil, fmt.Errorf("registering --transport completion: %w", err)
	}
	return cmd, nil
}
