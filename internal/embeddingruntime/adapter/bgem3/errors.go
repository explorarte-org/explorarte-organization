package bgem3

import "errors"

var (
	ErrQueueFull          = errors.New("embeddingruntime bge-m3: bounded queue is full, request rejected")
	ErrModelIdentityDrift = errors.New("embeddingruntime bge-m3: sidecar reported a model revision or artifact hash different from the pinned configuration")
	ErrInvalidVector      = errors.New("embeddingruntime bge-m3: sidecar returned an invalid vector (wrong dimension, empty, or non-finite component)")
	ErrDisabled           = errors.New("embeddingruntime bge-m3: adapter is disabled")
)
