package cactus

import (
	"errors"
	"fmt"
)

// Sentinel errors for errors.Is. Returned errors wrap them with detail.
var (
	// ErrUnsupportedPlatform indicates that no shared-library backend or engine
	// build exists for the target.
	ErrUnsupportedPlatform = errors.New("cactus: unsupported platform")
	// ErrEngineNotFound indicates that a cached engine library is unavailable.
	ErrEngineNotFound = errors.New("cactus: engine library not found")
	// ErrUnsupportedGeneration indicates a generation other than 2 or 3, or a
	// feature the selected generation lacks.
	ErrUnsupportedGeneration = errors.New("cactus: unsupported generation")
	// ErrUnsupportedABI indicates a native library with an incomplete
	// capability set.
	ErrUnsupportedABI = errors.New("cactus: unsupported native ABI")
	// ErrEmbeddingUnsupported indicates that the model or engine has no
	// embeddings.
	ErrEmbeddingUnsupported = errors.New("cactus: embeddings unavailable")
	// ErrSpeechUnsupported indicates that the engine has no speech ABI.
	ErrSpeechUnsupported = errors.New("cactus: speech unavailable")
	// ErrValidation is wrapped by ValidateResponse, and by Run, when the engine
	// flags a response.
	ErrValidation = errors.New("cactus: response validation")
	// ErrStreamActive indicates that a live transcription is open on the
	// engine, so other speech calls must wait for its Stop.
	ErrStreamActive = errors.New("cactus: a live transcription is in progress")
	// ErrClosed is returned by an Agent or Whistle after Close.
	ErrClosed = errors.New("cactus: closed")
)

// NativeError reports a failed engine call. Detail carries the engine's
// diagnostic when one is available.
type NativeError struct {
	Operation string
	Code      int32
	Detail    string
}

func (e *NativeError) Error() string {
	if e.Detail != "" {
		return fmt.Sprintf("cactus: %s failed with code %d: %s", e.Operation, e.Code, e.Detail)
	}
	return fmt.Sprintf("cactus: %s failed with code %d", e.Operation, e.Code)
}
