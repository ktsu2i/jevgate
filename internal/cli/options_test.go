package cli

import (
	"strings"
	"testing"
)

func TestParseOptions(t *testing.T) {
	positional := Options{Base: "main", Head: "HEAD", Format: FormatText}

	tests := []struct {
		name string
		args []string
		want Options
	}{
		{
			name: "positional revisions",
			args: []string{"main", "HEAD"},
			want: positional,
		},
		{
			name: "flag revisions are equivalent to positional ones",
			args: []string{"--base", "main", "--head", "HEAD"},
			want: positional,
		},
		{
			name: "flag revisions with an equals sign",
			args: []string{"--base=main", "--head=HEAD"},
			want: positional,
		},
		{
			name: "options after the revisions",
			args: []string{"main", "HEAD", "--threshold", "0.98"},
			want: Options{Base: "main", Head: "HEAD", Format: FormatText, Threshold: 0.98, ThresholdSet: true},
		},
		{
			name: "options before the revisions",
			args: []string{"--threshold", "0.98", "main", "HEAD"},
			want: Options{Base: "main", Head: "HEAD", Format: FormatText, Threshold: 0.98, ThresholdSet: true},
		},
		{
			name: "options around the revisions",
			args: []string{"--format", "json", "main", "HEAD", "--config", "custom.yml"},
			want: Options{Base: "main", Head: "HEAD", Format: FormatJSON, ConfigPath: "custom.yml"},
		},
		{
			name: "threshold with an equals sign",
			args: []string{"main", "HEAD", "--threshold=0.98"},
			want: Options{Base: "main", Head: "HEAD", Format: FormatText, Threshold: 0.98, ThresholdSet: true},
		},
		{
			// 0 allows every change, so it must survive as an explicit
			// override rather than look like an omitted flag.
			name: "threshold of zero is an override",
			args: []string{"main", "HEAD", "--threshold", "0"},
			want: Options{Base: "main", Head: "HEAD", Format: FormatText, Threshold: 0, ThresholdSet: true},
		},
		{
			name: "threshold of one is an override",
			args: []string{"main", "HEAD", "--threshold", "1"},
			want: Options{Base: "main", Head: "HEAD", Format: FormatText, Threshold: 1, ThresholdSet: true},
		},
		{
			name: "text format is the default",
			args: []string{"main", "HEAD", "--format", "text"},
			want: positional,
		},
		{
			name: "revisions after a terminator",
			args: []string{"--format", "json", "--", "main", "HEAD"},
			want: Options{Base: "main", Head: "HEAD", Format: FormatJSON},
		},
		{
			// Git accepts revisions this CLI would otherwise read as flags;
			// "--" is the only way to pass them.
			name: "terminator protects revisions that look like flags",
			args: []string{"--", "--base", "HEAD"},
			want: Options{Base: "--base", Head: "HEAD", Format: FormatText},
		},
		{
			name: "any revision git resolves is accepted",
			args: []string{"HEAD~1", "v1.2.3"},
			want: Options{Base: "HEAD~1", Head: "v1.2.3", Format: FormatText},
		},
		{
			name: "commit hashes are accepted",
			args: []string{"abc123", "def456"},
			want: Options{Base: "abc123", Head: "def456", Format: FormatText},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := ParseOptions(test.args)
			if err != nil {
				t.Fatalf("ParseOptions(%q) returned %v, want %+v", test.args, err, test.want)
			}
			if got != test.want {
				t.Errorf("ParseOptions(%q) = %+v, want %+v", test.args, got, test.want)
			}
		})
	}
}

func TestParseOptionsRejectsInvalidInput(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string // a substring the diagnostic must explain
	}{
		{
			name: "no arguments",
			args: nil,
			want: "exactly 2 revisions",
		},
		{
			name: "one positional revision",
			args: []string{"main"},
			want: "exactly 2 revisions",
		},
		{
			name: "three positional revisions",
			args: []string{"main", "HEAD", "extra"},
			want: "exactly 2 revisions",
		},
		{
			// The forms are exclusive even when they agree, because the
			// command would otherwise have two sources for one revision.
			name: "positional and flag forms mixed",
			args: []string{"main", "HEAD", "--base", "main"},
			want: "cannot be combined",
		},
		{
			name: "one positional revision and one flag",
			args: []string{"--base", "main", "HEAD"},
			want: "cannot be combined",
		},
		{
			name: "base without head",
			args: []string{"--base", "main"},
			want: "--base was given without --head",
		},
		{
			name: "head without base",
			args: []string{"--head", "HEAD"},
			want: "--head was given without --base",
		},
		{
			name: "two dot revision range",
			args: []string{"main..HEAD"},
			want: "revision range expression",
		},
		{
			name: "three dot revision range",
			args: []string{"main...HEAD"},
			want: "revision range expression",
		},
		{
			name: "revision range as one of two revisions",
			args: []string{"main..HEAD", "HEAD"},
			want: "revision range expression",
		},
		{
			name: "revision range in a flag",
			args: []string{"--base", "main...HEAD", "--head", "HEAD"},
			want: "revision range expression",
		},
		{
			name: "empty positional revision",
			args: []string{"", "HEAD"},
			want: "is empty",
		},
		{
			name: "empty head flag",
			args: []string{"--base", "main", "--head="},
			want: "--head is empty",
		},
		{
			name: "empty base flag",
			args: []string{"--base=", "--head", "HEAD"},
			want: "--base is empty",
		},
		{
			name: "unknown flag",
			args: []string{"main", "HEAD", "--safe-paths", "docs"},
			want: "unknown flag",
		},
		{
			name: "unknown shorthand flag",
			args: []string{"main", "HEAD", "-x"},
			want: "unknown shorthand flag",
		},
		{
			name: "single dash long flag",
			args: []string{"-base", "main", "-head", "HEAD"},
			want: "unknown shorthand flag",
		},
		{
			name: "threshold without a value",
			args: []string{"main", "HEAD", "--threshold"},
			want: "needs an argument",
		},
		{
			name: "config without a value",
			args: []string{"main", "HEAD", "--config"},
			want: "needs an argument",
		},
		{
			name: "empty threshold",
			args: []string{"main", "HEAD", "--threshold="},
			want: "is not a number",
		},
		{
			name: "threshold that is not a number",
			args: []string{"main", "HEAD", "--threshold", "high"},
			want: "is not a number",
		},
		{
			name: "threshold below zero",
			args: []string{"main", "HEAD", "--threshold", "-0.1"},
			want: "out of range",
		},
		{
			name: "threshold above one",
			args: []string{"main", "HEAD", "--threshold", "1.1"},
			want: "out of range",
		},
		{
			name: "threshold too large for a float",
			args: []string{"main", "HEAD", "--threshold", "1e400"},
			want: "out of range",
		},
		{
			name: "threshold not a number literal",
			args: []string{"main", "HEAD", "--threshold", "NaN"},
			want: "not a finite number",
		},
		{
			name: "infinite threshold",
			args: []string{"main", "HEAD", "--threshold", "Inf"},
			want: "not a finite number",
		},
		{
			name: "negative infinite threshold",
			args: []string{"main", "HEAD", "--threshold", "-Inf"},
			want: "not a finite number",
		},
		{
			name: "unsupported format",
			args: []string{"main", "HEAD", "--format", "yaml"},
			want: "is not supported",
		},
		{
			name: "empty format",
			args: []string{"main", "HEAD", "--format="},
			want: "is not supported",
		},
		{
			name: "format is case sensitive",
			args: []string{"main", "HEAD", "--format", "JSON"},
			want: "is not supported",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := ParseOptions(test.args)
			if err == nil {
				t.Fatalf("ParseOptions(%q) = %+v, want an error", test.args, got)
			}
			if !strings.Contains(err.Error(), test.want) {
				t.Errorf("ParseOptions(%q) error = %q, want it to mention %q", test.args, err, test.want)
			}
			// Rejected input must not leave a usable run behind: a caller
			// that ignored the error would otherwise evaluate a change the
			// user never asked for.
			if got != (Options{}) {
				t.Errorf("ParseOptions(%q) returned %+v with an error, want the zero Options", test.args, got)
			}
		})
	}
}

func TestParseOptionsHelpAndVersion(t *testing.T) {
	// Help and version print and exit, so they are accepted with or without
	// revisions and are not held to the rest of the validation.
	tests := []struct {
		args []string
		want Options
	}{
		{args: []string{"--help"}, want: Options{Help: true}},
		{args: []string{"-h"}, want: Options{Help: true}},
		{args: []string{"main", "HEAD", "--help"}, want: Options{Help: true}},
		{args: []string{"--help", "--format", "yaml"}, want: Options{Help: true}},
		{args: []string{"--version"}, want: Options{Version: true}},
		{args: []string{"main", "--version"}, want: Options{Version: true}},
		{args: []string{"--help", "--version"}, want: Options{Help: true}},
		{args: []string{"--version", "--help"}, want: Options{Help: true}},
	}

	for _, test := range tests {
		got, err := ParseOptions(test.args)
		if err != nil {
			t.Errorf("ParseOptions(%q) returned %v, want %+v", test.args, err, test.want)
			continue
		}
		if got != test.want {
			t.Errorf("ParseOptions(%q) = %+v, want %+v", test.args, got, test.want)
		}
	}
}

func TestParseOptionsDefaults(t *testing.T) {
	// The parser reports only what was given. Threshold resolution belongs to
	// the configuration loader, which cannot tell an omitted flag from a
	// deliberate 0 unless the parser keeps them apart.
	got, err := ParseOptions([]string{"main", "HEAD"})
	if err != nil {
		t.Fatalf("ParseOptions returned %v", err)
	}
	if got.ThresholdSet {
		t.Errorf("ParseOptions without --threshold set ThresholdSet, want it unset")
	}
	if got.Threshold != 0 {
		t.Errorf("ParseOptions without --threshold set Threshold = %v, want 0", got.Threshold)
	}
	if got.ConfigPath != "" {
		t.Errorf("ParseOptions without --config set ConfigPath = %q, want an empty path", got.ConfigPath)
	}
	if got.Format != FormatText {
		t.Errorf("ParseOptions without --format set Format = %q, want %q", got.Format, FormatText)
	}
}

func TestParseOptionsDoesNotMutateArgs(t *testing.T) {
	// Run passes os.Args[1:]; parsing must not reorder or consume it.
	args := []string{"--threshold", "0.98", "main", "HEAD"}
	want := []string{"--threshold", "0.98", "main", "HEAD"}

	if _, err := ParseOptions(args); err != nil {
		t.Fatalf("ParseOptions returned %v", err)
	}
	for i := range want {
		if args[i] != want[i] {
			t.Fatalf("ParseOptions modified its arguments: %q, want %q", args, want)
		}
	}
}
