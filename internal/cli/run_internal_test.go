package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ktsu2i/jevgate/internal/gate"
	gitpkg "github.com/ktsu2i/jevgate/internal/git"
	"github.com/ktsu2i/jevgate/internal/jev"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testAPIKey = "test-api-key"

type repositoryFixture struct {
	root   string
	cwd    string
	baseID string
	headID string
}

func createRepository(t *testing.T) repositoryFixture {
	t.Helper()

	root := t.TempDir()
	runGit(t, root, "init", "--quiet", "--initial-branch=main")
	runGit(t, root, "config", "user.name", "JevGate Test")
	runGit(t, root, "config", "user.email", "jevgate@example.invalid")
	require.NoError(t, os.WriteFile(filepath.Join(root, "app.txt"), []byte("before\n"), 0o600))
	runGit(t, root, "add", "app.txt")
	runGit(t, root, "commit", "--quiet", "--message", "base")
	baseID := strings.TrimSpace(runGit(t, root, "rev-parse", "HEAD"))
	runGit(t, root, "switch", "--quiet", "--create", "feature")
	require.NoError(t, os.WriteFile(filepath.Join(root, "app.txt"), []byte("after\n"), 0o600))
	runGit(t, root, "add", "app.txt")
	runGit(t, root, "commit", "--quiet", "--message", "head")
	headID := strings.TrimSpace(runGit(t, root, "rev-parse", "HEAD"))

	cwd := filepath.Join(root, "nested")
	require.NoError(t, os.Mkdir(cwd, 0o700))
	return repositoryFixture{root: root, cwd: cwd, baseID: baseID, headID: headID}
}

func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()

	cmd := exec.CommandContext(t.Context(), "git", args...)
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	require.NoError(t, err, "git %v: %s", args, output)
	return string(output)
}

type recordingAssessor struct {
	assessment jev.Assessment
	err        error
	calls      int
	diff       gitpkg.Diff
	context    string
	contextKey any
	contextVal any
}

func (a *recordingAssessor) Assess(ctx context.Context, diff gitpkg.Diff, repoContext string) (jev.Assessment, error) {
	a.calls++
	a.diff = diff
	a.context = repoContext
	if a.contextKey != nil {
		a.contextVal = ctx.Value(a.contextKey)
	}
	return a.assessment, a.err
}

type dependencyRecorder struct {
	cwd          string
	apiKey       string
	found        bool
	assessor     gate.Assessor
	requestedEnv []string
	receivedKey  string
	factoryCalls int
}

func (r *dependencyRecorder) dependencies() dependencies {
	return dependencies{
		getwd: func() (string, error) { return r.cwd, nil },
		lookupEnv: func(name string) (string, bool) {
			r.requestedEnv = append(r.requestedEnv, name)
			return r.apiKey, r.found
		},
		newAssessor: func(apiKey string) gate.Assessor {
			r.factoryCalls++
			r.receivedKey = apiKey
			return r.assessor
		},
	}
}

func runWithDependencies(ctx context.Context, t *testing.T, args []string, stdout io.Writer, recorder *dependencyRecorder) (code int, diagnostic string) {
	t.Helper()

	var stderr bytes.Buffer
	code = run(ctx, args, stdout, &stderr, recorder.dependencies())
	return code, stderr.String()
}

func TestRunEvaluatesAndWritesBothOutputFormats(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		assessment float64
		wantCode   int
		wantOutput string
	}{
		{
			name:       "positional revisions and text allow",
			args:       []string{"main", "HEAD"},
			assessment: 0.97,
			wantCode:   ExitAllow,
			wantOutput: "AI approval allowed: 97.0%\nThreshold:           95.0%\n\nALLOW\n",
		},
		{
			name:       "revision flags and JSON human review",
			args:       []string{"--base", "main", "--head", "HEAD", "--format", "json"},
			assessment: 0.80,
			wantCode:   ExitHumanReview,
			wantOutput: "{\"ai_approval_allowed\":false,\"confidence\":0.8,\"threshold\":0.95}\n",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := createRepository(t)
			assessor := &recordingAssessor{assessment: jev.Assessment{AIApprovalAllowedProbability: test.assessment}}
			recorder := &dependencyRecorder{cwd: fixture.cwd, apiKey: testAPIKey, found: true, assessor: assessor}
			var stdout bytes.Buffer

			code, stderr := runWithDependencies(context.Background(), t, test.args, &stdout, recorder)

			assert.Equal(t, test.wantCode, code)
			assert.Equal(t, test.wantOutput, stdout.String())
			assert.Empty(t, stderr)
			assert.Equal(t, 1, assessor.calls)
			assert.Equal(t, []string{apiKeyEnvironment}, recorder.requestedEnv)
			assert.Equal(t, testAPIKey, recorder.receivedKey)
			assert.Equal(t, fixture.baseID, assessor.diff.BaseID)
			assert.Equal(t, fixture.headID, assessor.diff.HeadID)
			require.Len(t, assessor.diff.Files, 1)
			assert.Equal(t, "app.txt", assessor.diff.Files[0].Path())
			assert.Contains(t, assessor.diff.Patch, "-before")
			assert.Contains(t, assessor.diff.Patch, "+after")
		})
	}
}

func TestRunLoadsRootConfigFromSubdirectory(t *testing.T) {
	fixture := createRepository(t)
	require.NoError(t, os.WriteFile(filepath.Join(fixture.root, ".jevgate.yml"), []byte("threshold: 0.8\ncontext: |\n  repository facts\n"), 0o600))
	type contextKey string
	key := contextKey("request")
	assessor := &recordingAssessor{
		assessment: jev.Assessment{AIApprovalAllowedProbability: 0.8},
		contextKey: key,
	}
	recorder := &dependencyRecorder{cwd: fixture.cwd, apiKey: testAPIKey, found: true, assessor: assessor}
	var stdout bytes.Buffer

	code, stderr := runWithDependencies(context.WithValue(context.Background(), key, "same context"), t, []string{"main", "HEAD"}, &stdout, recorder)

	assert.Equal(t, ExitAllow, code)
	assert.Empty(t, stderr)
	assert.Contains(t, stdout.String(), "Threshold:           80.0%")
	assert.Equal(t, "repository facts\n", assessor.context)
	assert.Equal(t, "same context", assessor.contextVal)
}

func TestRunExplicitZeroThresholdOverridesConfig(t *testing.T) {
	fixture := createRepository(t)
	require.NoError(t, os.WriteFile(filepath.Join(fixture.root, ".jevgate.yml"), []byte("threshold: 1\n"), 0o600))
	assessor := &recordingAssessor{assessment: jev.Assessment{AIApprovalAllowedProbability: 0}}
	recorder := &dependencyRecorder{cwd: fixture.cwd, apiKey: testAPIKey, found: true, assessor: assessor}
	var stdout bytes.Buffer

	code, stderr := runWithDependencies(context.Background(), t, []string{"main", "HEAD", "--threshold", "0", "--format", "json"}, &stdout, recorder)

	assert.Equal(t, ExitAllow, code)
	assert.Empty(t, stderr)
	assert.Equal(t, "{\"ai_approval_allowed\":true,\"confidence\":0,\"threshold\":0}\n", stdout.String())
}

func TestRunDoesNotAssessInvalidOrUnevaluableInput(t *testing.T) {
	tests := []struct {
		name       string
		prepare    func(repositoryFixture) error
		args       []string
		apiKey     string
		keyFound   bool
		wantError  string
		wantLookup bool
	}{
		{
			name: "invalid config",
			prepare: func(fixture repositoryFixture) error {
				return os.WriteFile(filepath.Join(fixture.root, ".jevgate.yml"), []byte("unknown: true\n"), 0o600)
			},
			args:      []string{"main", "HEAD", "--format", "json"},
			apiKey:    testAPIKey,
			keyFound:  true,
			wantError: "unknown key",
		},
		{
			name:      "invalid revision",
			args:      []string{"missing", "HEAD", "--format", "json"},
			apiKey:    testAPIKey,
			keyFound:  true,
			wantError: "unusable revision",
		},
		{
			name:      "empty diff",
			args:      []string{"HEAD", "HEAD", "--format", "json"},
			apiKey:    testAPIKey,
			keyFound:  true,
			wantError: "no change to evaluate",
		},
		{
			name:       "missing API key",
			args:       []string{"main", "HEAD", "--format", "json"},
			wantError:  apiKeyEnvironment,
			wantLookup: true,
		},
		{
			name:       "empty API key",
			args:       []string{"main", "HEAD", "--format", "json"},
			apiKey:     " \t",
			keyFound:   true,
			wantError:  apiKeyEnvironment,
			wantLookup: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := createRepository(t)
			if test.prepare != nil {
				require.NoError(t, test.prepare(fixture))
			}
			assessor := &recordingAssessor{assessment: jev.Assessment{AIApprovalAllowedProbability: 1}}
			recorder := &dependencyRecorder{cwd: fixture.cwd, apiKey: test.apiKey, found: test.keyFound, assessor: assessor}
			var stdout bytes.Buffer

			code, stderr := runWithDependencies(context.Background(), t, test.args, &stdout, recorder)

			assert.Equal(t, ExitError, code)
			assert.Empty(t, stdout.String(), "an error must not be encoded as a normal JSON result")
			assert.Contains(t, stderr, test.wantError)
			assert.Equal(t, 1, strings.Count(stderr, "\n"), "want one diagnostic: %q", stderr)
			assert.Equal(t, 0, assessor.calls)
			assert.Equal(t, 0, recorder.factoryCalls)
			if test.wantLookup {
				assert.Equal(t, []string{apiKeyEnvironment}, recorder.requestedEnv)
			} else {
				assert.Empty(t, recorder.requestedEnv)
			}
		})
	}
}

func TestRunReportsAssessmentAndOutputErrors(t *testing.T) {
	t.Run("assessment error", func(t *testing.T) {
		fixture := createRepository(t)
		assessor := &recordingAssessor{err: errors.New("assessment unavailable")}
		recorder := &dependencyRecorder{cwd: fixture.cwd, apiKey: testAPIKey, found: true, assessor: assessor}
		var stdout bytes.Buffer

		code, stderr := runWithDependencies(context.Background(), t, []string{"main", "HEAD", "--format", "json"}, &stdout, recorder)

		assert.Equal(t, ExitError, code)
		assert.Empty(t, stdout.String())
		assert.Contains(t, stderr, "assessment unavailable")
		assert.Equal(t, 1, assessor.calls)
	})

	t.Run("output error", func(t *testing.T) {
		fixture := createRepository(t)
		assessor := &recordingAssessor{assessment: jev.Assessment{AIApprovalAllowedProbability: 1}}
		recorder := &dependencyRecorder{cwd: fixture.cwd, apiKey: testAPIKey, found: true, assessor: assessor}

		code, stderr := runWithDependencies(context.Background(), t, []string{"main", "HEAD", "--format", "json"}, errorWriter{err: errors.New("disk full")}, recorder)

		assert.Equal(t, ExitError, code)
		assert.Contains(t, stderr, "write output")
		assert.Contains(t, stderr, "disk full")
		assert.Equal(t, 1, assessor.calls)
	})
}

func TestRunInformationOutputErrorDoesNotUseDependencies(t *testing.T) {
	deps := dependencies{
		getwd: func() (string, error) {
			t.Fatal("help must not inspect the working directory")
			return "", nil
		},
	}
	var stderr bytes.Buffer

	code := run(context.Background(), []string{"--help"}, errorWriter{err: errors.New("closed")}, &stderr, deps)

	assert.Equal(t, ExitError, code)
	assert.Contains(t, stderr.String(), "write output")
}

func TestRunArgumentErrorDoesNotUseDependencies(t *testing.T) {
	deps := dependencies{
		getwd: func() (string, error) {
			t.Fatal("invalid arguments must not inspect the working directory")
			return "", nil
		},
	}
	var stdout, stderr bytes.Buffer

	code := run(context.Background(), []string{"main"}, &stdout, &stderr, deps)

	assert.Equal(t, ExitError, code)
	assert.Empty(t, stdout.String())
	assert.Contains(t, stderr.String(), "exactly 2 revisions")
}

type errorWriter struct {
	err error
}

func (w errorWriter) Write([]byte) (int, error) {
	return 0, w.err
}
