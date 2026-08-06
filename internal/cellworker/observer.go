package cellworker

// Observer receives operational events from Worker.Run that would
// otherwise be silently discarded. Implementations must return promptly:
// they are called synchronously from the poll loop or a dispatch goroutine.
type Observer interface {
	// OnListError is called whenever WorkSource.ListEligible returns an
	// error. The poll loop backs off and retries regardless.
	OnListError(err error)
	// OnDispatchError is called whenever Dispatcher.Dispatch returns a
	// non-nil error for invocationID.
	OnDispatchError(invocationID int64, err error)
}

// NoopObserver discards every event. It is the default when no Observer is
// supplied.
type NoopObserver struct{}

func (NoopObserver) OnListError(error)            {}
func (NoopObserver) OnDispatchError(int64, error) {}
