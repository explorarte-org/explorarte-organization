package modelidentity

import "errors"

var (
	ErrInvalidPolicy       = errors.New("invalid model execution identity policy")
	ErrPolicyNotFound      = errors.New("model execution identity policy not found")
	ErrPolicyConflict      = errors.New("model execution identity policy conflict")
	ErrPolicyStale         = errors.New("model execution identity policy is not synchronized")
	ErrInvalidKey          = errors.New("invalid model execution identity key")
	ErrKeyNotFound         = errors.New("model execution identity key not found")
	ErrKeyConflict         = errors.New("model execution identity key conflict")
	ErrKeyInactive         = errors.New("model execution identity key is inactive")
	ErrChallengeNotFound   = errors.New("model execution identity challenge not found")
	ErrChallengeExpired    = errors.New("model execution identity challenge expired")
	ErrChallengeConsumed   = errors.New("model execution identity challenge already consumed")
	ErrAssertionInvalid    = errors.New("model execution identity assertion invalid")
	ErrReplayDenied        = errors.New("model execution identity replay denied")
	ErrAuthorizationDenied = errors.New("model execution identity administration denied")
	ErrConflict            = errors.New("model execution identity store transient conflict")
)
