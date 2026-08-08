package modelpricing

import "errors"

var (
	ErrInvalidPriceTier  = errors.New("invalid model price tier")
	ErrNoPricingResolved = errors.New("no priced tier for this provider/model")
	ErrConflict          = errors.New("model pricing conflict")
)
