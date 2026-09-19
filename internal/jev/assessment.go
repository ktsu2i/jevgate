// Package jev obtains the probability that a change may rely on AI approval.
// Threshold comparison and the resulting gate decision belong to the caller.
package jev

// Assessment contains Jev's affirmative probability, not a separate confidence
// metric. A successful Assess always returns a finite value in [0, 1].
type Assessment struct {
	AIApprovalAllowedProbability float64
}
