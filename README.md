# zcode-proxy

A TCP proxy with automatic failover, written in Go. It relays a local listen
address to an ordered list of upstream backends (by default the z.ai
endpoints `8.217.233.95:443` and `8.217.100.151:443`), routing traffic to
the remaining backends when one goes down and restoring preference order
once it recovers.

It exists to get around the API failing with "unable to authenticate" when
ping latency to it is over about 250ms - by relaying through the fastest
healthy z.ai IP instead of leaving the route to chance, the proxy keeps the
round-trip under that threshold and fails over automatically when an IP
goes slow or drops.

## Disclaimer

This is a personal tool that I built to solve a problem I was experiencing. It works well for my use case, but it comes with no guarantees, warranties, or support.

Use it entirely at your own risk. I am not responsible for any data loss, service disruption, account restrictions, unexpected behaviour, or other issues that may result from using this software.

In short: it works for me, but if it sets your house on fire, deletes your codebase, wipes your backups, or gets your account suspended, that's on you. You've been warned.

## Install

### Homebrew

```sh
brew install paularlott/tap/zcode-proxy
```

### From source

Requires [Go](https://go.dev) and [Task](https://taskfile.dev):

```sh
task build
# Binary at dist/zcode-proxy
```

## Usage

Point `api.z.ai` at the proxy by adding it to `/etc/hosts` (once):

```sh
echo "127.0.0.2 api.z.ai" | sudo tee -a /etc/hosts
```

If macOS keeps using the old lookup, flush the DNS cache:
`sudo dscacheutil -flushcache; sudo killall -HUP mDNSResponder`.

Then start the proxy:

```sh
# Default: listen on 127.0.0.2:443, failover between the two z.ai IPs.
# Binding a 127.0.0.x alias on macOS needs root; the binary adds the
# alias itself.
sudo zcode-proxy

# Equivalent, explicit:
sudo zcode-proxy --listen 127.0.0.2:443 \
                 --backend 8.217.233.95:443 \
                 --backend 8.217.100.151:443

# Any other listen address works without root where the port allows it:
zcode-proxy --listen 127.0.0.1:9443
```

Run `zcode-proxy --help` for all flags — backend health check intervals,
dial timeouts, failover thresholds, log level and format.

## License

MIT License — Copyright (c) 2026 Paul Arlott. See [LICENSE.txt](LICENSE.txt).
