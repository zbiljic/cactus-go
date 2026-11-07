//go:build (!darwin && !linux && !windows) || android || ios

package cactus

import (
	"fmt"
	"runtime"
)

func loadNative(string, int) (*nativeAPI, error) {
	return nil, fmt.Errorf("%w: %s/%s", ErrUnsupportedPlatform, runtime.GOOS, runtime.GOARCH)
}
