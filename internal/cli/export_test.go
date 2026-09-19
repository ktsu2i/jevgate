package cli

import (
	"context"
	"io"

	"github.com/ktsu2i/jevgate/internal/gate"
)

// RunDependencies exposes the narrow dependency seam to the external test package.
type RunDependencies struct {
	Getwd       func() (string, error)
	LookupEnv   func(string) (string, bool)
	NewAssessor func(string) gate.Assessor
}

// RunInternal exposes run to the external test package.
func RunInternal(ctx context.Context, args []string, stdout, stderr io.Writer, deps RunDependencies) int {
	return run(ctx, args, stdout, stderr, dependencies{
		getwd:       deps.Getwd,
		lookupEnv:   deps.LookupEnv,
		newAssessor: deps.NewAssessor,
	})
}
