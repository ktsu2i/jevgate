package output_test

import (
	"bytes"
	"errors"
	"io"
	"math"
	"testing"

	"github.com/ktsu2i/jevgate/internal/gate"
	"github.com/ktsu2i/jevgate/internal/output"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWriteText(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		result gate.Result
		want   string
	}{
		{
			name:   "allow",
			result: gate.Result{AIApprovalAllowed: true, Confidence: 0.972, Threshold: 0.95},
			want:   "AI approval allowed: 97.2%\nThreshold:           95.0%\n\nALLOW\n",
		},
		{
			name:   "human review with rounded display",
			result: gate.Result{AIApprovalAllowed: false, Confidence: 0.9496, Threshold: 0.95},
			want:   "AI approval allowed: 95.0%\nThreshold:           95.0%\n\nHUMAN REVIEW REQUIRED\n",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			var got bytes.Buffer
			require.NoError(t, output.WriteText(&got, test.result))
			assert.Equal(t, test.want, got.String())
		})
	}
}

func TestWriteTextRejectsInvalidResultBeforeWriting(t *testing.T) {
	t.Parallel()

	writer := &recordingWriter{}
	err := output.WriteText(writer, gate.Result{Confidence: math.NaN(), Threshold: 0.95})
	require.Error(t, err)
	assert.Equal(t, 0, writer.calls)
}

func TestWriteTextReturnsWriterError(t *testing.T) {
	t.Parallel()

	want := errors.New("write sentinel")
	err := output.WriteText(errorWriter{err: want}, gate.Result{Confidence: 0.5, Threshold: 0.95})
	require.ErrorIs(t, err, want)
}

func TestWriteTextRejectsShortWrite(t *testing.T) {
	t.Parallel()

	err := output.WriteText(shortWriter{}, gate.Result{Confidence: 0.5, Threshold: 0.95})
	require.ErrorIs(t, err, io.ErrShortWrite)
}

type recordingWriter struct {
	calls int
}

func (w *recordingWriter) Write(data []byte) (int, error) {
	w.calls++
	return len(data), nil
}

type errorWriter struct {
	err error
}

func (w errorWriter) Write([]byte) (int, error) {
	return 0, w.err
}

type shortWriter struct{}

func (shortWriter) Write(data []byte) (int, error) {
	return len(data) - 1, nil
}
