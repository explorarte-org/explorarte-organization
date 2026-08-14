package rag

import "time"

// canonicalPersistenceTime is the single place input time.Time values
// cross from "whatever precision the caller happened to construct" into
// "what this domain actually hashes and persists". PostgreSQL's
// timestamptz has microsecond resolution; Go's time.Now() has nanosecond
// resolution. A value that enters ComputeCanonicalHash() at nanosecond
// precision and is then stored/read back at microsecond precision no
// longer round-trips to the same hash -- RAG-INTEGRITY-001. Canonicalizing
// here, once, before the hash is ever computed, means the hash keeps
// meaning exactly what it says: the hash of the value that was actually
// persisted, not a value nobody can ever reproduce by reading it back.
func canonicalPersistenceTime(t time.Time) time.Time {
	return t.UTC().Truncate(time.Microsecond)
}
