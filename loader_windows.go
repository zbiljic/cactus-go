//go:build windows

package cactus

import (
	"fmt"
	"syscall"
)

func loadNative(path string) (*nativeAPI, error) {
	handle, err := syscall.LoadLibrary(path)
	if err != nil {
		return nil, fmt.Errorf("cactus: load native library: %w", err)
	}
	resolve := func(handle uintptr, name string) (uintptr, error) {
		return syscall.GetProcAddress(syscall.Handle(handle), name)
	}
	api, err := registerNative(uintptr(handle), resolve)
	if err != nil {
		_ = syscall.FreeLibrary(handle)
		return nil, err
	}
	return api, nil
}
