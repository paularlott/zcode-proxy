module github.com/paularlott/zcode-proxy

go 1.27.1

require (
	github.com/paularlott/cli v0.0.0
	github.com/paularlott/logger v0.0.0
)

require (
	github.com/fsnotify/fsnotify v1.9.0 // indirect
	golang.org/x/sys v0.42.0 // indirect
)

replace github.com/paularlott/cli => ../cli

replace github.com/paularlott/logger => ../logger
