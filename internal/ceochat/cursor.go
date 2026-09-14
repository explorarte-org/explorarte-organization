package ceochat

import (
	"encoding/base64"
	"fmt"
	"strconv"
)

// encodeOffsetCursor and decodeOffsetCursor implement the one pagination
// shape this round's capabilities need: an opaque, host-owned cursor over
// an offset a canonical service already accepts (tasks.TaskFilter.Offset).
// The model receives and returns only the opaque string; it can never
// construct, parse, or forge a SQL offset or query fragment of its own --
// an invalid or tampered cursor is rejected outright (ArgumentValidator's
// job), never guessed at or clamped into something "close enough".
const cursorPrefix = "ceochat-cursor-v1:"

func encodeOffsetCursor(nextOffset int) string {
	if nextOffset <= 0 {
		return ""
	}
	return cursorPrefix + base64.RawURLEncoding.EncodeToString([]byte(strconv.Itoa(nextOffset)))
}

func decodeOffsetCursor(cursor string) (int, error) {
	if cursor == "" {
		return 0, nil
	}
	if len(cursor) <= len(cursorPrefix) || cursor[:len(cursorPrefix)] != cursorPrefix {
		return 0, fmt.Errorf("%w: cursor is not a recognized ceochat cursor", ErrInvalidInput)
	}
	raw, err := base64.RawURLEncoding.DecodeString(cursor[len(cursorPrefix):])
	if err != nil {
		return 0, fmt.Errorf("%w: cursor is not decodable", ErrInvalidInput)
	}
	offset, err := strconv.Atoi(string(raw))
	if err != nil || offset < 0 {
		return 0, fmt.Errorf("%w: cursor does not encode a valid offset", ErrInvalidInput)
	}
	return offset, nil
}
