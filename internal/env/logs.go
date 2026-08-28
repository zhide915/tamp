package env

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/zhide915/tamp/internal/engine"
	"github.com/zhide915/tamp/internal/exitcode"
	"github.com/zhide915/tamp/internal/router"
)

// LogService maps a user-facing log name to where it lives: the five bench
// processes share one container and are told apart by honcho's prefix, the
// service containers have one stream each, and the router belongs to no
// environment.
type LogService struct {
	Name    string
	Service string
	// Process is the honcho process to keep lines from; empty takes the whole
	// container log.
	Process string
	// Global marks the router, the one service outside any environment —
	// why `tamp logs router` works from anywhere.
	Global bool
}

// DefaultLogService is the web server — what the browser is talking to.
const DefaultLogService = "web"

// logServices is ordered the way the error message lists them.
var logServices = []LogService{
	{Name: "web", Service: FrappeService, Process: "web"},
	{Name: "socketio", Service: FrappeService, Process: "socketio"},
	{Name: "watch", Service: FrappeService, Process: "watch"},
	{Name: "schedule", Service: FrappeService, Process: "schedule"},
	{Name: "worker", Service: FrappeService, Process: "worker"},
	{Name: MariaDBService, Service: MariaDBService},
	{Name: RedisCacheService, Service: RedisCacheService},
	{Name: RedisQueueService, Service: RedisQueueService},
	{Name: MailpitService, Service: MailpitService},
	{Name: "router", Global: true},
}

// ParseLogService resolves a service name. An unknown one is a usage error —
// nothing was attempted — and the answer is the list.
func ParseLogService(name string) (LogService, error) {
	if name == "" {
		name = DefaultLogService
	}
	for _, svc := range logServices {
		if svc.Name == name {
			return svc, nil
		}
	}
	return LogService{}, exitcode.Usage(
		fmt.Sprintf("%q is not a service tamp has a log for", name),
		"services: "+strings.Join(LogServiceNames(), ", "))
}

func LogServiceNames() []string {
	names := make([]string, len(logServices))
	for i, svc := range logServices {
		names[i] = svc.Name
	}
	return names
}

// LogsRequest is what `tamp logs` was asked for.
type LogsRequest struct {
	// Target is the raw positionals — environment, service, both in that
	// order, or neither; telling which needs the registry.
	Target []string
	Follow bool
	// Tail bounds the container's log; for a bench process the per-process
	// filter applies after that.
	Tail int
}

// Logs streams one service's log.
func (m *Manager) Logs(ctx context.Context, req LogsRequest) error {
	name, service, err := m.logsTarget(req.Target)
	if err != nil {
		return err
	}
	svc, err := ParseLogService(service)
	if err != nil {
		return err
	}

	container := router.Container
	if svc.Global {
		// A named environment must still exist, or a typo would silently show
		// the router's log.
		if name != "" {
			if _, err := m.resolve(name); err != nil {
				return err
			}
		}
	} else {
		e, err := m.resolve(name)
		if err != nil {
			return err
		}
		container = e.Resources.Container(svc.Service)
	}

	// The log is the command's output — --quiet does not drop it.
	out := io.Writer(m.Out.Out)
	if svc.Process != "" {
		filter := &processFilter{out: out, process: svc.Process}
		defer filter.flush()
		out = filter
	}
	return m.Engine.Logs(ctx, engine.LogRequest{
		Container: container,
		Follow:    req.Follow,
		Tail:      req.Tail,
		Stdout:    out,
		Stderr:    out,
	})
}

// logsTarget settles one bare word via the registry: an environment answering
// to the name wins — reserved words do not grow, so someone may name one
// "web" — and the service stays reachable by saying both.
func (m *Manager) logsTarget(target []string) (name, service string, err error) {
	switch len(target) {
	case 0:
		return "", "", nil
	case 1:
	default:
		return target[0], target[1], nil
	}

	registered, err := m.isRegistered(target[0])
	if err != nil {
		return "", "", err
	}
	if registered {
		return target[0], "", nil
	}
	if _, err := ParseLogService(target[0]); err == nil {
		return "", target[0], nil
	}
	return "", "", exitcode.Usage(
		fmt.Sprintf("%q is neither an environment on this machine nor a service tamp has a log for", target[0]),
		"services: "+strings.Join(LogServiceNames(), ", ")+"; environments: see 'tamp list'")
}

func (m *Manager) isRegistered(name string) (bool, error) {
	reg, err := LoadRegistry(m.Home)
	if err != nil {
		return false, err
	}
	_, ok := reg[name]
	return ok, nil
}

// processFilter keeps the lines honcho attributed to one process. It buffers
// because writes arrive as arbitrary byte chunks, not whole lines.
type processFilter struct {
	out     io.Writer
	process string
	buf     bytes.Buffer
}

func (f *processFilter) Write(p []byte) (int, error) {
	f.buf.Write(p)
	for {
		line, err := f.buf.ReadString('\n')
		if err != nil {
			// Partial line — put it back and wait for the rest.
			f.buf.WriteString(line)
			return len(p), nil
		}
		if err := f.emit(line); err != nil {
			return 0, err
		}
	}
}

// flush emits a trailing line that never got its newline.
func (f *processFilter) flush() {
	if f.buf.Len() > 0 {
		_ = f.emit(f.buf.String())
	}
}

func (f *processFilter) emit(line string) error {
	if !f.belongs(line) {
		return nil
	}
	_, err := io.WriteString(f.out, line)
	return err
}

// belongs also keeps honcho's own "system" lines about the process — "web.1
// stopped (rc=1)" is exactly what a reader of web's log came for.
func (f *processFilter) belongs(line string) bool {
	process, rest, ok := honchoLine(line)
	if !ok {
		// Not honcho's format: written before or instead of honcho, likely
		// the entrypoint failing. Dropping it would empty every log view at
		// the moment it matters most.
		return true
	}
	if process == f.process {
		return true
	}
	return process == "system" && strings.HasPrefix(strings.TrimSpace(rest), f.process+".")
}

// honchoLine parses "<time> <process>.<n> | <line>". The replica number is
// dropped so processes go by their process-file names, and tolerated as
// absent.
func honchoLine(line string) (process, rest string, ok bool) {
	bar := strings.Index(line, "|")
	if bar < 0 {
		return "", "", false
	}
	fields := strings.Fields(line[:bar])
	if len(fields) == 0 {
		return "", "", false
	}

	process = fields[len(fields)-1]
	if dot := strings.LastIndex(process, "."); dot > 0 {
		if _, err := strconv.Atoi(process[dot+1:]); err == nil {
			process = process[:dot]
		}
	}
	return process, line[bar+1:], true
}
