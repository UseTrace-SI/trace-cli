// Package acp is a transparent stdio proxy for the Agent Client Protocol
// (https://agentclientprotocol.com). An editor launches `trace acp -- <agent>`
// in place of the agent. Every byte the editor and the agent exchange is
// forwarded unchanged and immediately; a copy is parsed off the forwarding
// path to record sessions, turns, tool calls and file edits.
package acp

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"

	"github.com/UseTrace-SI/trace-cli/internal/event"
)

// Direction says which peer wrote a message.
type Direction int

const (
	// FromClient is a message the editor sent to the agent.
	FromClient Direction = iota
	// FromAgent is a message the agent sent to the editor.
	FromAgent
)

const (
	chunkSize = 64 << 10
	// observeQueue bounds the bytes held for observation (in chunks). When the
	// observer falls behind, chunks are dropped rather than slowing the proxy.
	observeQueue = 1024
	// flushTimeout bounds how long the proxy waits for recorded events to be
	// handed to the sink after the agent exits.
	flushTimeout = 3 * time.Second
	// killGrace is how long the agent may take to exit after its editor
	// disappears before it is killed.
	killGrace = 2 * time.Second
)

// Config describes one proxied agent process.
type Config struct {
	// Args is the agent command line; Args[0] is resolved on PATH.
	Args []string
	// Name is the agent name recorded in events, for example "acp:claude-agent-acp".
	Name   string
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
	// Sink receives recorded events in order, on a goroutine of its own.
	// It may block; the proxy never waits on it while forwarding.
	Sink func(event.Event)
	// Logf records observation failures. It must not write to Stdout.
	Logf func(format string, args ...any)
	// Signals, when set, delivers signals to forward to the agent.
	Signals <-chan os.Signal
	// Now overrides the clock in tests.
	Now func() time.Time
}

type chunk struct {
	dir  Direction
	data []byte
	// gap marks that bytes before this chunk were not observed.
	gap bool
}

// ExitError carries the agent's exit status.
type ExitError struct{ Code int }

func (e ExitError) Error() string { return fmt.Sprintf("agent exited with status %d", e.Code) }

// Run starts the agent, proxies until it exits, and returns its exit status as
// an ExitError (nil for status 0).
func Run(cfg Config) error {
	if len(cfg.Args) == 0 {
		return errors.New("no agent command given")
	}
	if cfg.Logf == nil {
		cfg.Logf = func(string, ...any) {}
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Sink == nil {
		cfg.Sink = func(event.Event) {}
	}
	cmd := exec.Command(cfg.Args[0], cfg.Args[1:]...)
	cmd.Stderr = cfg.Stderr
	agentIn, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("creating agent stdin: %w", err)
	}
	agentOut, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("creating agent stdout: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("starting %s: %w", cfg.Args[0], err)
	}

	obs := newObserver(cfg.Name, cfg.Now, cfg.Logf)
	queue := make(chan chunk, observeQueue)
	done := make(chan struct{})
	observed := make(chan struct{})
	events := make(chan event.Event, 4096)
	go func() {
		defer close(observed)
		obs.run(queue, done, func(ev event.Event) {
			select {
			case events <- ev:
			default:
				cfg.Logf("event queue full; dropped %s event", ev.Kind)
			}
		})
		close(events)
	}()
	sent := make(chan struct{})
	go func() {
		defer close(sent)
		for ev := range events {
			deliver(cfg, ev)
		}
	}()

	var clientGone sync.Once
	terminate := func() {
		clientGone.Do(func() {
			if cmd.Process == nil {
				return
			}
			_ = cmd.Process.Signal(syscall.SIGTERM)
			go func() {
				time.Sleep(killGrace)
				_ = cmd.Process.Kill()
			}()
		})
	}

	// Editor -> agent. Closing the agent's stdin on editor EOF lets the agent
	// shut down as it would without the proxy. This goroutine may stay blocked
	// reading the editor after the agent exits; the process exits then.
	go func() {
		err := forward(cfg.Stdin, agentIn, FromClient, queue, done)
		_ = agentIn.Close()
		if err != nil && !errors.Is(err, errReadSide) {
			cfg.Logf("forwarding to agent: %v", err)
		}
	}()
	stopSignals := make(chan struct{})
	if cfg.Signals != nil {
		go func() {
			for {
				select {
				case sig := <-cfg.Signals:
					_ = cmd.Process.Signal(sig)
				case <-stopSignals:
					return
				}
			}
		}()
	}

	// Agent -> editor, on this goroutine: the agent's output must be read to
	// EOF before Wait.
	if err := forward(agentOut, cfg.Stdout, FromAgent, queue, done); err != nil {
		if !errors.Is(err, errReadSide) {
			cfg.Logf("forwarding to editor: %v; stopping agent", err)
			terminate()
		}
		// Keep draining so the agent never blocks on a full pipe while it exits.
		_, _ = io.Copy(io.Discard, agentOut)
	}
	waitErr := cmd.Wait()
	close(stopSignals)
	close(done)
	<-observed
	select {
	case <-sent:
	case <-time.After(flushTimeout):
		cfg.Logf("timed out delivering recorded events")
	}
	return exitStatus(waitErr)
}

func deliver(cfg Config, ev event.Event) {
	defer func() {
		if r := recover(); r != nil {
			cfg.Logf("recording %s event: panic: %v", ev.Kind, r)
		}
	}()
	cfg.Sink(ev)
}

var errReadSide = errors.New("read side closed")

// forward copies src to dst, writing each read as soon as it arrives. A copy
// is offered to the observer first, without ever waiting for it.
func forward(src io.Reader, dst io.Writer, dir Direction, queue chan<- chunk, done <-chan struct{}) error {
	buf := make([]byte, chunkSize)
	gap := false
	for {
		n, rerr := src.Read(buf)
		if n > 0 {
			// Offer the copy before writing: a peer's reply to this message
			// is then always observed after it.
			c := chunk{dir: dir, data: append([]byte(nil), buf[:n]...), gap: gap}
			select {
			case <-done:
			case queue <- c:
				gap = false
			default:
				gap = true
			}
			if _, werr := dst.Write(buf[:n]); werr != nil {
				return werr
			}
		}
		if rerr != nil {
			if rerr == io.EOF {
				return nil
			}
			return fmt.Errorf("%w: %v", errReadSide, rerr)
		}
	}
}

func exitStatus(err error) error {
	if err == nil {
		return nil
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		code := exit.ExitCode()
		if code < 0 {
			if status, ok := exit.Sys().(syscall.WaitStatus); ok && status.Signaled() {
				code = 128 + int(status.Signal())
			} else {
				code = 1
			}
		}
		return ExitError{Code: code}
	}
	return fmt.Errorf("waiting for agent: %w", err)
}
