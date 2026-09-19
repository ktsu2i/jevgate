// Package config loads the optional .jevgate.yml configuration.
//
// The configuration is deliberately tiny: it carries the threshold and free
// form repository context, and nothing that could move the decision out of the
// model and into deterministic rules. Keys the MVP does not support, such as
// safe_paths, are rejected rather than ignored, so that a configuration that
// looks like it weakens the gate never silently fails to.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"os"
	"path/filepath"

	"go.yaml.in/yaml/v3"
)

// DefaultThreshold is the probability required to allow AI approval when
// neither the command line nor a configuration file sets one.
const DefaultThreshold = 0.95

// FileName is the configuration file discovered in the repository root.
const FileName = ".jevgate.yml"

// Supported configuration keys.
const (
	keyThreshold = "threshold"
	keyContext   = "context"
)

// maxFileSize bounds how much of a configuration file is read. The context is
// free form text that ends up in an API request, so the amount accepted here
// stays finite; whether a context of a permitted size is too large for the API
// is checked when the request is built.
const maxFileSize = 1 << 20 // 1 MiB

// Config is the effective configuration after the file and the command line
// have been combined. It is separate from the YAML shape on purpose: the file
// may omit values, Config may not.
type Config struct {
	// Threshold is the minimum probability required to allow AI approval. It
	// is finite and within [0,1].
	Threshold float64

	// Context is the repository context exactly as written in the file, empty
	// when the file omits it.
	Context string
}

// Params describes where Load may look for a configuration file and what the
// command line overrides.
type Params struct {
	// RepoRoot is the Git repository root searched for FileName. It is
	// required unless Path is given.
	RepoRoot string

	// Cwd is the working directory a relative Path is resolved against, so
	// that --config keeps meaning what the shell means by it.
	Cwd string

	// Path is the --config value. Empty enables discovery in RepoRoot; a
	// non-empty Path that does not exist is an error and never falls back.
	Path string

	// Threshold and ThresholdSet carry the --threshold override. As on the
	// command line, an override of 0 is distinct from no override.
	Threshold    float64
	ThresholdSet bool
}

// Load resolves the effective configuration.
//
// Precedence is command line, then file, then DefaultThreshold. The file is
// validated before the override is applied, so an override cannot hide a
// broken configuration: a repository whose configuration is wrong should be
// reported, not quietly evaluated against the value someone passed today.
func Load(params Params) (Config, error) {
	path, err := resolve(params.RepoRoot, params.Cwd, params.Path)
	if err != nil {
		return Config{}, err
	}

	config := Config{Threshold: DefaultThreshold}
	if path != "" {
		data, err := read(path)
		if err != nil {
			return Config{}, err
		}
		parsed, err := parse(data)
		if err != nil {
			return Config{}, fmt.Errorf("%s: %w", path, err)
		}
		if parsed.threshold != nil {
			config.Threshold = *parsed.threshold
		}
		config.Context = parsed.context
	}

	if params.ThresholdSet {
		if err := validateThreshold(params.Threshold); err != nil {
			return Config{}, fmt.Errorf("--threshold: %w", err)
		}
		config.Threshold = params.Threshold
	}

	return config, nil
}

// resolve returns the configuration file to read, or an empty path when there
// is none and the defaults apply.
func resolve(repoRoot, cwd, path string) (string, error) {
	if path != "" {
		if !filepath.IsAbs(path) {
			if cwd == "" {
				return "", fmt.Errorf("configuration file %s: a working directory is required to resolve a relative path", path)
			}
			path = filepath.Join(cwd, path)
		}
		if _, err := os.Stat(path); err != nil {
			return "", pathError(path, err)
		}
		return path, nil
	}

	if repoRoot == "" {
		return "", errors.New("configuration discovery requires a repository root")
	}

	path = filepath.Join(repoRoot, FileName)
	switch _, err := os.Stat(path); {
	case err == nil:
		return path, nil
	case errors.Is(err, fs.ErrNotExist):
		// Only a missing discovered file falls back to the defaults. A
		// permission or I/O error means the repository may well have a
		// configuration that could not be read, which is not the same as
		// having none.
		return "", nil
	default:
		return "", pathError(path, err)
	}
}

// pathError reports a file system failure with the path this package used.
// The wrapped *fs.PathError repeats the path and names the syscall, neither of
// which helps someone reading a command's diagnostics.
func pathError(path string, err error) error {
	var perr *fs.PathError
	if errors.As(err, &perr) {
		err = perr.Err
	}
	return fmt.Errorf("configuration file %s: %w", path, err)
}

// read returns the contents of the configuration file.
func read(path string) ([]byte, error) {
	// The path is the configuration file the caller asked for, so opening a
	// variable path is the purpose of this function rather than a risk.
	file, err := os.Open(path) //nolint:gosec // The path is a deliberate caller input.
	if err != nil {
		return nil, pathError(path, err)
	}
	defer file.Close()

	// Reading one byte past the limit turns an oversized file into an error
	// instead of a silently truncated configuration.
	data, err := io.ReadAll(io.LimitReader(file, maxFileSize+1))
	if err != nil {
		return nil, pathError(path, err)
	}
	if len(data) > maxFileSize {
		return nil, fmt.Errorf("configuration file %s is larger than %d bytes", path, maxFileSize)
	}
	return data, nil
}

// document is the YAML shape of the file. A missing threshold is a nil
// pointer rather than 0, because "threshold: 0" is a meaningful setting that
// allows every change and must not be confused with an omitted key.
type document struct {
	threshold *float64
	context   string
}

// parse reads the file strictly. The YAML is walked as nodes rather than
// unmarshalled into a struct so that unknown keys, duplicate keys, explicit
// nulls, and type mismatches are all refused with the line that caused them.
func parse(data []byte) (document, error) {
	decoder := yaml.NewDecoder(bytes.NewReader(data))

	var node yaml.Node
	switch err := decoder.Decode(&node); {
	case errors.Is(err, io.EOF):
		return document{}, nil // An empty file means "use the defaults".
	case err != nil:
		return document{}, err
	}

	// A second document would make it ambiguous which one configures the run.
	var extra yaml.Node
	switch err := decoder.Decode(&extra); {
	case errors.Is(err, io.EOF):
	case err != nil:
		return document{}, err
	default:
		return document{}, fmt.Errorf("line %d: the file contains more than one YAML document", extra.Line)
	}

	root := &node
	if root.Kind == yaml.DocumentNode {
		if len(root.Content) == 0 {
			return document{}, nil
		}
		root = root.Content[0]
	}
	if root.ShortTag() == nullTag {
		return document{}, nil // Comments or an explicit empty document.
	}
	if root.Kind != yaml.MappingNode {
		return document{}, fmt.Errorf("line %d: the file must be a mapping of %q and %q, got %s", root.Line, keyThreshold, keyContext, describe(root))
	}

	var parsed document
	seen := make(map[string]int, len(root.Content)/2)
	for i := 0; i+1 < len(root.Content); i += 2 {
		key, value := root.Content[i], root.Content[i+1]

		if key.ShortTag() != strTag {
			return document{}, fmt.Errorf("line %d: keys must be strings, got %s", key.Line, describe(key))
		}
		if line, duplicate := seen[key.Value]; duplicate {
			return document{}, fmt.Errorf("line %d: duplicate key %q, first defined on line %d", key.Line, key.Value, line)
		}
		seen[key.Value] = key.Line

		// An explicit null reads like "unset", but a configuration file that
		// sets a key is trying to say something; treating it as a default
		// would silently ignore the intent.
		if value.ShortTag() == nullTag {
			return document{}, fmt.Errorf("line %d: %q has no value; remove the key to use the default", key.Line, key.Value)
		}

		switch key.Value {
		case keyThreshold:
			threshold, err := decodeThreshold(value)
			if err != nil {
				return document{}, err
			}
			parsed.threshold = &threshold
		case keyContext:
			context, err := decodeContext(value)
			if err != nil {
				return document{}, err
			}
			parsed.context = context
		default:
			return document{}, fmt.Errorf("line %d: unknown key %q; only %q and %q are supported", key.Line, key.Value, keyThreshold, keyContext)
		}
	}

	return parsed, nil
}

func decodeThreshold(node *yaml.Node) (float64, error) {
	if tag := node.ShortTag(); tag != intTag && tag != floatTag {
		return 0, fmt.Errorf("line %d: %q must be a number, got %s", node.Line, keyThreshold, describe(node))
	}

	var threshold float64
	if err := node.Decode(&threshold); err != nil {
		return 0, fmt.Errorf("line %d: %q: %w", node.Line, keyThreshold, err)
	}
	if err := validateThreshold(threshold); err != nil {
		return 0, fmt.Errorf("line %d: %q: %w", node.Line, keyThreshold, err)
	}
	return threshold, nil
}

func decodeContext(node *yaml.Node) (string, error) {
	if node.ShortTag() != strTag {
		return "", fmt.Errorf("line %d: %q must be a string, got %s", node.Line, keyContext, describe(node))
	}

	var context string
	if err := node.Decode(&context); err != nil {
		return "", fmt.Errorf("line %d: %q: %w", node.Line, keyContext, err)
	}
	return context, nil
}

// validateThreshold reports whether a threshold is a usable probability. A
// value outside [0,1] is never what the author meant: above 1 nothing is ever
// allowed and a negative value allows everything.
func validateThreshold(threshold float64) error {
	switch {
	case math.IsNaN(threshold), math.IsInf(threshold, 0):
		return fmt.Errorf("%v is not a finite number", threshold)
	case threshold < 0 || threshold > 1:
		return fmt.Errorf("%v is out of range; it must be between 0 and 1", threshold)
	}
	return nil
}

// YAML tags of the values this package accepts or reports on.
const (
	nullTag  = "!!null"
	boolTag  = "!!bool"
	strTag   = "!!str"
	intTag   = "!!int"
	floatTag = "!!float"
)

// describe names a node the way the configuration file reads, so that errors
// talk about YAML rather than about tags.
func describe(node *yaml.Node) string {
	switch node.Kind {
	case yaml.MappingNode:
		return "a mapping"
	case yaml.SequenceNode:
		return "a list"
	default:
		// Every other kind carries a scalar value, which the tag names below.
	}

	switch node.ShortTag() {
	case nullTag:
		return "no value"
	case boolTag:
		return "a boolean"
	case intTag, floatTag:
		return "a number"
	case strTag:
		return "a string"
	default:
		return node.ShortTag()
	}
}
