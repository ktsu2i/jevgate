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

// DefaultThreshold is the default probability required for AI approval.
const DefaultThreshold = 0.95

// FileName is the discovered configuration filename.
const FileName = ".jevgate.yml"

const (
	keyThreshold = "threshold"
	keyContext   = "context"
)

const maxFileSize = 1 << 20

// Config is the effective configuration.
type Config struct {
	// Threshold is the minimum probability required for AI approval.
	Threshold float64

	// Context is repository context supplied to Jev.
	Context string
}

// Params controls configuration discovery and overrides.
type Params struct {
	// RepoRoot is searched for FileName.
	RepoRoot string

	// Cwd resolves a relative Path.
	Cwd string

	// Path is the optional explicit configuration path.
	Path string

	// Threshold and ThresholdSet preserve an explicit zero override.
	Threshold    float64
	ThresholdSet bool
}

// Load returns the validated effective configuration.
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
		return "", nil
	default:
		return "", pathError(path, err)
	}
}

func pathError(path string, err error) error {
	var perr *fs.PathError
	if errors.As(err, &perr) {
		err = perr.Err
	}
	return fmt.Errorf("configuration file %s: %w", path, err)
}

func read(path string) ([]byte, error) {
	file, err := os.Open(path) //nolint:gosec // The path is a deliberate caller input.
	if err != nil {
		return nil, pathError(path, err)
	}
	defer file.Close()

	data, err := io.ReadAll(io.LimitReader(file, maxFileSize+1))
	if err != nil {
		return nil, pathError(path, err)
	}
	if len(data) > maxFileSize {
		return nil, fmt.Errorf("configuration file %s is larger than %d bytes", path, maxFileSize)
	}
	return data, nil
}

type document struct {
	threshold *float64
	context   string
}

func parse(data []byte) (document, error) {
	decoder := yaml.NewDecoder(bytes.NewReader(data))

	var node yaml.Node
	switch err := decoder.Decode(&node); {
	case errors.Is(err, io.EOF):
		return document{}, nil
	case err != nil:
		return document{}, err
	}

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
		return document{}, nil
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

		// Reject null so an explicit key cannot silently become a default.
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

func validateThreshold(threshold float64) error {
	switch {
	case math.IsNaN(threshold), math.IsInf(threshold, 0):
		return fmt.Errorf("%v is not a finite number", threshold)
	case threshold < 0 || threshold > 1:
		return fmt.Errorf("%v is out of range; it must be between 0 and 1", threshold)
	}
	return nil
}

const (
	nullTag  = "!!null"
	boolTag  = "!!bool"
	strTag   = "!!str"
	intTag   = "!!int"
	floatTag = "!!float"
)

func describe(node *yaml.Node) string {
	switch node.Kind {
	case yaml.MappingNode:
		return "a mapping"
	case yaml.SequenceNode:
		return "a list"
	default:
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
