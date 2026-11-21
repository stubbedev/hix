package supervisor

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"syscall"
	"time"
)

type Op string

const (
	OpPing     Op = "ping"
	OpStatus   Op = "status"
	OpStart    Op = "start"
	OpStop     Op = "stop"
	OpRestart  Op = "restart"
	OpReload   Op = "reload"
	OpSync     Op = "sync"
	OpShutdown Op = "shutdown"
	OpFollow   Op = "follow"
)

type Request struct {
	Op         Op       `json:"op"`
	Units      []string `json:"units,omitempty"`
	Executable string   `json:"executable,omitempty"`
	Lines      int      `json:"lines,omitempty"`
}

type Action string

const (
	ActionStop    Action = "stopping"
	ActionRestart Action = "restarting"
	ActionReload  Action = "reloading"
	ActionStart   Action = "starting"
)

type Change struct {
	Action Action `json:"action"`
	Unit   string `json:"unit"`
}

type UnitStatus struct {
	Name        string    `json:"name"`
	Kind        string    `json:"kind"`
	Description string    `json:"description,omitempty"`
	LoadError   string    `json:"load_error,omitempty"`
	Fragment    string    `json:"fragment,omitempty"`
	Active      string    `json:"active"`
	Sub         string    `json:"sub"`
	Since       time.Time `json:"since,omitzero"`
	Result      string    `json:"result,omitempty"`
	MainPID     int       `json:"main_pid,omitempty"`
	StatusText  string    `json:"status_text,omitempty"`
	Job         string    `json:"job,omitempty"`
	Wanted      bool      `json:"wanted,omitempty"`
	Ignored     []string  `json:"ignored,omitempty"`
	Triggers    string    `json:"triggers,omitempty"`
	NextElapse  time.Time `json:"next_elapse,omitzero"`
	LastTrigger time.Time `json:"last_trigger,omitzero"`
}

type Response struct {
	Error      string       `json:"error,omitempty"`
	Failures   []string     `json:"failures,omitempty"`
	Status     []UnitStatus `json:"status,omitempty"`
	Changes    []Change     `json:"changes,omitempty"`
	Restarting bool         `json:"restarting,omitempty"`
	Line       string       `json:"line,omitempty"`
	Done       bool         `json:"done,omitempty"`
}

var ErrNotRunning = errors.New("the phix service supervisor is not running")

type Client struct {
	socket string
}

func NewClient(l Layout) Client { return Client{socket: l.Socket()} }

const dialTimeout = 5 * time.Second

func (c Client) dial(ctx context.Context) (net.Conn, error) {
	d := net.Dialer{Timeout: dialTimeout}
	conn, err := d.DialContext(ctx, "unix", c.socket)
	if errors.Is(err, syscall.ENOENT) || errors.Is(err, syscall.ECONNREFUSED) {
		return nil, fmt.Errorf("%w (no listener on %s)", ErrNotRunning, c.socket)
	}
	if err != nil {
		return nil, fmt.Errorf("connecting to the supervisor at %s: %w", c.socket, err)
	}
	return conn, nil
}

func (c Client) Stream(ctx context.Context, req Request, each func(Response) error) (err error) {
	conn, err := c.dial(ctx)
	if err != nil {
		return err
	}
	defer func() {
		if cerr := conn.Close(); cerr != nil && !errors.Is(cerr, net.ErrClosed) && err == nil {
			err = fmt.Errorf("closing the supervisor connection: %w", cerr)
		}
	}()
	stop := context.AfterFunc(ctx, func() { _ = conn.SetDeadline(time.Unix(1, 0)) })
	defer stop()
	if err := json.NewEncoder(conn).Encode(req); err != nil {
		return fmt.Errorf("sending %s to the supervisor: %w", req.Op, err)
	}
	dec := json.NewDecoder(bufio.NewReader(conn))
	for {
		var resp Response
		err := dec.Decode(&resp)
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("reading the supervisor's %s reply: %w", req.Op, err)
		}
		if resp.Error != "" {
			return errors.New(resp.Error)
		}
		if err := each(resp); err != nil {
			return err
		}
		if resp.Done {
			return nil
		}
	}
}

func (c Client) Call(ctx context.Context, req Request) (Response, error) {
	var last Response
	err := c.Stream(ctx, req, func(r Response) error {
		last = r
		return nil
	})
	return last, err
}
