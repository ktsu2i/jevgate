package cli

import (
	"errors"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"

	"github.com/spf13/pflag"
)

const (
	FormatText = "text"
	FormatJSON = "json"
)

// Options is validated command-line input.
type Options struct {
	// Base and Head are the revisions to compare.
	Base string
	Head string

	// ConfigPath is the optional --config value.
	ConfigPath string

	// Threshold and ThresholdSet preserve an explicit zero override.
	Threshold    float64
	ThresholdSet bool

	// Format selects text or JSON output.
	Format string

	// Help and Version select informational output.
	Help    bool
	Version bool
}

// ParseOptions parses and validates arguments excluding the program name.
func ParseOptions(args []string) (Options, error) {
	flags := pflag.NewFlagSet("jevgate", pflag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.Usage = func() {}

	base := flags.String("base", "", "base revision")
	head := flags.String("head", "", "head revision")
	configPath := flags.String("config", "", "configuration file")
	threshold := flags.String("threshold", "", "probability required to allow AI approval")
	format := flags.String("format", FormatText, "output format")
	help := flags.BoolP("help", "h", false, "print help and exit")
	version := flags.Bool("version", false, "print the version and exit")

	if err := flags.Parse(args); err != nil {
		return Options{}, err
	}

	switch {
	case *help:
		return Options{Help: true}, nil
	case *version:
		return Options{Version: true}, nil
	}

	opts := Options{ConfigPath: *configPath, Format: *format}

	var err error
	if opts.Base, opts.Head, err = revisions(flags, *base, *head); err != nil {
		return Options{}, err
	}

	switch opts.Format {
	case FormatText, FormatJSON:
	default:
		return Options{}, fmt.Errorf("--format %q is not supported; use %q or %q", opts.Format, FormatText, FormatJSON)
	}

	if flags.Changed("threshold") {
		if opts.Threshold, err = parseThreshold(*threshold); err != nil {
			return Options{}, err
		}
		opts.ThresholdSet = true
	}

	return opts, nil
}

func revisions(flags *pflag.FlagSet, baseFlag, headFlag string) (base, head string, err error) {
	positional := flags.Args()
	byFlag := flags.Changed("base") || flags.Changed("head")

	if byFlag {
		if len(positional) > 0 {
			return "", "", fmt.Errorf("positional revisions %q cannot be combined with --base or --head; use one form, not both", positional)
		}
		if !flags.Changed("base") {
			return "", "", errors.New("--head was given without --base; both revisions are required")
		}
		if !flags.Changed("head") {
			return "", "", errors.New("--base was given without --head; both revisions are required")
		}

		base, head = baseFlag, headFlag
		if err := validateRevision("--base", base); err != nil {
			return "", "", err
		}
		if err := validateRevision("--head", head); err != nil {
			return "", "", err
		}
		return base, head, nil
	}

	// Validate first so a range expression gets the actionable error.
	for _, revision := range positional {
		if err := validateRevision("revision", revision); err != nil {
			return "", "", err
		}
	}
	if len(positional) != 2 {
		return "", "", fmt.Errorf("expected exactly 2 revisions, got %d; use \"jevgate <base> <head>\" or \"jevgate --base <base> --head <head>\"", len(positional))
	}
	return positional[0], positional[1], nil
}

func validateRevision(name, revision string) error {
	switch {
	case revision == "":
		return fmt.Errorf("%s is empty", name)
	case strings.Contains(revision, ".."):
		return fmt.Errorf("%s %q is a revision range expression; pass the base and head revisions separately", name, revision)
	}
	return nil
}

func parseThreshold(value string) (float64, error) {
	threshold, err := strconv.ParseFloat(value, 64)
	switch {
	case errors.Is(err, strconv.ErrRange):
		return 0, fmt.Errorf("--threshold %q is out of range; it must be between 0 and 1", value)
	case err != nil:
		return 0, fmt.Errorf("--threshold %q is not a number", value)
	case math.IsNaN(threshold), math.IsInf(threshold, 0):
		return 0, fmt.Errorf("--threshold %q is not a finite number", value)
	case threshold < 0 || threshold > 1:
		return 0, fmt.Errorf("--threshold %q is out of range; it must be between 0 and 1", value)
	}
	return threshold, nil
}
