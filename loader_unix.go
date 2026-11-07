//go:build (darwin && !ios) || (linux && !android)

package cactus

import (
	"fmt"

	"github.com/ebitengine/purego"
)

func loadNative(path string, generation int) (*nativeAPI, error) {
	handle, err := purego.Dlopen(path, purego.RTLD_NOW|purego.RTLD_LOCAL)
	if err != nil {
		return nil, fmt.Errorf("cactus: load native library: %w", err)
	}
	api, err := registerNative(handle, purego.Dlsym, generation)
	if err != nil {
		_ = purego.Dlclose(handle)
		return nil, err
	}
	return api, nil
}
