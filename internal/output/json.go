package output

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/ktsu2i/jevgate/internal/gate"
)

// WriteJSON writes the machine-readable representation of a gate result.
func WriteJSON(w io.Writer, result gate.Result) error {
	if err := result.Validate(); err != nil {
		return fmt.Errorf("output: %w", err)
	}

	data, err := json.Marshal(result)
	if err != nil {
		return fmt.Errorf("output: encode JSON: %w", err)
	}
	data = append(data, '\n')
	return write(w, data)
}
