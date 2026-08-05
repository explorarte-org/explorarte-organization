package contextengine

func CanTransition(from, to SnapshotStatus) bool {
	return from == SnapshotReady && to == SnapshotInvalidated
}
