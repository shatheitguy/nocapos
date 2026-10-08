//go:build windows

package docker

import (
	"context"
	"net"

	"github.com/Microsoft/go-winio"
)

func pipeDialer(path string) (dialFunc, error) {
	return func(ctx context.Context) (net.Conn, error) { return winio.DialPipeContext(ctx, path) }, nil
}
