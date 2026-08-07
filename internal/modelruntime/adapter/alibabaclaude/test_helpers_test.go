package alibabaclaude

import (
	"crypto/sha256"
	"encoding/hex"
)

func sha256Hex(value []byte) string {
	sum := sha256.Sum256(value)
	return hex.EncodeToString(sum[:])
}
