package output

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"math"
	"testing"

	"github.com/ktsu2i/jevgate/internal/gate"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWriteJSON(t *testing.T) {
	t.Parallel()

	tests := []gate.Result{
		{AIApprovalAllowed: true, Confidence: 0.9496, Threshold: 0.95},
		{AIApprovalAllowed: false, Confidence: 0, Threshold: 0},
	}
	for _, result := range tests {
		var encoded bytes.Buffer
		require.NoError(t, WriteJSON(&encoded, result))
		assert.Equal(t, byte('\n'), encoded.Bytes()[encoded.Len()-1])

		var got map[string]any
		require.NoError(t, json.Unmarshal(encoded.Bytes(), &got))
		assert.Equal(t, map[string]any{
			"ai_approval_allowed": result.AIApprovalAllowed,
			"confidence":          result.Confidence,
			"threshold":           result.Threshold,
		}, got)
		assert.Len(t, got, 3)
	}
}

func TestWriteJSONRejectsInvalidResultBeforeWriting(t *testing.T) {
	t.Parallel()

	for _, result := range []gate.Result{
		{Confidence: math.NaN(), Threshold: 0.95},
		{Confidence: 0.5, Threshold: math.Inf(1)},
	} {
		writer := &recordingWriter{}
		err := WriteJSON(writer, result)
		require.Error(t, err)
		assert.Equal(t, 0, writer.calls)
	}
}

func TestWriteJSONReturnsWriterError(t *testing.T) {
	t.Parallel()

	want := errors.New("write sentinel")
	err := WriteJSON(errorWriter{err: want}, gate.Result{Confidence: 0.5, Threshold: 0.95})
	require.ErrorIs(t, err, want)
}

func TestWriteJSONRejectsShortWrite(t *testing.T) {
	t.Parallel()

	err := WriteJSON(shortWriter{}, gate.Result{Confidence: 0.5, Threshold: 0.95})
	require.ErrorIs(t, err, io.ErrShortWrite)
}
