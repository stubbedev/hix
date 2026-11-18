package nixmcp

import (
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestRootsAllowedProtocolGate(t *testing.T) {
	caps := &mcp.ClientCapabilities{RootsV2: &mcp.RootCapabilities{}} //nolint:staticcheck
	for ver, want := range map[string]bool{
		"2024-11-05": true,
		"2025-06-18": true,
		"2025-11-25": true,
		"2026-07-28": false,
		"2027-03-01": false,
	} {
		if got := rootsAllowed(&mcp.InitializeParams{ProtocolVersion: ver, Capabilities: caps}); got != want {
			t.Errorf("rootsAllowed(%s) = %v, want %v", ver, got, want)
		}
	}
	if rootsAllowed(nil) {
		t.Error("nil params should not be roots-usable")
	}
	if rootsAllowed(&mcp.InitializeParams{ProtocolVersion: "2025-11-25"}) {
		t.Error("client without the roots capability should not be roots-usable")
	}
}
