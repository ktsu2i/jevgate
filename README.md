# jevgate

**Decide when a change can rely on an AI code reviewer's approval.**

English | [日本語](README.ja.md)

jevgate is a CLI for teams that use AI code reviewers. It compares two Git revisions and uses [Jev](https://docs.typesafe.ai/api) to assess whether the change is simple enough to rely on AI approval without requiring human approval. If the score falls below your threshold, the change needs human review.

jevgate makes that decision; your reviewer still reviews the code. It does not submit pull request approvals, merge changes, or change your repository's approval rules.

- Evaluate the entire change, including interactions between changed files.
- Set an approval threshold and provide context about your repository.
- Read the result in your terminal or use JSON and exit codes in CI.

## Quick start

### 1. Install from source

You need **Go 1.26 or later**, **Git**, and a **Jev API key**. Evaluation requires network access to the Jev API. Prebuilt release binaries are not yet available.

```sh
git clone https://github.com/ktsu2i/jevgate.git
cd jevgate
go install ./cmd/jevgate
```

Go installs the executable in `GOBIN`, or `$(go env GOPATH)/bin` if `GOBIN` is unset. Make sure that directory is on your `PATH`, then check the installation:

```sh
jevgate --help
```

### 2. Set your API key

```sh
export JEV_API_KEY='your-api-key'
```

Use the `JEV_API_KEY` environment variable for credentials; do not put your key in the configuration file. See the [Jev API documentation](https://docs.typesafe.ai/api) for the service's authentication details.

### 3. Evaluate a change

From the Git repository you want to evaluate, compare your base branch with your current commit:

```sh
jevgate main HEAD
```

Replace `main` with your base branch or commit. The two revisions must contain a change. An example result:

```text
AI approval allowed: 97.2%
Threshold:           95.0%

ALLOW
```

`ALLOW` means the score meets the threshold for relying on AI approval. A score below the threshold produces `HUMAN REVIEW REQUIRED`. The default threshold is `0.95`.

## Usage

Pass two branches, tags, or commit SHAs, either as positional arguments or with `--base` and `--head`:

```sh
jevgate main HEAD
jevgate --base main --head HEAD
jevgate origin/main HEAD --threshold 0.98
jevgate main HEAD --format json
```

Both revisions are resolved to commit SHAs and compared directly. jevgate does not select a merge base automatically. Pass revisions separately: range expressions such as `main..HEAD` and `main...HEAD` are not supported. Uncommitted changes and untracked files are excluded.

| Option | Description | Default |
| --- | --- | --- |
| `--base <revision>` | Base revision; use together with `--head`, without positional revisions | Required if using flags |
| `--head <revision>` | Head revision; use together with `--base`, without positional revisions | Required if using flags |
| `--config <path>` | Configuration file; relative paths start at the current working directory | `.jevgate.yml` in the repository root |
| `--threshold <number>` | Minimum score for allowing AI approval, from `0` to `1` inclusive | `0.95` |
| `--format text\|json` | Output format | `text` |
| `-h`, `--help` | Show help | — |
| `--version` | Show version | — |

## Configuration

Optionally create `.jevgate.yml` at the root of the repository you are evaluating:

```yaml
threshold: 0.95

context: |
  This repository contains a Go API deployed to ECS.
  Files under docs/ contain documentation.
```

`context` supplies repository facts to help Jev interpret the change. The CLI threshold takes precedence over the configuration file, which takes precedence over the default. Without a configuration file, jevgate uses `0.95` and no additional context.

To use a different configuration file:

```sh
jevgate main HEAD --config ./review-policy.yml
```

## Understanding the result

jevgate allows AI approval when `confidence >= threshold`. `confidence` is Jev's affirmative probability for the question of whether AI approval is sufficient. A low value means human review is required; it does not mean the change is dangerous.

The assessment looks for clearly understood, simple changes such as documentation corrections or comment-only edits. Its criteria call for human approval for changes to business logic, authentication, infrastructure, dependencies, or other behavior. File paths and change types never grant an automatic exemption.

With `--format json`, the result contains three fields:

```json
{
  "ai_approval_allowed": true,
  "confidence": 0.972,
  "threshold": 0.95
}
```

| Exit code | Meaning |
| --- | --- |
| `0` | AI approval is allowed |
| `1` | Human review is required |
| `2` | Evaluation failed because of an input, configuration, Git, API, or output error |

Decisions go to stdout; diagnostics go to stderr. Exit code `2` means no usable decision was produced. Always check the exit code before consuming JSON: an output error can leave a partial result.

## Use in CI

Run the CLI in a checkout containing both revisions, provide `JEV_API_KEY` through your CI secret store, and pass the exact base and head commit SHAs. A shallow checkout may need additional history before either revision can be resolved.

For a check that passes only when AI approval is allowed, use the CLI's exit code directly. If your workflow routes changes to different reviewers, handle exit code `1` as a decision requiring human review and exit code `2` as an execution failure. Only pass successfully parsed JSON from exit codes `0` or `1` to downstream steps; do not convert errors into a score of zero.

jevgate does not provide a dedicated GitHub Action or a ready-to-use GitHub Actions workflow yet. The CLI's result must be connected to your review and approval process separately.

## Data sent to Jev and input limits

jevgate sends the diff, changed file paths and metadata, and your configured `context` to the Jev API for evaluation.

It evaluates the complete change without truncating the diff or excluding files by path. Evaluation stops with exit code `2` for:

- Empty diffs, binary changes, or changes involving submodules.
- Diffs or file paths that cannot be represented faithfully, including invalid UTF-8.
- Changes that exceed the input limits: 128 KiB for collected Git output and 128 KiB for the encoded API request, including context and evaluation instructions.

## Troubleshooting

| Problem | What to check |
| --- | --- |
| `jevgate: command not found` | Add Go's install directory (`GOBIN` or `$(go env GOPATH)/bin`) to `PATH`. |
| `JEV_API_KEY is not set or is empty` | Export your API key in the shell or CI step that runs jevgate. |
| `unusable revision` | Check the branch or commit name and fetch any missing history. |
| `there is no change to evaluate` | Choose two commits with different contents; working tree edits are not included. |
| Exit code `2` | Read stderr for the cause. No approval decision is available. |

For bugs and feature requests, [open an issue](https://github.com/ktsu2i/jevgate/issues).
