//go:build !windows

package capture

import "context"

func loopback(context.Context, func(Stream), Handler) error { return ErrUnsupported }
