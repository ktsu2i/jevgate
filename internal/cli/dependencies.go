package cli

import (
	"os"

	"github.com/ktsu2i/jevgate/internal/gate"
	"github.com/ktsu2i/jevgate/internal/jev"
)

type dependencies struct {
	getwd       func() (string, error)
	lookupEnv   func(string) (string, bool)
	newAssessor func(string) gate.Assessor
}

func productionDependencies() dependencies {
	return dependencies{
		getwd:     os.Getwd,
		lookupEnv: os.LookupEnv,
		newAssessor: func(apiKey string) gate.Assessor {
			return jev.NewClient(apiKey, nil)
		},
	}
}
