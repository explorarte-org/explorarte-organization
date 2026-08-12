package corpussemantic

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

func hashStrings(sortedItems []string) string {
	digest := sha256.Sum256([]byte(strings.Join(sortedItems, "\x00")))
	return "scluster-" + hex.EncodeToString(digest[:])[:16]
}
