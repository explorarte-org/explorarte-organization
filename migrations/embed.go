package migrations

import "embed"

// Files contains the versioned SQL migrations compiled into orgd and orgctl.
//
//go:embed *.sql
var Files embed.FS
