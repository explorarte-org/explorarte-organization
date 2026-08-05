package authorization

func CanTransition(from, to RequestStatus) bool {
	switch from {
	case RequestPending:
		return to == RequestApproved || to == RequestRejected || to == RequestCancelled || to == RequestExpired
	case RequestApproved:
		return to == RequestConsumed || to == RequestCancelled || to == RequestExpired
	default:
		return false
	}
}
