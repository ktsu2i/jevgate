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

// Output formats accepted by --format.
const (
	FormatText = "text"
	FormatJSON = "json"
)

// Options is the normalized command line input. It is the only description of
// the run that later stages receive: parsing happens once, before any Git or
// API work, so that invalid input can never reach a decision.
type Options struct {
	// Base and Head are the revisions to compare. They are non-empty and free
	// of range expressions, but they are not resolved here: whether Git can
	// resolve them is decided when the diff is collected.
	Base string
	Head string

	// ConfigPath is the --config value, empty when the flag was omitted. A
	// relative path is resolved against the working directory by the
	// configuration loader, not here.
	ConfigPath string

	// Threshold is the --threshold override and ThresholdSet reports whether
	// the flag was given. The two are kept apart because 0 is a valid
	// threshold and must stay distinguishable from an omitted flag.
	Threshold    float64
	ThresholdSet bool

	// Format is FormatText or FormatJSON.
	Format string

	// Help and Version report that the run only prints information. The
	// remaining fields are then zero: both work without revisions, a Git
	// repository, or an API key, so nothing else is validated.
	Help    bool
	Version bool
}

// ParseOptions parses and validates args, which excludes the program name.
//
// Options may appear before or after the positional revisions, and everything
// after a "--" terminator is a revision. Parsing rejects input rather than
// repairing it: an unusable command line is an error, never a negative
// decision.
func ParseOptions(args []string) (Options, error) {
	flags := pflag.NewFlagSet("jevgate", pflag.ContinueOnError)
	// Usage is printed by Run from its own help text, so the flag set must
	// stay silent and report errors only through the return value.
	flags.SetOutput(io.Discard)
	flags.Usage = func() {}

	base := flags.String("base", "", "base revision")
	head := flags.String("head", "", "head revision")
	configPath := flags.String("config", "", "configuration file")
	// The threshold is kept as a string so that this package, and not pflag,
	// decides what a malformed or out-of-range number looks like.
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

// revisions returns the base and head revisions from whichever form was used.
// The two forms are equivalent but exclusive: mixing them is an error even
// when the values agree, because the command then has two sources for one
// revision and no rule says which one wins.
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

	// Each revision is checked before the count so that a range expression is
	// reported as such: "jevgate main...HEAD" is the common mistake, and
	// "expected two revisions" would not explain it.
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

// validateRevision rejects the revisions this tool cannot compare. Anything
// else is left to Git: branches, tags, and expressions such as HEAD~1 are all
// acceptable revisions and must not be refused by a stricter rule here.
func validateRevision(name, revision string) error {
	switch {
	case revision == "":
		return fmt.Errorf("%s is empty", name)
	case strings.Contains(revision, ".."):
		return fmt.Errorf("%s %q is a revision range expression; pass the base and head revisions separately", name, revision)
	}
	return nil
}

// parseThreshold converts the --threshold value into a probability.
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
