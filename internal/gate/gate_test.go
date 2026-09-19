package gate_test

import (
	"context"
	"errors"
	"math"
	"testing"

	"github.com/ktsu2i/jevgate/internal/gate"
	"github.com/ktsu2i/jevgate/internal/git"
	"github.com/ktsu2i/jevgate/internal/jev"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type stubAssessor struct {
	assessment jev.Assessment
	err        error
	calls      int
	ctxValue   string
	diff       git.Diff
	repo       string
}

type contextKey struct{}

func (a *stubAssessor) Assess(ctx context.Context, diff git.Diff, repoContext string) (jev.Assessment, error) {
	a.calls++
	ctxValue, ok := ctx.Value(contextKey{}).(string)
	if ok {
		a.ctxValue = ctxValue
	}
	a.diff = diff
	a.repo = repoContext
	return a.assessment, a.err
}

func TestEvaluateDecision(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		confidence float64
		threshold  float64
		allowed    bool
	}{
		{"above threshold", 0.97, 0.95, true},
		{"at threshold", 0.95, 0.95, true},
		{"below threshold", 0.949, 0.95, false},
		{"zero boundary", 0, 0, true},
		{"one boundary", 1, 1, true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			ctx := context.WithValue(context.Background(), contextKey{}, "context sentinel")
			diff := git.Diff{BaseID: "base", HeadID: "head", Patch: "diff sentinel"}
			assessor := &stubAssessor{assessment: jev.Assessment{AIApprovalAllowedProbability: test.confidence}}

			got, err := gate.Evaluate(ctx, assessor, diff, "repository sentinel", test.threshold)
			require.NoError(t, err)
			assert.Equal(t, gate.Result{
				AIApprovalAllowed: test.allowed,
				Confidence:        test.confidence,
				Threshold:         test.threshold,
			}, got)
			assert.Equal(t, 1, assessor.calls)
			assert.Equal(t, "context sentinel", assessor.ctxValue)
			assert.Equal(t, diff, assessor.diff)
			assert.Equal(t, "repository sentinel", assessor.repo)
		})
	}
}

func TestEvaluateRejectsInvalidThresholdBeforeAssessment(t *testing.T) {
	t.Parallel()

	for _, threshold := range []float64{-0.001, 1.001, math.NaN(), math.Inf(1), math.Inf(-1)} {
		t.Run("threshold", func(t *testing.T) {
			t.Parallel()

			assessor := &stubAssessor{assessment: jev.Assessment{AIApprovalAllowedProbability: 1}}
			got, err := gate.Evaluate(context.Background(), assessor, git.Diff{}, "", threshold)
			require.Error(t, err)
			assert.Equal(t, gate.Result{}, got)
			assert.Equal(t, 0, assessor.calls)
		})
	}
}

func TestEvaluateRejectsInvalidAssessment(t *testing.T) {
	t.Parallel()

	for _, confidence := range []float64{-0.001, 1.001, math.NaN(), math.Inf(1), math.Inf(-1)} {
		t.Run("confidence", func(t *testing.T) {
			t.Parallel()

			assessor := &stubAssessor{assessment: jev.Assessment{AIApprovalAllowedProbability: confidence}}
			got, err := gate.Evaluate(context.Background(), assessor, git.Diff{}, "", 0.95)
			require.Error(t, err)
			assert.Equal(t, gate.Result{}, got)
			assert.Equal(t, 1, assessor.calls)
		})
	}
}

func TestEvaluatePreservesAssessorError(t *testing.T) {
	t.Parallel()

	want := errors.New("assessment sentinel")
	assessor := &stubAssessor{err: want}

	got, err := gate.Evaluate(context.Background(), assessor, git.Diff{}, "", 0.95)
	require.ErrorIs(t, err, want)
	assert.Equal(t, gate.Result{}, got)
	assert.Equal(t, 1, assessor.calls)
}

func TestEvaluateRequiresAssessor(t *testing.T) {
	t.Parallel()

	got, err := gate.Evaluate(context.Background(), nil, git.Diff{}, "", 0.95)
	require.Error(t, err)
	assert.Equal(t, gate.Result{}, got)
}
