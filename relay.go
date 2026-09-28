package main

import (
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"syscall"

	"github.com/paularlott/logger"
)

// Server accepts client connections and relays them to a backend from the
// pool. Live connections are tracked so shutdown can close them all.
type Server struct {
	pool *Pool
	log  logger.Logger

	mu    sync.Mutex
	conns map[net.Conn]struct{}
}

func NewServer(pool *Pool, log logger.Logger) *Server {
	if log == nil {
		log = logger.NewNullLogger()
	}
	return &Server{pool: pool, log: log, conns: make(map[net.Conn]struct{})}
}

// serve accepts until stop is closed (which also closes the listener), then
// waits for in-flight relays to finish.
func (s *Server) serve(ln net.Listener, stop <-chan struct{}) error {
	go func() {
		<-stop
		ln.Close()
		s.closeAll()
	}()

	for {
		client, err := ln.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return nil
			}
			return err
		}
		go s.handle(client)
	}
}

func (s *Server) track(c net.Conn) {
	s.mu.Lock()
	s.conns[c] = struct{}{}
	s.mu.Unlock()
}

func (s *Server) untrack(c net.Conn) {
	s.mu.Lock()
	delete(s.conns, c)
	s.mu.Unlock()
}

func (s *Server) closeAll() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for c := range s.conns {
		c.Close()
	}
}

// handle connects the client to the first reachable backend, trying the
// remaining ones when a dial fails, then pumps bytes both ways.
func (s *Server) handle(client net.Conn) {
	s.track(client)
	defer func() {
		s.untrack(client)
		client.Close()
	}()

	for _, b := range s.pool.pick() {
		upstream, err := s.pool.dial(b)
		if err != nil {
			s.log.WithError(err).Debug("backend dial failed", "backend", b.Address)
			continue
		}
		s.log.Info("relay opened", "client", client.RemoteAddr().String(), "backend", b.Address)
		relay(client, upstream)
		s.log.Info("relay closed", "client", client.RemoteAddr().String(), "backend", b.Address)
		return
	}

	s.log.Warn("no reachable backend", "client", client.RemoteAddr().String(), "backends", s.pool.snapshot())
}

// relay copies bytes in both directions until both sides are done. When one
// direction sees EOF its half of the peer connection is closed (FIN), letting
// the other direction drain what is still in flight; any error tears down
// both connections so a reset unblocks the opposite copy immediately.
func relay(a, b net.Conn) {
	var closeOnce sync.Once
	hardClose := func() {
		closeOnce.Do(func() {
			a.Close()
			b.Close()
		})
	}

	var wg sync.WaitGroup
	wg.Add(2)
	copyDir := func(dst, src net.Conn) {
		defer wg.Done()
		_, err := io.Copy(dst, src)
		if err != nil {
			hardClose()
			return
		}
		if c, ok := dst.(*net.TCPConn); ok {
			c.CloseWrite() // half-close: signal EOF to the peer, keep reading
		} else {
			dst.Close()
		}
	}
	go copyDir(a, b)
	go copyDir(b, a)
	wg.Wait()
	hardClose()
}

// listen binds the proxy address, adding a loopback alias first on macOS if
// needed (binding 127.0.0.2 and friends requires the alias to exist, unlike
// Linux where all of 127/8 is bound by default).
func listen(address string, addAlias bool) (net.Listener, error) {
	ln, err := net.Listen("tcp", address)
	if err == nil || !errors.Is(err, syscall.EADDRNOTAVAIL) || !addAlias {
		return ln, err
	}

	host, _, splitErr := net.SplitHostPort(address)
	if splitErr != nil {
		return ln, err
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() || ip.Equal(net.IPv4(127, 0, 0, 1)) {
		return ln, err
	}

	if aliasErr := addLoopbackAlias(host); aliasErr != nil {
		return ln, fmt.Errorf("listen %s: %w (adding loopback alias: %v)", address, err, aliasErr)
	}
	return net.Listen("tcp", address)
}
