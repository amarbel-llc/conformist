package config

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"slices"
	"strconv"
)

// Identity returns a stable, hex-encoded hash of the resolved configuration that
// determines how this tree is formatted and linted: the formatter and linter
// tables — including each tool's command, which for a Nix-module config is a
// /nix/store path that encodes the tool's exact version — plus the global
// excludes and the walk type. Two invocations that would format the tree
// identically produce the same identity; a different config, or the same config
// against a different toolchain (a rotated store path), produces a different one.
//
// It is the basis of the config-identity attestation (conformist#76): after a
// successful format/repair run the identity is recorded against the tree, and a
// later run whose identity differs is flagged as a competing config fighting
// over the same tree — the failure mode that is silent-by-construction today.
//
// Identity intentionally hashes only the resolved config (it never resolves a
// binary on PATH), so it is deterministic, cheap, and — unlike a live toolchain
// stat — cannot fail when a configured tool is missing (cf. conformist#75). It
// omits runtime-only flags (they do not change what "owns" the tree) and the
// niche per-tool behavior booleans (passes-files, the restage tiers, sandbox),
// which do not distinguish competing configs and would only add churn.
func (c *Config) Identity() string {
	h := sha256.New()

	writeField(h, "walk", c.Walk)
	writeList(h, "excludes", c.Excludes)

	for _, name := range sortedKeys(c.FormatterConfigs) {
		f := c.FormatterConfigs[name]
		writeField(h, "formatter", name)
		writeField(h, "command", f.Command)
		writeList(h, "options", f.Options)
		writeList(h, "includes", f.Includes)
		writeList(h, "excludes", f.Excludes)
		writeField(h, "priority", strconv.Itoa(f.Priority))
		writeField(h, "check-command", f.CheckCommand)
		writeList(h, "check-options", f.CheckOptions)
		writeField(h, "working-dir", f.WorkingDir)
	}

	for _, name := range sortedKeys(c.LinterConfigs) {
		l := c.LinterConfigs[name]
		writeField(h, "linter", name)
		writeField(h, "command", l.Command)
		writeList(h, "options", l.Options)
		writeList(h, "includes", l.Includes)
		writeList(h, "excludes", l.Excludes)
		writeField(h, "priority", strconv.Itoa(l.Priority))
		writeField(h, "repair-command", l.RepairCommand)
		writeList(h, "repair-options", l.RepairOptions)
		writeField(h, "working-dir", l.WorkingDir)
	}

	return hex.EncodeToString(h.Sum(nil))
}

// writeField writes a length-prefixed key/value pair so the concatenation is
// unambiguous — a value cannot forge the framing of the next field regardless of
// the bytes it contains.
func writeField(h io.Writer, key, value string) {
	_, _ = fmt.Fprintf(h, "%s=%d:%s\n", key, len(value), value)
}

// writeList writes a length-prefixed list, each element itself length-prefixed,
// preserving order (which is significant — e.g. formatter options).
func writeList(h io.Writer, key string, values []string) {
	_, _ = fmt.Fprintf(h, "%s[%d]\n", key, len(values))
	for _, v := range values {
		_, _ = fmt.Fprintf(h, "%d:%s\n", len(v), v)
	}
}

// sortedKeys returns the keys of m in ascending order, so a map's
// non-deterministic iteration order does not perturb the identity hash.
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}

	slices.Sort(keys)

	return keys
}
