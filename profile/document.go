// Package profile implements the POC v1 resolver for conformist profiles
// (docs/rfcs/0005-conformist-profile-and-cache-delivered-tools.md): a hyphence
// document whose TOML body pins cache-delivered artifacts and carries linter
// stanzas that use them.
//
// POC v1 scope (RFC 0005 §7): a single, local, hand-written profile. No layer
// walk, no delegation, no signatures, and only the `static` artifact form. Every
// construct outside that scope is REJECTED with an error naming it, never
// silently ignored — a profile that half-loads would run a weaker lint than its
// author wrote.
package profile

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/BurntSushi/toml"
)

// TypeTag is the hyphence `!` type a profile MUST carry (RFC 0005 §1).
const TypeTag = "toml-conformist_profile-v1"

// FormStatic is the only artifact form POC v1 supports (RFC 0005 §2, §7).
const FormStatic = "static"

var (
	ErrNotHyphence       = errors.New("profile is not a hyphence document")
	ErrUnknownType       = errors.New("unrecognized profile type tag")
	ErrUnsupportedInPOC  = errors.New("not supported by the POC v1 resolver (RFC 0005 §7)")
	ErrUnknownField      = errors.New("unknown profile field")
	ErrInvalidArtifact   = errors.New("invalid artifact stanza")
	ErrInvalidLinter     = errors.New("invalid linter stanza")
	ErrRuleCarriedTwice  = errors.New("rule carried both inline and as an artifact (RFC 0005 §4.4)")
	ErrUnknownRuleTool   = errors.New("unsupported rule-tool")
	ErrUnknownArtifactID = errors.New("reference to an undeclared artifact")

	ErrInvalidPrelude      = errors.New("invalid prelude stanza")
	ErrUnknownPrelude      = errors.New("reference to an undeclared prelude")
	ErrPreludeToolMismatch = errors.New("prelude is written for a different rule-tool")
)

// nameRegex matches config.FromViper's tool-name rule, so a profile cannot
// inject a linter name conformist.toml itself would refuse.
var nameRegex = regexp.MustCompile("^[a-zA-Z0-9_-]+$")

// Artifact is one `[artifact.<name>]` table (RFC 0005 §2).
type Artifact struct {
	Form  string `toml:"form"`
	URL   string `toml:"url"`
	Markl string `toml:"markl"`
	// Executable defaults to true; a data artifact (§2.2) sets it false.
	Executable *bool `toml:"executable"`
}

// IsExecutable reports whether the artifact is materialized executable and put
// on PATH, as opposed to a data file.
func (a Artifact) IsExecutable() bool { return a.Executable == nil || *a.Executable }

// Linter is one `[linter.<name>]` table: the RFC 0001 fields a POC stanza
// needs, plus the rule carriers of RFC 0005 §4.4.
type Linter struct {
	Command       string   `toml:"command"`
	Options       []string `toml:"options"`
	Includes      []string `toml:"includes"`
	Excludes      []string `toml:"excludes"`
	Priority      int      `toml:"priority"`
	PassesFiles   *bool    `toml:"passes-files"`
	RepairCommand string   `toml:"repair-command"`
	RepairOptions []string `toml:"repair-options"`
	WorkingDir    string   `toml:"working-dir"`

	// Rule is the inline rule program; RuleArtifact names a data artifact
	// holding it instead. At most one may be set.
	Rule         string `toml:"rule"`
	RuleArtifact string `toml:"rule-artifact"`
	// RuleTool runs the rule over Command's standard output.
	RuleTool string `toml:"rule-tool"`
	// Preludes name `[prelude.<name>]` stanzas joined, in this order, in front
	// of the rule before it runs.
	Preludes []string `toml:"preludes"`
}

// HasRule reports whether the stanza carries a rule program either way.
func (l Linter) HasRule() bool { return l.Rule != "" || l.RuleArtifact != "" }

// Prelude is one `[prelude.<name>]` table: definitions shared by every rule that
// lists it, written once instead of copied into each rule. Like a rule it is
// carried inline or as a data artifact, never both.
type Prelude struct {
	RuleTool string `toml:"rule-tool"`
	Rule     string `toml:"rule"`
	Artifact string `toml:"artifact"`
}

// Document is a parsed profile.
type Document struct {
	// Path is where the profile was read from, for diagnostics.
	Path string
	// Description is the hyphence `#` lines, space-joined.
	Description string
	Artifacts   map[string]Artifact
	Preludes    map[string]Prelude
	Linters     map[string]Linter
}

type body struct {
	Artifact map[string]Artifact `toml:"artifact"`
	Prelude  map[string]Prelude  `toml:"prelude"`
	Linter   map[string]Linter   `toml:"linter"`
}

// Parse decodes and structurally validates a profile. It does not look at
// artifact pins beyond requiring them: pins are checked by Resolve, before any
// fetch, so a profile whose pins are still pending remains parseable.
func Parse(path string, data []byte) (*Document, error) {
	doc := &Document{Path: path}

	tomlBody, err := parseHyphence(doc, data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}

	var decoded body

	meta, err := toml.Decode(tomlBody, &decoded)
	if err != nil {
		return nil, fmt.Errorf("%s: decoding TOML body: %w", path, err)
	}

	if undecoded := meta.Undecoded(); len(undecoded) > 0 {
		keys := make([]string, 0, len(undecoded))
		for _, k := range undecoded {
			keys = append(keys, k.String())
		}

		return nil, fmt.Errorf(
			"%s: %w: %s (formatter stanzas and any field not listed in RFC 0005 §2/§4 are %w)",
			path, ErrUnknownField, strings.Join(keys, ", "), ErrUnsupportedInPOC,
		)
	}

	doc.Artifacts = decoded.Artifact
	doc.Preludes = decoded.Prelude
	doc.Linters = decoded.Linter

	if err := doc.validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}

	return doc, nil
}

// parseHyphence reads the metadata section into doc and returns the body.
// See hyphence(7): a `---` boundary, prefixed metadata lines, a closing `---`,
// then exactly one blank line before any body.
func parseHyphence(doc *Document, data []byte) (string, error) {
	lines := strings.Split(string(data), "\n")

	if lines[0] != "---" {
		return "", fmt.Errorf("%w: first line must be the `---` boundary", ErrNotHyphence)
	}

	closing := slices.Index(lines[1:], "---")
	if closing < 0 {
		return "", fmt.Errorf("%w: metadata section has no closing `---` boundary", ErrNotHyphence)
	}

	closing++

	var (
		descriptions []string
		typeLines    int
	)

	for i, line := range lines[1:closing] {
		lineNo := i + 2

		if len(line) < 2 || line[1] != ' ' {
			return "", fmt.Errorf("%w: line %d: expected `<prefix> <content>`", ErrNotHyphence, lineNo)
		}

		content := line[2:]

		switch line[0] {
		case '#':
			descriptions = append(descriptions, content)
		case '%':
			// comment: opaque
		case '!':
			typeLines++

			tag, lock, _ := strings.Cut(content, " ")
			if tag != TypeTag {
				return "", fmt.Errorf("%w: found %q, want %q", ErrUnknownType, tag, TypeTag)
			}

			if lock != "" {
				return "", fmt.Errorf("line %d: a type-line lock is %w", lineNo, ErrUnsupportedInPOC)
			}
		case '-', '<':
			return "", fmt.Errorf(
				"line %d: tag/field lines (delegation, RFC 0005 §3.1) are %w", lineNo, ErrUnsupportedInPOC,
			)
		case '@':
			return "", fmt.Errorf("line %d: a blob-reference body is %w", lineNo, ErrUnsupportedInPOC)
		default:
			return "", fmt.Errorf("%w: line %d: unknown metadata prefix %q", ErrNotHyphence, lineNo, line[0])
		}
	}

	if typeLines != 1 {
		return "", fmt.Errorf("%w: want exactly one `! %s` line, found %d", ErrNotHyphence, TypeTag, typeLines)
	}

	doc.Description = strings.Join(descriptions, " ")

	rest := lines[closing+1:]
	if len(rest) == 0 || (len(rest) == 1 && rest[0] == "") {
		return "", nil
	}

	if rest[0] != "" {
		return "", fmt.Errorf("%w: a blank line must separate the closing `---` from the body", ErrNotHyphence)
	}

	return strings.Join(rest[1:], "\n"), nil
}

func (d *Document) validate() error {
	for _, name := range sortedKeys(d.Artifacts) {
		a := d.Artifacts[name]

		switch {
		case !nameRegex.MatchString(name):
			return fmt.Errorf("%w %q: name must match %s", ErrInvalidArtifact, name, nameRegex)
		case a.Form == "":
			return fmt.Errorf("%w %q: missing `form`", ErrInvalidArtifact, name)
		case a.Form != FormStatic:
			return fmt.Errorf("artifact %q: form %q is %w", name, a.Form, ErrUnsupportedInPOC)
		case a.URL == "", a.Markl == "":
			return fmt.Errorf("%w %q: a static artifact requires `url` and `markl`", ErrInvalidArtifact, name)
		}
	}

	for _, name := range sortedKeys(d.Preludes) {
		if err := d.validatePrelude(name, d.Preludes[name]); err != nil {
			return err
		}
	}

	for _, name := range sortedKeys(d.Linters) {
		if err := d.validateLinter(name, d.Linters[name]); err != nil {
			return err
		}
	}

	return nil
}

func (d *Document) validatePrelude(name string, p Prelude) error {
	switch {
	case !nameRegex.MatchString(name):
		return fmt.Errorf("%w %q: name must match %s", ErrInvalidPrelude, name, nameRegex)
	case p.Rule != "" && p.Artifact != "":
		return fmt.Errorf("prelude %q: %w", name, ErrRuleCarriedTwice)
	case p.Rule == "" && p.Artifact == "":
		return fmt.Errorf("%w %q: needs `rule` or `artifact`", ErrInvalidPrelude, name)
	}

	if _, ok := ruleToolArgs[p.RuleTool]; !ok {
		return fmt.Errorf("prelude %q: %w %q (supported: %s)",
			name, ErrUnknownRuleTool, p.RuleTool, strings.Join(sortedKeys(ruleToolArgs), ", "))
	}

	if p.Artifact != "" {
		return d.requireDataArtifact(ErrInvalidPrelude, "prelude "+name, p.Artifact)
	}

	return nil
}

// requireDataArtifact checks that ref names a declared, non-executable artifact,
// wrapping invalid (the owner's own invalid-stanza error) when it is executable.
func (d *Document) requireDataArtifact(invalid error, owner, ref string) error {
	a, ok := d.Artifacts[ref]
	if !ok {
		return fmt.Errorf("%s: %w %q", owner, ErrUnknownArtifactID, ref)
	}

	if a.IsExecutable() {
		return fmt.Errorf("%w: %s: artifact %q must be a data artifact (`executable = false`)", invalid, owner, ref)
	}

	return nil
}

func (d *Document) validateLinter(name string, l Linter) error {
	switch {
	case !nameRegex.MatchString(name):
		return fmt.Errorf("%w %q: name must match %s", ErrInvalidLinter, name, nameRegex)
	case l.Command == "":
		return fmt.Errorf("%w %q: missing `command`", ErrInvalidLinter, name)
	case len(l.Includes) == 0:
		return fmt.Errorf("%w %q: missing `includes`", ErrInvalidLinter, name)
	}

	if !l.HasRule() {
		switch {
		case l.RuleTool != "":
			return fmt.Errorf("%w %q: `rule-tool` without a rule", ErrInvalidLinter, name)
		case len(l.Preludes) > 0:
			return fmt.Errorf("%w %q: `preludes` without a rule", ErrInvalidLinter, name)
		}

		return nil
	}

	if l.Rule != "" && l.RuleArtifact != "" {
		return fmt.Errorf("linter %q: %w", name, ErrRuleCarriedTwice)
	}

	if _, ok := ruleToolArgs[l.RuleTool]; !ok {
		return fmt.Errorf("linter %q: %w %q (supported: %s)",
			name, ErrUnknownRuleTool, l.RuleTool, strings.Join(sortedKeys(ruleToolArgs), ", "))
	}

	// The rule file is the pipeline's only argument, so a rule stanza can take
	// neither file arguments nor extra options.
	if l.PassesFiles == nil || *l.PassesFiles {
		return fmt.Errorf("%w %q: a rule stanza must set `passes-files = false`", ErrInvalidLinter, name)
	}

	if len(l.Options) > 0 {
		return fmt.Errorf("%w %q: `options` cannot be combined with a rule", ErrInvalidLinter, name)
	}

	seen := map[string]bool{}

	for _, ref := range l.Preludes {
		p, ok := d.Preludes[ref]

		switch {
		case !ok:
			return fmt.Errorf("linter %q: %w %q", name, ErrUnknownPrelude, ref)
		case seen[ref]:
			return fmt.Errorf("%w %q: prelude %q listed twice", ErrInvalidLinter, name, ref)
		case p.RuleTool != l.RuleTool:
			return fmt.Errorf("linter %q: %w: %q is for %q, the rule is %q",
				name, ErrPreludeToolMismatch, ref, p.RuleTool, l.RuleTool)
		}

		seen[ref] = true
	}

	if l.RuleArtifact != "" {
		return d.requireDataArtifact(ErrInvalidLinter, "linter "+name, l.RuleArtifact)
	}

	return nil
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}

	slices.Sort(keys)

	return keys
}
