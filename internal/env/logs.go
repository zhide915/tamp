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

// LogService is one thing an environment's logs can be asked about.
//
// It is the whole of what tamp knows about a log, and it exists so that the
// user never has to: the five bench processes share one container and are told
// apart by the prefix honcho writes, the four service containers have one
// stream each, and the router belongs to no environment at all.
type LogService struct {
	Name string
	// Service is the compose service whose container holds the log.
	Service string
	// Process is the honcho process to keep lines from, or empty when the
	// container's whole log is the answer.
	Process string
	// Global marks a service that is not part of any environment. There is one
	// — the router — and it is why `tamp logs router` works from anywhere.
	Global bool
}

// DefaultLogService is what `tamp logs` shows when told nothing: the web
// server, which is what the browser is talking to.
const DefaultLogService = "web"

// logServices is every service tamp can show a log for, in the order the
// error message lists them: the bench's own processes first, then the
// containers around it, then the machine's router.
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

// ParseLogService resolves a service name, or names every one tamp has.
//
// An unknown service is a usage error rather than a failure: nothing was
// attempted, the command line itself was wrong, and the answer is the list.
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

// LogServiceNames lists the services `tamp logs` accepts.
func LogServiceNames() []string {
	names := make([]string, len(logServices))
	for i, svc := range logServices {
		names[i] = svc.Name
	}
	return names
}

// LogsRequest is what `tamp logs` was asked for.
type LogsRequest struct {
	// Target is the positional arguments as the user wrote them: an
	// environment, a service, both in that order, or neither. Resolving which
	// is which needs the registry, so the command hands them over unread.
	Target []string
	// Follow keeps the log open, until the user interrupts tamp.
	Follow bool
	// Tail is how many lines from the end of the container's log to start
	// with. For a bench process it bounds the bench's whole output, of which
	// tamp then shows the lines belonging to that one service.
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
		// The router belongs to no environment, but an environment the user
		// named still has to exist: a typo silently ignored would show the
		// router's log as if the environment were there.
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

	// The log goes to stdout whatever --quiet says: it is the whole of what
	// the command was asked for, not narration around a result.
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

// logsTarget reads which environment and which service the positional
// arguments named.
//
// One bare word is ambiguous on its face, and the registry settles it: an
// environment answering to that name wins. Nothing stops someone naming an
// environment "web" — tamp's reserved words are its command words, and are
// deliberately not derived from a list that grows — so the environment they
// made has to keep answering to its own name, and the service is still
// reachable by saying both.
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

// processFilter passes through the lines honcho attributed to one process.
//
// The five bench processes share a container and so share a log; honcho
// prefixes every line with the process that wrote it, and this is what turns
// that back into five logs. It buffers because a write arrives as whatever
// bytes the daemon had ready, which is not a whole number of lines.
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
			// Not a whole line yet — put it back and wait for the rest.
			f.buf.WriteString(line)
			return len(p), nil
		}
		if err := f.emit(line); err != nil {
			return 0, err
		}
	}
}

// flush writes out a last line that never ended in a newline, which is what a
// log tamp stopped reading mid-line leaves behind.
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

// belongs reports whether a honcho line was written by, or is about, the
// process being asked for.
//
// The second half is not padding: "web.1 stopped (rc=1)" is written by
// honcho's own system process and is exactly what someone reading web's log
// came for.
func (f *processFilter) belongs(line string) bool {
	process, rest, ok := honchoLine(line)
	if !ok {
		// Not honcho's format at all, so it was written before or instead of
		// honcho — the entrypoint failing, most likely. Dropping it would
		// leave every log view empty at the exact moment the user is asking
		// why the bench will not start.
		return true
	}
	if process == f.process {
		return true
	}
	return process == "system" && strings.HasPrefix(strings.TrimSpace(rest), f.process+".")
}

// honchoLine splits a line of honcho's output into the process that wrote it
// and what that process said.
//
// honcho writes "<time> <process>.<n> | <line>". The replica number is dropped
// so that a process is addressed by the name it has in the process file, and
// tolerated as absent so a honcho that stops numbering still reads.
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
