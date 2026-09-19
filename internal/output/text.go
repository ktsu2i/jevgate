package output

import (
	"fmt"
	"io"

	"github.com/ktsu2i/jevgate/internal/gate"
)

// WriteText writes the human-readable representation of a gate result.
func WriteText(w io.Writer, result gate.Result) error {
	if err := result.Validate(); err != nil {
		return fmt.Errorf("output: %w", err)
	}

	verdict := "HUMAN REVIEW REQUIRED"
	if result.AIApprovalAllowed {
		verdict = "ALLOW"
	}
	data := fmt.Appendf(nil,
		"AI approval allowed: %.1f%%\nThreshold:           %.1f%%\n\n%s\n",
		result.Confidence*100,
		result.Threshold*100,
		verdict,
	)
	return write(w, data)
}

func write(w io.Writer, data []byte) error {
	n, err := w.Write(data)
	if err != nil {
		return err
	}
	if n != len(data) {
		return io.ErrShortWrite
	}
	return nil
}
