package nixmcp

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type mcpRoot struct {
	URI  string
	Name string
}

func (r mcpRoot) path() string {
	if strings.HasPrefix(r.URI, "file://") {
		if u, err := url.Parse(r.URI); err == nil && u.Path != "" {
			return u.Path
		}
	}
	return r.URI
}

func rootFromString(s string) (mcpRoot, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return mcpRoot{}, false
	}
	return mcpRoot{URI: s}, true
}

var rootHeaders = []string{"X-Repo-Root", "X-Mcp-Roots", "X-Mcp-Root", "Mcp-Roots", "Mcp-Root"}

func parseRootHeaders(h http.Header) []mcpRoot {
	var roots []mcpRoot
	for _, name := range rootHeaders {
		for _, v := range h.Values(name) {
			for part := range strings.SplitSeq(v, ",") {
				if r, ok := rootFromString(part); ok {
					roots = append(roots, r)
				}
			}
		}
	}
	return roots
}

func resolveRoots(ctx context.Context, req *mcp.CallToolRequest) []mcpRoot {
	if req == nil {
		return nil
	}
	if req.Extra != nil && req.Extra.Header != nil {
		if roots := parseRootHeaders(req.Extra.Header); len(roots) > 0 {
			return roots
		}
	}
	if req.Session == nil {
		return nil
	}

	if !rootsAllowed(req.Session.InitializeParams()) {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	res, err := req.Session.ListRoots(ctx, &mcp.ListRootsParams{}) //nolint:staticcheck
	if err != nil || res == nil {
		return nil
	}
	out := make([]mcpRoot, 0, len(res.Roots))
	for _, r := range res.Roots {
		out = append(out, mcpRoot{URI: r.URI, Name: r.Name})
	}
	return out
}

func rootDir(ctx context.Context, req *mcp.CallToolRequest, explicit string) string {
	if explicit != "" {
		return explicit
	}
	for _, r := range resolveRoots(ctx, req) {
		if p := r.path(); p != "" {
			return p
		}
	}
	return "."
}

const rootsRemovedFrom = "2026-07-28"

func rootsAllowed(ip *mcp.InitializeParams) bool {
	if ip == nil || ip.Capabilities == nil || ip.ProtocolVersion >= rootsRemovedFrom {
		return false
	}
	return ip.Capabilities.RootsV2 != nil //nolint:staticcheck
}
