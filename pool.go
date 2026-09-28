package main

import (
	"context"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/paularlott/logger"
)

// Backend is one upstream target with its health state.
type Backend struct {
	Address string

	mu            sync.Mutex
	up            bool
	consecFailure int
	consecSuccess int
	lastError     error
	since         time.Time
}

func (b *Backend) healthy() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.up
}

func (b *Backend) state() (up bool, lastErr error, since time.Time) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.up, b.lastError, b.since
}

// Pool holds the ordered list of backends. Backends keep their configured
// order as preference: traffic always goes to the first healthy one and the
// rest are failover targets, so a recovered primary is returned to
// automatically.
type Pool struct {
	backends         []*Backend
	dialTimeout      time.Duration
	failThreshold    int
	recoverThreshold int
	log              logger.Logger
}

func NewPool(addresses []string, dialTimeout time.Duration, failThreshold, recoverThreshold int, log logger.Logger) *Pool {
	if log == nil {
		log = logger.NewNullLogger()
	}
	p := &Pool{
		dialTimeout:      dialTimeout,
		failThreshold:    failThreshold,
		recoverThreshold: recoverThreshold,
		log:              log,
	}
	for _, addr := range addresses {
		p.backends = append(p.backends, &Backend{Address: addr, up: true, since: time.Now()})
	}
	return p
}

// pick returns the backends to try for a new connection: healthy ones first
// in preference order, then unhealthy ones as a last resort in case the
// health state is stale and the host has in fact recovered.
func (p *Pool) pick() []*Backend {
	var up, down []*Backend
	for _, b := range p.backends {
		if b.healthy() {
			up = append(up, b)
		} else {
			down = append(down, b)
		}
	}
	return append(up, down...)
}

// dial attempts a connection to one backend within the dial timeout.
func (p *Pool) dial(b *Backend) (net.Conn, error) {
	conn, err := net.DialTimeout("tcp", b.Address, p.dialTimeout)
	p.report(b, err)
	return conn, err
}

// report folds a probe or data-plane result into the backend's state.
// Failures demote after failThreshold consecutive errors; successes promote
// after recoverThreshold consecutive successes.
func (p *Pool) report(b *Backend, err error) {
	b.mu.Lock()
	wasUp := b.up
	if err != nil {
		b.consecSuccess = 0
		b.lastError = err
		b.consecFailure++
		if b.up && b.consecFailure >= p.failThreshold {
			b.up = false
			b.since = time.Now()
		}
	} else {
		b.consecFailure = 0
		b.lastError = nil
		b.consecSuccess++
		if !b.up && b.consecSuccess >= p.recoverThreshold {
			b.up = true
			b.since = time.Now()
		}
	}
	nowUp := b.up
	b.mu.Unlock()

	if wasUp != nowUp {
		if nowUp {
			p.log.Info("backend recovered", "backend", b.Address, "state", "up")
		} else {
			p.log.Warn("backend marked down", "backend", b.Address, "state", "down")
		}
	}
}

// probe does one TCP-connect health check against every backend so a dead
// primary is noticed before clients pay the dial timeout, and a recovered
// backend is promoted back into first position.
func (p *Pool) probe() {
	var wg sync.WaitGroup
	for _, b := range p.backends {
		wg.Add(1)
		go func(b *Backend) {
			defer wg.Done()
			conn, err := net.DialTimeout("tcp", b.Address, p.dialTimeout)
			if err == nil {
				conn.Close()
			}
			p.report(b, err)
		}(b)
	}
	wg.Wait()
}

// RunHealthChecks probes all backends on the given interval until ctx is
// cancelled.
func (p *Pool) RunHealthChecks(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			p.probe()
		}
	}
}

func (p *Pool) snapshot() string {
	s := ""
	for i, b := range p.backends {
		if i > 0 {
			s += ", "
		}
		up, lastErr, since := b.state()
		state := "up"
		if !up {
			state = fmt.Sprintf("down since %s (%v)", since.Format(time.TimeOnly), lastErr)
		}
		s += fmt.Sprintf("%s [%s]", b.Address, state)
	}
	return s
}
