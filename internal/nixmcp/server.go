package nixmcp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/stubbedev/phix/internal/version"
)

const serverName = "phix-mcp"

func logf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "["+serverName+"] "+format+"\n", args...)
}

func ServeStdio(ctx context.Context, in io.Reader, out io.Writer) error {
	newServer, err := serverFactory()
	if err != nil {
		return err
	}
	transport := &mcp.IOTransport{Reader: io.NopCloser(in), Writer: nopWriteCloser{out}}
	if err := newServer().Run(ctx, transport); err != nil && ctx.Err() == nil {
		return fmt.Errorf("serving MCP over stdio: %w", err)
	}
	return nil
}

type nopWriteCloser struct{ io.Writer }

func (nopWriteCloser) Close() error { return nil }

func serverFactory() (func() *mcp.Server, error) {
	tools, err := toolDefs()
	if err != nil {
		return nil, err
	}
	return func() *mcp.Server {
		srv := mcp.NewServer(
			&mcp.Implementation{Name: serverName, Version: version.Version},
			&mcp.ServerOptions{Instructions: serverInstructions},
		)
		for _, d := range tools {
			srv.AddTool(&mcp.Tool{Name: d.Name, Description: d.Description, InputSchema: d.InputSchema}, dispatch)
		}
		return srv
	}, nil
}

const toolCallTimeout = 120 * time.Second

func dispatch(ctx context.Context, req *mcp.CallToolRequest) (result *mcp.CallToolResult, err error) {
	ctx, cancel := context.WithTimeout(ctx, toolCallTimeout)
	defer cancel()

	args := map[string]any{}
	if len(req.Params.Arguments) > 0 {
		if err := json.Unmarshal(req.Params.Arguments, &args); err != nil {
			return textResult(errorf("invalid arguments: %v", err)), nil
		}
	}

	defer func() {
		if r := recover(); r != nil {
			result = textResult(errorf("internal error: %v", r))
			err = nil
		}
	}()

	var out string
	switch req.Params.Name {
	case "nix":
		out = nixDispatch(ctx, req, args)
	case "nix_versions":
		out = nixVersions(ctx, args)
	default:
		return nil, fmt.Errorf("unknown tool: %s", req.Params.Name)
	}
	return textResult(out), nil
}

func textResult(s string) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: s}}}
}

func argString(args map[string]any, key string) string {
	if v, ok := args[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

func argStringOr(args map[string]any, key, def string) string {
	if s := argString(args, key); s != "" {
		return s
	}
	return def
}

func argInt(args map[string]any, key string, def int) int {
	v, ok := args[key]
	if !ok {
		return def
	}
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	case json.Number:
		if i, err := n.Int64(); err == nil {
			return int(i)
		}
	case string:
		if i, err := parseIntStrict(n); err == nil {
			return i
		}
	}
	return def
}
