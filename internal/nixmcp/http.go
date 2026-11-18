package nixmcp

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type Transport int

const (
	Stdio Transport = iota
	StreamableHTTP
	SSE
)

var transportNames = []string{"stdio", "http", "sse"}

func TransportNames() []string { return slices.Clone(transportNames) }

func ParseTransport(s string) (Transport, error) {
	i := slices.Index(transportNames, s)
	if i < 0 {
		return 0, fmt.Errorf("transport %q: want one of %s", s, strings.Join(transportNames, ", "))
	}
	return Transport(i), nil
}

func (t Transport) String() string { return transportNames[t] }

func (t Transport) DefaultPath() string {
	if t == SSE {
		return "/sse"
	}
	return "/mcp"
}

const (
	DefaultAddr = "127.0.0.1:8765"

	sessionTTL      = 30 * time.Minute
	shutdownTimeout = 5 * time.Second
)

type Endpoint struct {
	Addr  string
	Path  string
	Token string
}

func ServeNetwork(ctx context.Context, t Transport, e Endpoint) error {
	newServer, err := serverFactory()
	if err != nil {
		return err
	}
	getServer := func(*http.Request) *mcp.Server { return newServer() }
	var handler http.Handler
	switch t {
	case StreamableHTTP:
		handler = mcp.NewStreamableHTTPHandler(getServer, &mcp.StreamableHTTPOptions{SessionTimeout: sessionTTL})
	case SSE:
		handler = mcp.NewSSEHandler(getServer, nil)
	default:
		return fmt.Errorf("transport %s does not listen on a network address", t)
	}
	return listen(ctx, t, e, handler)
}

func authMiddleware(want string, next http.Handler) http.Handler {
	if want == "" {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if got == "" {
			got = r.Header.Get("X-Mcp-Token")
		}
		if subtle.ConstantTimeCompare([]byte(got), []byte(want)) != 1 {
			w.Header().Set("WWW-Authenticate", "Bearer")
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func listen(ctx context.Context, t Transport, e Endpoint, handler http.Handler) error {
	mux := http.NewServeMux()
	mux.Handle(e.Path, authMiddleware(e.Token, handler))
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})
	srv := &http.Server{Addr: e.Addr, Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	shutdownErr := make(chan error, 1)
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownTimeout)
		defer cancel()
		shutdownErr <- srv.Shutdown(shutdownCtx)
	}()
	logf("listening on http://%s%s (MCP over %s)", e.Addr, e.Path, t)
	if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("serving MCP over %s on %s: %w", t, e.Addr, err)
	}
	if err := <-shutdownErr; err != nil {
		return fmt.Errorf("shutting down the MCP %s server: %w", t, err)
	}
	return nil
}
