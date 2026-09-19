package gate

import (
	"context"
	"errors"
	"fmt"
	"math"

	"github.com/ktsu2i/jevgate/internal/git"
	"github.com/ktsu2i/jevgate/internal/jev"
)

// Assessor obtains Jev's assessment of a change.
type Assessor interface {
	Assess(ctx context.Context, diff git.Diff, repoContext string) (jev.Assessment, error)
}

// Result is the decision shared by command-line output formats.
type Result struct {
	AIApprovalAllowed bool `json:"ai_approval_allowed"`

	// Confidence is the affirmative probability that AI approval is sufficient.
	Confidence float64 `json:"confidence"`

	Threshold float64 `json:"threshold"`
}

// Evaluate assesses a change once and compares its affirmative probability to the threshold.
func Evaluate(ctx context.Context, assessor Assessor, diff git.Diff, repoContext string, threshold float64) (Result, error) {
	if err := validateProbability("threshold", threshold); err != nil {
		return Result{}, err
	}
	if assessor == nil {
		return Result{}, errors.New("gate: assessor is required")
	}

	assessment, err := assessor.Assess(ctx, diff, repoContext)
	if err != nil {
		return Result{}, fmt.Errorf("gate: assess change: %w", err)
	}
	confidence := assessment.AIApprovalAllowedProbability
	if err := validateProbability("confidence", confidence); err != nil {
		return Result{}, err
	}

	return Result{
		AIApprovalAllowed: confidence >= threshold,
		Confidence:        confidence,
		Threshold:         threshold,
	}, nil
}

// Validate rejects result values that cannot represent probabilities.
func (r Result) Validate() error {
	if err := validateProbability("confidence", r.Confidence); err != nil {
		return err
	}
	return validateProbability("threshold", r.Threshold)
}

func validateProbability(name string, value float64) error {
	if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 || value > 1 {
		return fmt.Errorf("gate: %s must be finite and within [0, 1], got %v", name, value)
	}
	return nil
}
