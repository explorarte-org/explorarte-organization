package modelegress

import "errors"

var (
	ErrInvalidPolicy      = errors.New("invalid model egress policy")
	ErrPolicyNotFound     = errors.New("model egress policy not found")
	ErrPolicyConflict     = errors.New("model egress policy conflict")
	ErrPolicyStale        = errors.New("model egress policy registry stale")
	ErrPolicyUnpinned     = errors.New("model egress policy unpinned")
	ErrEvaluationConflict = errors.New("model egress evaluation conflict")
	ErrClaimMismatch      = errors.New("model egress claim mismatch")
)
