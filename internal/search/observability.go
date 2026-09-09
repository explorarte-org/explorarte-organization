package search

type EventKind string

const (
	EventCacheHit             EventKind = "cache_hit"
	EventProviderStarted      EventKind = "provider_started"
	EventProviderCompleted    EventKind = "provider_completed"
	EventProviderInsufficient EventKind = "provider_insufficient"
	EventProviderFailed       EventKind = "provider_failed"
	EventCompleted            EventKind = "completed"
)

type Event struct {
	Kind      EventKind
	RequestID string
	Provider  string
	Intent    string
	CacheHit  bool
	Error     string
	Results   int
}

type Logger interface {
	Log(e Event)
}

type NoopLogger struct{}

func (NoopLogger) Log(Event) {}
