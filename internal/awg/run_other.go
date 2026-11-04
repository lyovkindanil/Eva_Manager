//go:build !linux

package awg

import (
	"context"
	"errors"
	"log/slog"
	"net/netip"
)

// RunOptions — см. run_linux.go.
type RunOptions struct {
	Iface        string
	Addr         netip.Prefix
	MTU          int
	SocketDir    string
	NAT          bool
	AllowPrivate bool
	Log          *slog.Logger
	Ready        func()
}

// Run — AmneziaWG на сервере работает только на Linux.
func Run(context.Context, RunOptions) error {
	return errors.New("AmneziaWG на сервере поддерживается только на Linux")
}
