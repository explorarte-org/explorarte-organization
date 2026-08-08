// Package modelpricing holds the versioned, per-provider/per-model price
// card used to estimate and reconcile the real USD cost of a model call.
// Resolution is fail-closed: a provider/model combination with no priced
// tier is rejected rather than treated as free, the same way an unrotated
// placeholder credential is a defect rather than a default.
package modelpricing
