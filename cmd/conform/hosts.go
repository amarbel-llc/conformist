package conform

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"code.linenisgreat.com/conformist/git"
)

// canonicalGitHost is the git-remotes linter's default canonical-host: an origin
// anywhere else fails the impure lane unless that host is allowlisted.
const canonicalGitHost = "code.linenisgreat.com"

// impureOverlay is the file conform persists the git-remotes allowlist in: the
// impure lane's overlay, the only place the option has any effect (conformist.nix
// is imported into the pure eval only, and git-remotes lives in the impure one).
const impureOverlay = "conformist-impure.nix"

// allowedHostsMarker ends the one line conform owns in the overlay. Everything
// else in the file is the user's; conform only ever rewrites this line.
const allowedHostsMarker = "# managed by `conformist conform` (--allow-host)"

// allowedHostsLine matches the managed line, capturing its host list.
var allowedHostsLine = regexp.MustCompile(
	`^(\s*)linters\.git-remotes\.allowed-hosts = \[(.*)\]; ` + regexp.QuoteMeta(allowedHostsMarker) + `\s*$`)

var quotedHost = regexp.MustCompile(`"([^"]+)"`)

// hostsOutcome reports what persistAllowedHosts did.
type hostsOutcome struct {
	// Created is true when the overlay did not exist and was written.
	Created bool
	// Changed is true when an existing overlay's managed line was added or updated.
	Changed bool
	// Manual is non-empty when conform could not safely edit and the operator
	// must apply the line themselves; it says why.
	Manual string
}

// wantedHosts is the allowlist conform should persist: the hosts named with
// --allow-host plus, automatically, the origin remote's host when it is not the
// canonical one. Order: explicit flags first, then the origin host; duplicates
// and the canonical host are dropped.
func wantedHosts(dir string, flagHosts []string) ([]string, error) {
	var hosts []string

	seen := map[string]bool{canonicalGitHost: true}
	add := func(host string) {
		host = strings.ToLower(strings.TrimSpace(host))
		if host == "" || seen[host] {
			return
		}

		seen[host] = true
		hosts = append(hosts, host)
	}

	for _, host := range flagHosts {
		add(host)
	}

	origin, err := git.OriginHost(dir)
	if err != nil {
		return nil, fmt.Errorf("reading origin host: %w", err)
	}

	add(origin)

	return hosts, nil
}

// managedLine renders the owned line for hosts at the given indent.
func managedLine(indent string, hosts []string) string {
	quoted := make([]string, len(hosts))
	for i, host := range hosts {
		quoted[i] = `"` + host + `"`
	}

	return indent + "linters.git-remotes.allowed-hosts = [ " + strings.Join(quoted, " ") + " ]; " + allowedHostsMarker
}

// persistAllowedHosts makes hosts part of dir's conformist-impure.nix so every
// later run (conform --repair, `just lint`) keeps them. It is idempotent and only
// ever adds hosts: an existing managed line is widened, never narrowed, so a
// re-run without the flag cannot drop a host a previous run persisted.
//
// A hand-written allowed-hosts, or an overlay whose shape it cannot place a line
// into, is reported via Manual and left unedited.
func persistAllowedHosts(dir string, hosts []string) (hostsOutcome, error) {
	var out hostsOutcome

	if len(hosts) == 0 {
		return out, nil
	}

	path := filepath.Join(dir, impureOverlay)

	content, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		content = impureNix
		out.Created = true
	} else if err != nil {
		return out, fmt.Errorf("reading %s: %w", impureOverlay, err)
	}

	updated, manual := withAllowedHosts(string(content), hosts)
	if manual != "" {
		out.Created = false
		out.Manual = manual

		return out, nil
	}

	if updated == string(content) && !out.Created {
		return out, nil
	}

	if err := os.WriteFile(path, []byte(updated), 0o644); err != nil { //nolint:gosec // overlay, not a secret
		return out, fmt.Errorf("writing %s: %w", impureOverlay, err)
	}

	out.Changed = !out.Created

	return out, nil
}

// withAllowedHosts returns content with the managed allowed-hosts line holding
// the union of its current hosts and hosts. A non-empty second result means the
// content cannot be edited safely (and says why).
func withAllowedHosts(content string, hosts []string) (string, string) {
	lines := strings.Split(content, "\n")

	for i, line := range lines {
		match := allowedHostsLine.FindStringSubmatch(line)
		if match == nil {
			continue
		}

		var existing []string
		for _, quoted := range quotedHost.FindAllStringSubmatch(match[2], -1) {
			existing = append(existing, quoted[1])
		}

		lines[i] = managedLine(match[1], union(existing, hosts))

		return strings.Join(lines, "\n"), ""
	}

	// Only code counts: the stub's own comments mention the option.
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "#") && strings.Contains(trimmed, "allowed-hosts") {
			return content, "it already sets allowed-hosts by hand, so conform will not edit it"
		}
	}

	// Insert before the last line that is exactly "}": the close of the module's
	// attribute set in every shape the templates ship (plain `{ ... }: { … }` and
	// `let … in { … }`).
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.TrimRight(lines[i], " \t") != "}" {
			continue
		}

		inserted := append([]string{}, lines[:i]...)
		inserted = append(inserted, managedLine("  ", hosts))
		inserted = append(inserted, lines[i:]...)

		return strings.Join(inserted, "\n"), ""
	}

	return content, "it has no closing } at the start of a line to insert before"
}

// union appends to base each of extra not already in base, keeping order.
func union(base, extra []string) []string {
	seen := map[string]bool{}
	for _, host := range base {
		seen[host] = true
	}

	merged := append([]string{}, base...)

	for _, host := range extra {
		if !seen[host] {
			seen[host] = true
			merged = append(merged, host)
		}
	}

	return merged
}

// persistHosts is local-mode conform's allowed-hosts step: compute the wanted
// hosts, persist them, and record/report what happened.
func persistHosts(dir string, out io.Writer, flagHosts []string, res *Result) error {
	hosts, err := wantedHosts(dir, flagHosts)
	if err != nil {
		return err
	}

	if len(hosts) == 0 {
		return nil
	}

	outcome, err := persistAllowedHosts(dir, hosts)
	if err != nil {
		return err
	}

	alreadyWritten := false

	for _, name := range res.Wrote {
		if name == impureOverlay {
			alreadyWritten = true
		}
	}

	switch {
	case outcome.Created && !alreadyWritten:
		res.Wrote = append(res.Wrote, impureOverlay)
	case outcome.Changed && !alreadyWritten:
		res.Edited = append(res.Edited, impureOverlay+" (allowed-hosts)")
	}

	reportHosts(out, hosts, outcome, flakeImportsOverlay(dir))

	return nil
}

// reportHosts tells the operator which hosts are allowlisted and what, if
// anything, they must still do by hand.
func reportHosts(out io.Writer, hosts []string, outcome hostsOutcome, importsOverlay bool) {
	if outcome.Manual == "" {
		fmt.Fprintf(out, "allowed git-remotes host(s): %s (persisted in %s)\n", strings.Join(hosts, ", "), impureOverlay)
	}

	if advice := allowedHostsAdvice(hosts, outcome, importsOverlay); advice != "" {
		fmt.Fprintf(out, "note: %s\n", advice)
	}
}

// flakeImportsOverlay reports whether dir's flake.nix imports the impure
// overlay; without that import the persisted allowlist has no effect.
func flakeImportsOverlay(dir string) bool {
	content, err := os.ReadFile(filepath.Join(dir, "flake.nix"))
	if err != nil {
		return false
	}

	return strings.Contains(string(content), "./"+impureOverlay)
}

// allowedHostsAdvice is what to tell the operator when the flake does not import
// the overlay or the overlay could not be edited.
func allowedHostsAdvice(hosts []string, outcome hostsOutcome, importsOverlay bool) string {
	var parts []string

	if outcome.Manual != "" {
		parts = append(parts, fmt.Sprintf(
			"%s: %s; add this line to its module body yourself:\n  %s",
			impureOverlay, outcome.Manual, managedLine("", hosts)))
	}

	if !importsOverlay {
		parts = append(parts, fmt.Sprintf(
			"flake.nix does not import ./%s in impureEval, so the allowed host(s) have no effect yet; "+
				"add it to impureEval's imports (next to conformist.lib.presets.eng-impure)", impureOverlay))
	}

	return strings.Join(parts, "\n")
}
