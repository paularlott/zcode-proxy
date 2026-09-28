package main

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/paularlott/cli"

	"github.com/paularlott/zcode-proxy/build"
	"github.com/paularlott/zcode-proxy/internal/log"
)

var (
	flagListen         string
	flagBackends       []string
	flagHealthInterval int
	flagDialTimeout    int
	flagFailAfter      int
	flagRecoverAfter   int
	flagAddAlias       bool
	flagLogLevel       string
	flagLogFormat      string
)

func main() {
	cmd := &cli.Command{
		Name:        "zcode-proxy",
		Version:     build.Version,
		Usage:       "[flags]",
		Description: "TCP proxy with automatic failover: relays a local address to a list of upstream backends, routing to the remaining ones when one goes down and restoring preference order when it recovers.",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:         "listen",
				Aliases:      []string{"l"},
				Usage:        "Address to listen on",
				DefaultValue: "127.0.0.2:443",
				EnvVars:      []string{"ZCODE_PROXY_LISTEN"},
				AssignTo:     &flagListen,
			},
			&cli.StringSliceFlag{
				Name:         "backend",
				Aliases:      []string{"b"},
				Usage:        "Upstream address in preference order (repeatable or comma-separated; a bare host uses the listen port)",
				DefaultValue: []string{"8.217.233.95:443", "8.217.100.151:443"},
				EnvVars:      []string{"ZCODE_PROXY_BACKENDS"},
				AssignTo:     &flagBackends,
			},
			&cli.IntFlag{
				Name:         "health-interval",
				Usage:        "Seconds between backend health checks",
				DefaultValue: 3,
				AssignTo:     &flagHealthInterval,
			},
			&cli.IntFlag{
				Name:         "dial-timeout",
				Usage:        "Seconds to wait when connecting to a backend before failing over",
				DefaultValue: 3,
				AssignTo:     &flagDialTimeout,
			},
			&cli.IntFlag{
				Name:         "fail-after",
				Usage:        "Consecutive failed checks before a backend is marked down",
				DefaultValue: 2,
				AssignTo:     &flagFailAfter,
			},
			&cli.IntFlag{
				Name:         "recover-after",
				Usage:        "Consecutive successful checks before a backend is marked up again",
				DefaultValue: 1,
				AssignTo:     &flagRecoverAfter,
			},
			&cli.BoolFlag{
				Name:         "add-alias",
				Usage:        "Add the loopback alias for the listen IP if it is missing (macOS, needs root)",
				DefaultValue: true,
				AssignTo:     &flagAddAlias,
			},
			&cli.StringFlag{
				Name:         "log-level",
				Usage:        "Log level: trace, debug, info, warn or error",
				DefaultValue: "info",
				EnvVars:      []string{"ZCODE_PROXY_LOG_LEVEL"},
				AssignTo:     &flagLogLevel,
			},
			&cli.StringFlag{
				Name:         "log-format",
				Usage:        "Log format: console or json",
				DefaultValue: "console",
				EnvVars:      []string{"ZCODE_PROXY_LOG_FORMAT"},
				AssignTo:     &flagLogFormat,
			},
		},
		Run: run,
	}

	if err := cmd.Execute(context.Background()); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, cmd *cli.Command) error {
	if _, _, err := net.SplitHostPort(flagListen); err != nil {
		return fmt.Errorf("--listen %q: missing or invalid port", flagListen)
	}
	if flagHealthInterval < 1 || flagDialTimeout < 1 || flagFailAfter < 1 || flagRecoverAfter < 1 {
		return fmt.Errorf("--health-interval, --dial-timeout, --fail-after and --recover-after must all be >= 1 second")
	}

	listenPort := portOf(flagListen)
	backends := make([]string, 0, len(flagBackends))
	for _, raw := range flagBackends {
		for _, b := range strings.Split(raw, ",") {
			b = strings.TrimSpace(b)
			if b == "" {
				continue
			}
			if _, _, err := net.SplitHostPort(b); err != nil {
				b = net.JoinHostPort(b, listenPort)
			}
			backends = append(backends, b)
		}
	}
	if len(backends) == 0 {
		return fmt.Errorf("at least one --backend is required")
	}

	switch flagLogLevel {
	case "trace", "debug", "info", "warn", "error":
	default:
		return fmt.Errorf("--log-level %q: must be trace, debug, info, warn or error", flagLogLevel)
	}
	switch flagLogFormat {
	case "console", "json":
	default:
		return fmt.Errorf("--log-format %q: must be console or json", flagLogFormat)
	}
	log.Configure(flagLogLevel, flagLogFormat, nil)

	pool := NewPool(backends,
		time.Duration(flagDialTimeout)*time.Second,
		flagFailAfter, flagRecoverAfter, log.GetLogger())

	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Probe once up front so the first client connection doesn't pay for
	// discovering a dead primary.
	pool.probe()

	ln, err := listen(flagListen, flagAddAlias)
	if err != nil {
		return err
	}

	go pool.RunHealthChecks(ctx, time.Duration(flagHealthInterval)*time.Second)

	log.Info("relay active", "listen", flagListen, "backends", pool.snapshot())
	server := NewServer(pool, log.GetLogger())
	serveErr := server.serve(ln, ctx.Done())
	log.Info("shut down")
	return serveErr
}

func portOf(addr string) string {
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "443"
	}
	return port
}

// addLoopbackAlias makes 127.0.0.x (x != 1) bindable on macOS, mirroring
// `ifconfig lo0 alias <ip> up`. Only works as root; on Linux it is never
// needed because all of 127/8 is bound to lo by default.
func addLoopbackAlias(host string) error {
	if runtime.GOOS != "darwin" {
		return fmt.Errorf("automatic loopback aliases are only supported on macOS")
	}
	out, err := exec.Command("ifconfig", "lo0", "alias", host, "up").CombinedOutput()
	if err != nil {
		return fmt.Errorf("ifconfig lo0 alias %s up: %v: %s", host, err, string(out))
	}
	return nil
}
