package main

import (
	"context"
	"errors"
	"io"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/paularlott/logger"
	logtesting "github.com/paularlott/logger/testing"
)

// echoBackend is a mock upstream that answers with "<name>:<received data>"
// once the client half-closes, then closes itself.
type echoBackend struct {
	name string
	ln   net.Listener
	hits atomic.Int64
	done chan struct{}
}

func startEchoBackend(t *testing.T, name string) *echoBackend {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen mock backend: %v", err)
	}
	e := &echoBackend{name: name, ln: ln, done: make(chan struct{})}
	go e.acceptLoop()
	t.Cleanup(func() { e.stop() })
	return e
}

func (e *echoBackend) acceptLoop() {
	for {
		c, err := e.ln.Accept()
		if err != nil {
			close(e.done)
			return
		}
		go func(c net.Conn) {
			defer c.Close()
			e.hits.Add(1)
			data, err := io.ReadAll(c) // drains until the relay propagates the client's half-close
			if err != nil {
				return
			}
			c.Write([]byte(e.name + ":" + string(data)))
		}(c)
	}
}

func (e *echoBackend) addr() string { return e.ln.Addr().String() }

func (e *echoBackend) stop()        { e.ln.Close() }
func (e *echoBackend) waitStopped() { <-e.done }

func (e *echoBackend) restart(t *testing.T) {
	t.Helper()
	ln, err := net.Listen("tcp", e.ln.Addr().String())
	if err != nil {
		t.Fatalf("restart mock backend on %s: %v", e.ln.Addr().String(), err)
	}
	e.ln = ln
	e.done = make(chan struct{})
	go e.acceptLoop()
}

// ask sends one request through the proxy and returns the mock backend's
// response. Half-closing after the write also exercises FIN propagation.
func ask(t *testing.T, proxyAddr, payload string) (string, error) {
	t.Helper()
	c, err := net.DialTimeout("tcp", proxyAddr, 2*time.Second)
	if err != nil {
		return "", err
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := c.Write([]byte(payload)); err != nil {
		return "", err
	}
	if tc, ok := c.(*net.TCPConn); ok {
		tc.CloseWrite()
	}
	resp, err := io.ReadAll(c)
	return string(resp), err
}

// startTestProxy relays to the two mocks and returns the proxy address plus
// the mock the pool logs to, for asserting state transitions. The server
// gets a null logger: its WithError children would race on the mock's
// shared entries slice.
func startTestProxy(t *testing.T, primary, secondary *echoBackend) (string, *logtesting.MockLogger) {
	t.Helper()
	poolLog := logtesting.New()
	pool := NewPool([]string{primary.addr(), secondary.addr()},
		500*time.Millisecond, 2, 1, poolLog)
	proxyLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen proxy: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go pool.RunHealthChecks(ctx, 100*time.Millisecond)
	server := NewServer(pool, logger.NewNullLogger())
	go func() { server.serve(proxyLn, ctx.Done()) }()
	return proxyLn.Addr().String(), poolLog
}

func expectRouting(t *testing.T, proxyAddr, want string) {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		got, err := ask(t, proxyAddr, "ping")
		if err == nil && got == want {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("proxy did not route to %q in time", want)
}

func expectEntry(t *testing.T, mock *logtesting.MockLogger, level, msg string) {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		if mock.HasEntry(level, msg) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("no %q/%q entry logged; entries: %s", level, msg, mock.String())
}

func TestFailoverAndRecovery(t *testing.T) {
	primary := startEchoBackend(t, "P")
	secondary := startEchoBackend(t, "S")
	proxyAddr, poolLog := startTestProxy(t, primary, secondary)

	// Traffic prefers the primary while both are up.
	expectRouting(t, proxyAddr, "P:ping")

	// Primary dies: dials to it are refused instantly so traffic moves to
	// the secondary, and the health checks mark the primary down.
	primary.stop()
	primary.waitStopped()
	expectRouting(t, proxyAddr, "S:ping")
	expectEntry(t, poolLog, "warn", "backend marked down")

	// It comes back: the pool promotes it and preference order is restored
	// without restarting the proxy.
	time.Sleep(500 * time.Millisecond) // >= fail-after checks while down
	primary.restart(t)
	expectEntry(t, poolLog, "info", "backend recovered")
	expectRouting(t, proxyAddr, "P:ping")
}

func TestAllBackendsDownRefuses(t *testing.T) {
	primary := startEchoBackend(t, "P")
	secondary := startEchoBackend(t, "S")
	proxyAddr, _ := startTestProxy(t, primary, secondary)

	primary.stop()
	primary.waitStopped()
	secondary.stop()
	secondary.waitStopped()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		_, err := ask(t, proxyAddr, "ping")
		if err != nil {
			return // proxy closed the connection: no reachable backend
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("proxy still accepted traffic with every backend down")
}

func TestPickPrefersHealthyInOrder(t *testing.T) {
	p := NewPool([]string{"a:1", "b:1", "c:1"}, time.Second, 2, 1, logtesting.New())

	dialErr := errors.New("boom")
	p.report(p.backends[0], dialErr)
	p.report(p.backends[0], dialErr) // a: two consecutive failures -> down

	var got []string
	for _, b := range p.pick() {
		got = append(got, b.Address)
	}
	want := []string{"b:1", "c:1", "a:1"} // healthy first in order, down as last resort
	if len(got) != len(want) {
		t.Fatalf("pick() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("pick() = %v, want %v", got, want)
		}
	}

	// Recovery: one success promotes it back to first place.
	p.report(p.backends[0], nil)
	if first := p.pick()[0].Address; first != "a:1" {
		t.Fatalf("after recovery pick()[0] = %q, want %q", first, "a:1")
	}
}
