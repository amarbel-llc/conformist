// Package codegen implements `conformist codegen-repair` (conformist#124): the
// generic codegen-repair lane that lets a repo restamp its own generated files at
// commit time instead of discovering the drift at the merge gate.
//
// THE ANCHOR IS THE DRIFT CHECK. A generator (tommy, a dagnabit facade export, …)
// already ships a flake check that FAILS when its output is stale. igloo#80 makes
// that check also name where its repair lives: the check derivation carries
// `passthru.codegenPatch`, a pure sibling built from the same tree with the same
// command, whose `$out/patch` is a `git diff --no-index --binary src work` (empty
// when current) applied with `git apply -p2`. So this command needs no new output
// names, no per-repo override, and no knowledge of any particular generator: it
// enumerates `checks.<system>`, keeps every attr carrying that passthru, builds
// each one's patch, and applies it.
//
// TOMMY-AGNOSTIC BY CONSTRUCTION. Nothing here mentions a generator. The only host
// requirements are `nix` (resolved from PATH — deliberately the HOST nix, with its
// store, substituters and eval cache, not a pinned pkgs.nix) and `git`
// (conformist's own [git.Binary], a burned-in store path in a Nix build). That is
// also what makes the standalone static artifact a viable second delivery route
// for the RFC-0005 profile at POC v2 — same code, different delivery.
//
// REPAIR-ONLY. This command is wired as a conformist linter's `repair-command`
// only; that linter's read-only `command` is a no-op (see
// nix/linters/codegen-repair.nix). The repo's own drift check stays the gate, so
// `conformist check` never double-flags what the drift check already reports,
// never pays for a second build, and there is one place to debug.
//
// ONE PATCH PER PASS. Each codegenPatch is computed against its own check's `src`
// alone, so two checks that touch the same file yield patches that cannot both
// apply to the original tree. [session.converge] therefore applies ONE patch, then
// re-discovers and rebuilds the rest against the updated tree, repeating until
// every patch comes back empty. A single pass would fail on exactly the repos this
// exists for.
//
// FAIL-SOFT ON DISCOVERY, FAIL-LOUD ON APPLY. This runs inside a git pre-commit
// hook, where a non-zero exit blocks the commit. A flaky or unavailable nix (no
// flake, offline, an eval error in an unrelated check) must therefore NOT block
// every commit in the repo: discovery and per-check build failures, and a run with
// no git worktree to apply to (conformist#132), warn and return success, leaving the drift check to catch any resulting staleness. A patch that
// was built but will not APPLY is different — the contract says it was generated
// from this very tree, so a refusal means something is genuinely inconsistent —
// and that exits non-zero. `--strict` promotes the soft failures to hard ones for
// a gate that would rather stop than proceed uncertain.
//
// THE FAILURE REACHES THE CALLER ONLY BECAUSE THE LINTER OPTS IN. conformist
// discards a `repair-command`'s exit status by default — most repairs are
// best-effort — so nix/linters/codegen-repair.nix sets `repair-must-succeed`, which
// turns a non-zero exit into an operational failure carrying this command's output.
// Without it the exit 2 below would vanish and a commit would proceed over an
// inconsistent tree.
//
// THE SOFT FAILURES, THOUGH, ARE STILL SILENT. The warnings go to this process's
// stderr, which conformist captures and logs at Debug while the default level is
// Warn — so a discovery failure looks exactly like a clean no-op unless someone
// passes -vv, which no hook does. Raising the level is not the fix (Warn would be
// wrong for a successful repair's chatter); it needs conformist to stop fusing
// stdout and stderr. Tracked separately; see
// docs/features/0001-generic-codegen-repair-linter.md.
package codegen

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"

	"code.linenisgreat.com/conformist/git"
	"github.com/charmbracelet/log"
)

// ErrRepairFailed is the operational-failure sentinel for this command, mapped to
// exit code 2 by cmd.ExitCode. It reports a failure the caller must act on: a
// patch that would not apply, a run that would not converge, or — under --strict —
// a discovery or build failure that would otherwise have been a warning.
var ErrRepairFailed = errors.New("codegen repair failed")

// PatchFile is the file the igloo#80 contract places in a codegenPatch
// derivation's output: a `git diff --no-index --binary src work`, empty when the
// tree is already current.
const PatchFile = "patch"

// patchStrip is the -p level `git apply` needs for a codegenPatch. The diff is
// produced with `git diff --no-index src work`, so its paths carry two synthetic
// leading components (`a/src/…`, `b/work/…`) that must be stripped to land on
// module-root-relative paths. This is the igloo#80 / goRun convention, not a
// choice made here.
const patchStrip = "-p2"

// experimentalFeatures is passed on every nix invocation so the command works on a
// host whose nix.conf has not enabled them. It is additive
// (--extra-experimental-features), so a host that already enables more keeps them.
// This matters for the standalone-artifact delivery route, where the caller's
// nix.conf is not ours to assume.
var experimentalFeatures = []string{"--extra-experimental-features", "nix-command flakes"}

// systemPattern bounds the nix system double interpolated into the discovery
// expression, so a bad --system cannot inject Nix source.
var systemPattern = regexp.MustCompile(`^[a-z0-9_]+-[a-z0-9]+$`)

// checkNamePattern bounds a discovered check name to what can be spelled UNQUOTED
// in a nix installable attrpath. A name needing quotes is skipped with a warning
// rather than guessed at: nix's attrpath quoting is not something to bet a
// fleet-wide commit hook on, and no check in the fleet needs it.
var checkNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_-]*$`)

// Check is one discovered codegen-repair lane: a `checks.<system>.<name>` attr
// carrying `passthru.codegenPatch`.
type Check struct {
	// Name is the attr name under `checks.<system>`.
	Name string `json:"name"`
	// Prefix is the check's `passthru.codegenPrefix` (igloo#83), and it is
	// deliberately a pointer because the contract has THREE states, not two:
	//
	//   "go" — the module root is that repo-relative subpath, so the patch must be
	//          applied with `git apply --directory=go`.
	//   ""   — the module IS the repo root, asserted by the check.
	//   nil  — UNKNOWN (the attr is absent, or present and null). igloo only
	//          auto-derives a prefix from the `self + "/subdir"` src shape; a
	//          module written `src = self` or `src = ./.` derives null even though
	//          it sits at the root, because bare self and an unrelated bare path
	//          are indistinguishable by string context. So nil is the COMMON case
	//          for a root module, not an error.
	//
	// A codegenPatch's paths are relative to the module root, NOT to the
	// repository, so conflating nil with "" would be a guess about where a patch
	// belongs. For nil this command applies at the tree root but only when the
	// patch's paths resolve there, and refuses loudly otherwise; for "" it knows
	// the root is right and reports a refusal as the plain conflict it is. See
	// [session.explainRefusal] and
	// docs/features/0001-generic-codegen-repair-linter.md.
	Prefix *string `json:"prefix"`
	// Includes is the check's own `passthru.codegenIncludes` (igloo#80): the
	// trigger globs a generator declares next to its check for inputs that are
	// not Go sources. It is reported here rather than acted on — see the
	// Limitations section of
	// docs/features/0001-generic-codegen-repair-linter.md for why the union with
	// the linter's own `includes` cannot be computed inside the consumer's own
	// module eval.
	Includes []string `json:"includes"`
}

// directory is the `git apply --directory` argument this check's patch needs, or
// "" when the patch applies at the tree root — which covers both an asserted root
// ("") and an unknown root (nil, where the tree root is the only candidate and
// [session.explainRefusal] guards the guess).
func (c Check) directory() string {
	if c.Prefix == nil {
		return ""
	}

	return *c.Prefix
}

// rootIsAsserted reports whether the check positively declared that its module is
// the repo root, as opposed to leaving it unknown. It separates "this really is the
// root" from "nobody said", which is the difference between reporting a refusal as
// a plain conflict and suggesting a subdirectory-rooted module.
func (c Check) rootIsAsserted() bool {
	return c.Prefix != nil && *c.Prefix == ""
}

// validate reports why this command cannot safely act on a discovered check, or
// nil when it can. The returned error is a bare reason, wrapped by the caller.
func (c Check) validate() error {
	if !checkNamePattern.MatchString(c.Name) {
		return errors.New(
			"its name cannot be spelled unquoted in a nix attrpath, so its patch cannot be built",
		)
	}

	if c.directory() == "" {
		return nil
	}

	// The prefix becomes a `git apply --directory` argument, so an absolute or
	// climbing value would write outside the tree. Refuse rather than sanitize:
	// a prefix this command does not understand is a contract mismatch, and
	// guessing at it is how a repair tool corrupts a repo.
	prefix := c.directory()

	if filepath.IsAbs(prefix) {
		return fmt.Errorf(
			"its passthru.codegenPrefix (%q) is absolute, but a codegen prefix must be a"+
				" tree-root-relative subdirectory",
			prefix,
		)
	}

	if slices.Contains(strings.Split(filepath.ToSlash(filepath.Clean(prefix)), "/"), "..") {
		return fmt.Errorf(
			"its passthru.codegenPrefix (%q) climbs out of the tree root", prefix,
		)
	}

	return nil
}

// Options configures one codegen-repair run.
type Options struct {
	// FlakeRef is the flake whose `checks.<system>` is enumerated. Defaults to
	// ".", which nix resolves to the enclosing git worktree.
	FlakeRef string
	// System is the nix system double selecting the checks attrset. Defaults to
	// the running binary's own platform (see NixSystem).
	System string
	// Nix is the nix executable. Defaults to "nix" resolved from PATH — the HOST
	// nix, deliberately.
	Nix string
	// TreeRoot is the directory nix runs in and `git apply` applies from. It MUST
	// be the git toplevel: for a `git+file` ref the flake root IS the git
	// toplevel, and a codegenPatch's paths are rooted at (or under, via
	// [Check.Prefix]) that root. Defaults to the resolved toplevel of the working
	// directory.
	TreeRoot string
	// Strict promotes a discovery or per-check build failure from a warning to
	// ErrRepairFailed.
	Strict bool
	// ListOnly performs discovery, prints the result as JSON, and applies
	// nothing. It is the operator diagnostic, and the thing the eval-cost
	// measurement times.
	ListOnly bool
	// Stdout receives the --list JSON. Defaults to os.Stdout.
	Stdout io.Writer
}

// NixSystem returns the nix system double for the running binary, mapping Go's
// GOARCH names onto nix's. It avoids a `nix eval builtins.currentSystem`
// subprocess on every commit; --system overrides it for the rare host whose nix
// reports something else (an x86_64 nix under emulation, say).
func NixSystem() string {
	arch := runtime.GOARCH

	switch arch {
	case "amd64":
		arch = "x86_64"
	case "arm64":
		arch = "aarch64"
	case "386":
		arch = "i686"
	}

	return arch + "-" + runtime.GOOS
}

// discoverExpr is the --apply function handed to `nix eval .#checks`. It takes the
// whole `checks` attrset (not `checks.<system>`) so a flake with no entry for this
// system yields an empty list instead of nix's "attribute not found" error.
//
// Every probe is wrapped in builtins.tryEval TWICE: once around the shallow
// passthru test, once around a deepSeq of the result. One check that cannot
// evaluate on this system (an unavailable package, a failing assert) must not
// abort discovery for every other check in the repo — and the shallow tryEval
// alone would not catch a throw hiding inside codegenIncludes, since tryEval
// forces only to weak head normal form.
const discoverExpr = `checks:
let
  systemChecks = checks.%q or { };
  probe =
    name:
    let
      probed = builtins.tryEval (
        let
          check = systemChecks.${name};
        in
        if check ? passthru && check.passthru ? codegenPatch then
          {
            inherit name;
            # Defaults to null, NOT to the empty string: an absent attr means the
            # module root is unknown, while an explicit "" means the check asserts
            # it IS the repo root (igloo#83). Defaulting to "" here would silently
            # turn "nobody said" into "definitely the root".
            prefix = check.passthru.codegenPrefix or null;
            includes = check.passthru.codegenIncludes or [ ];
          }
        else
          null
      );
      forced = builtins.tryEval (builtins.deepSeq probed.value probed.value);
    in
    if probed.success && forced.success then forced.value else null;
in
builtins.filter (entry: entry != null) (map probe (builtins.attrNames systemChecks))
`

// Resolve fills in the defaults Options leaves empty and validates what cannot be
// defaulted.
func (o *Options) Resolve() error {
	if o.Stdout == nil {
		o.Stdout = os.Stdout
	}

	if o.FlakeRef == "" {
		o.FlakeRef = "."
	}

	if o.Nix == "" {
		o.Nix = "nix"
	}

	if o.System == "" {
		o.System = NixSystem()
	}

	if !systemPattern.MatchString(o.System) {
		return fmt.Errorf("%w: %q is not a valid nix system double", ErrRepairFailed, o.System)
	}

	if o.TreeRoot == "" {
		root, err := defaultTreeRoot()
		if err != nil {
			return err
		}

		o.TreeRoot = root
	}

	return nil
}

// defaultTreeRoot resolves where nix runs and patches are applied: the git
// worktree root, but only when it is also the working directory.
//
// Both matter, and they are not the same thing. `nix eval .#checks` resolves `.`
// against the directory nix runs in, so that directory decides WHICH flake is
// enumerated; a codegenPatch's paths are rooted at that flake's root. conformist
// invokes a whole-tree repair with the cwd set to ITS tree root, which a repo can
// point somewhere other than the git toplevel (--tree-root, --tree-root-file).
// Taking the toplevel unconditionally would then enumerate the OUTER flake of a
// nested layout and apply its patches at the outer root, while the linter's own
// `[ -f flake.nix ]` gate had checked the inner one — repairing a flake nobody
// asked about. Refuse that instead: the case is rare, and there is no safe guess
// between two flakes.
func defaultTreeRoot() (string, error) {
	top, err := gitToplevel()
	if err != nil {
		return "", err
	}

	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("%w: cannot resolve the working directory: %w", ErrRepairFailed, err)
	}

	// Compare the resolved forms on BOTH sides: either path may reach the same
	// directory through a symlink (eng-design_patterns-paths(7)), and a spurious
	// mismatch here would refuse a perfectly ordinary repo.
	realTop, topErr := filepath.EvalSymlinks(top)
	realCwd, cwdErr := filepath.EvalSymlinks(cwd)

	if topErr == nil && cwdErr == nil && realTop != realCwd {
		return "", fmt.Errorf(
			"%w: the flake to repair is ambiguous — this command was run in %s but the git"+
				" worktree root is %s, and a codegen patch is rooted at the flake nix resolves"+
				" from the directory it runs in. Pass --tree-root to name the one you mean",
			ErrRepairFailed, cwd, top,
		)
	}

	return top, nil
}

// errNoWorktree marks the "no git worktree to repair" case, which [Run] treats
// as a soft failure (conformist#132).
var errNoWorktree = errors.New("not inside a git worktree, so there is no tree to apply a codegen patch to")

// gitToplevel resolves the git worktree root of the working directory.
func gitToplevel() (string, error) {
	out, err := exec.Command(git.Binary, "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return "", fmt.Errorf("%w: %w: %w", ErrRepairFailed, errNoWorktree, err)
	}

	return strings.TrimSpace(string(out)), nil
}

// Run performs one codegen-repair run: discover, build, apply, repeat until every
// patch is empty.
func Run(ctx context.Context, opts Options) error {
	if err := opts.Resolve(); err != nil {
		// No worktree (e.g. a nix sandbox, conformist#132) means there is nothing
		// to apply a patch to — the same case as the linter's no-flake.nix gate —
		// so it fails soft like discovery does, and --strict (or --list, which
		// reports rather than repairs) keeps it loud.
		if errors.Is(err, errNoWorktree) && !opts.Strict && !opts.ListOnly {
			log.Warnf("codegen-repair: %v — nothing to repair; pass --strict to fail instead", errNoWorktree)

			return nil
		}

		return err
	}

	// --list is a diagnostic (and the eval-cost measurement's subject), so it
	// reports a broken discovery instead of swallowing it: an operator asking
	// what was found wants the nix error, and a measurement that silently timed
	// nothing would be worse than no measurement.
	if opts.ListOnly {
		checks, err := Discover(ctx, opts)
		if err != nil {
			return fmt.Errorf("%w: %w", ErrRepairFailed, err)
		}

		return writeJSON(opts.Stdout, checks)
	}

	s := &session{opts: opts}

	// The index bookkeeping below must be undone even when the run is cancelled
	// mid-pass, so the cleanup deliberately does not inherit the cancellation.
	defer s.restoreIndex(context.WithoutCancel(ctx))

	return s.converge(ctx)
}

// session carries one run's mutable state across convergence passes: the paths it
// made visible to nix with `git add --intent-to-add` (undone before returning),
// the checks it gave up on (warned, not fatal), and the checks whose patches it
// applied — named rather than counted so a run that fails to converge can say what
// it already wrote to the tree.
type session struct {
	opts         Options
	intentAdded  []string
	unrepaired   []string
	appliedNames []string
}

// converge runs repair passes until no check has a non-empty patch left.
//
// Applying ONE patch per pass and rebuilding the rest is what makes overlapping
// checks work (igloo#80 note 3): a codegenPatch is a diff against its own check's
// `src`, so two checks that rewrite the same file produce patches that conflict
// with each other, and only the second one's REBUILD — against the tree as the
// first one left it — is applicable.
func (s *session) converge(ctx context.Context) error {
	limit := 0

	for pass := 1; ; pass++ {
		checks, ok, err := s.discover(ctx, pass)
		if err != nil {
			return err
		}

		if !ok {
			// Discovery failed softly; already warned.
			return nil
		}

		if len(checks) == 0 {
			log.Debugf(
				"codegen-repair: no checks.%s.* carries passthru.codegenPatch", s.opts.System,
			)

			return nil
		}

		if limit == 0 {
			limit = passLimit(len(checks))

			s.reportIncludes(checks)
		}

		if pass > limit {
			// Name what was already applied: this path leaves the tree MUTATED by
			// every patch that landed before the loop gave up, and nothing reverts
			// them. Whoever untangles an oscillating pair of generators needs to
			// know which ones wrote, in order.
			return fmt.Errorf(
				"%w: repair did not converge in %d passes over %d check(s) — two generators are"+
					" most likely overwriting each other's output. The tree still holds the"+
					" patches applied along the way, in this order: %s. Nothing was reverted;"+
					" inspect them with `conformist codegen-repair --list` and by hand",
				ErrRepairFailed, limit, len(checks), strings.Join(s.appliedNames, ", "),
			)
		}

		progressed, err := s.pass(ctx, checks)
		if err != nil {
			return err
		}

		if !progressed {
			return s.result(len(checks))
		}
	}
}

// passLimit bounds the convergence loop. A well-behaved check needs one apply, so
// len(checks) applies plus a confirming pass would do — but a legitimate CHAIN
// (generator A's output is generator B's input, so A must run again once B has
// landed) needs more, hence the doubling. Anything past that is not a chain but a
// cycle: two generators each undoing the other, which would loop forever and must
// instead fail where someone can see it.
//
// Only called with checks >= 1 (a run with none returns before this), so the
// smallest bound is 4 passes.
func passLimit(checks int) int {
	return 2*checks + 2
}

// reportIncludes surfaces the trigger globs the discovered checks declare. The
// linter's own `includes` decide whether this repair runs at all, and they are
// configured independently of what a generator asks for, so a mismatch is
// something an operator has to be told about rather than something this command
// can reconcile. Logged once per run, not once per pass.
func (s *session) reportIncludes(checks []Check) {
	for _, check := range checks {
		if len(check.Includes) == 0 {
			continue
		}

		log.Infof(
			"codegen-repair: %s declares codegenIncludes %v — confirm the linter's own includes"+
				" cover them, or a change to those files will not trigger this repair",
			check.Name, check.Includes,
		)
	}
}

// discover enumerates the codegen checks for one pass. The bool reports whether
// discovery succeeded: a soft failure warns and returns false, so the caller stops
// without failing the commit.
func (s *session) discover(ctx context.Context, pass int) ([]Check, bool, error) {
	checks, err := Discover(ctx, s.opts)
	if err == nil {
		return checks, true, nil
	}

	if s.opts.Strict {
		return nil, false, fmt.Errorf("%w: %w", ErrRepairFailed, err)
	}

	// Fail-soft: a pre-commit hook that cannot reach nix must not block the
	// commit. The drift check remains the gate, so the worst case is the
	// staleness we were trying to pre-empt — not a repo nobody can commit to.
	if pass == 1 {
		log.Warnf(
			"codegen-repair: discovery failed, so generated files may land stale: %v"+
				" — pass --strict to fail instead",
			err,
		)
	} else {
		log.Warnf(
			"codegen-repair: discovery failed after applying %d patch(es) (%s), so the tree may"+
				" be only partly repaired: %v",
			len(s.appliedNames), strings.Join(s.appliedNames, ", "), err,
		)
	}

	return nil, false, nil
}

// pass builds each outstanding check's patch in turn and applies the FIRST
// non-empty one, reporting whether it applied anything. Returning after a single
// apply is what makes the loop correct: every other patch was computed against the
// tree as it was before, and is now stale.
func (s *session) pass(ctx context.Context, checks []Check) (bool, error) {
	for _, check := range checks {
		// A check already given up on stays given up on: retrying it every pass
		// would repeat its build and its warning without new information.
		if slices.Contains(s.unrepaired, check.Name) {
			continue
		}

		if err := check.validate(); err != nil {
			s.giveUp(check.Name, err)

			continue
		}

		applied, err := s.repairOne(ctx, check)
		if err != nil {
			if errors.Is(err, ErrRepairFailed) {
				return false, err
			}

			s.giveUp(check.Name, err)

			continue
		}

		if applied {
			s.appliedNames = append(s.appliedNames, check.Name)

			log.Infof("codegen-repair: applied %s's codegen patch", check.Name)

			return true, nil
		}
	}

	return false, nil
}

// giveUp records a check this run will not repair and says so. It is a warning
// rather than a failure for the same reason discovery fails soft: the drift check
// is the gate, and a commit hook that stops the world over one unbuildable check
// is worse than one that lets the gate catch it.
func (s *session) giveUp(name string, err error) {
	s.unrepaired = append(s.unrepaired, name)

	log.Warnf("codegen-repair: %s not repaired, so its generated files may land stale: %v", name, err)
}

// result is the verdict once the tree has converged.
func (s *session) result(total int) error {
	if len(s.unrepaired) > 0 && s.opts.Strict {
		return fmt.Errorf(
			"%w: %d of %d codegen check(s) could not be repaired: %s",
			ErrRepairFailed, len(s.unrepaired), total, strings.Join(s.unrepaired, ", "),
		)
	}

	log.Debugf("codegen-repair: converged after applying %d patch(es)", len(s.appliedNames))

	return nil
}

// Discover enumerates the flake's `checks.<system>` attrs that carry
// `passthru.codegenPatch`, sorted by name so a run's order — and so its log — is
// deterministic.
func Discover(ctx context.Context, opts Options) ([]Check, error) {
	args := append([]string{"eval", "--json", "--no-warn-dirty"}, experimentalFeatures...)
	args = append(
		args,
		opts.FlakeRef+"#checks",
		"--apply", fmt.Sprintf(discoverExpr, opts.System),
	)

	stdout, err := runNix(ctx, opts, args)
	if err != nil {
		return nil, err
	}

	var checks []Check
	if err := json.Unmarshal(stdout, &checks); err != nil {
		return nil, fmt.Errorf("failed to decode the discovery result as JSON: %w", err)
	}

	slices.SortFunc(checks, func(a, b Check) int { return strings.Compare(a.Name, b.Name) })

	return checks, nil
}

// repairOne builds one check's codegenPatch and applies it, reporting whether
// anything was applied. An error wrapping ErrRepairFailed is fatal (the patch
// exists but will not apply); any other error is the caller's to downgrade to a
// warning.
func (s *session) repairOne(ctx context.Context, check Check) (bool, error) {
	outPath, err := s.buildPatch(ctx, check.Name)
	if err != nil {
		return false, err
	}

	patch := filepath.Join(outPath, PatchFile)

	info, err := os.Stat(patch)
	if err != nil {
		return false, fmt.Errorf(
			"its codegenPatch does not expose %s, breaking the igloo#80 contract: %w",
			PatchFile, err,
		)
	}

	if info.Size() == 0 {
		// The contract's "already current" signal.
		log.Debugf("codegen-repair: %s is current (empty patch)", check.Name)

		return false, nil
	}

	return s.applyPatch(ctx, check, patch)
}

// buildPatch realizes one check's codegenPatch derivation and returns its store
// path.
func (s *session) buildPatch(ctx context.Context, name string) (string, error) {
	attr := fmt.Sprintf(
		"%s#checks.%s.%s.passthru.codegenPatch",
		s.opts.FlakeRef, s.opts.System, name,
	)

	args := append(
		[]string{"build", "--no-link", "--print-out-paths", "--no-warn-dirty"},
		experimentalFeatures...,
	)
	args = append(args, attr)

	stdout, err := runNix(ctx, s.opts, args)
	if err != nil {
		return "", err
	}

	for _, line := range strings.Split(string(stdout), "\n") {
		if path := strings.TrimSpace(line); path != "" {
			return path, nil
		}
	}

	return "", fmt.Errorf("nix build of %s printed no output path", attr)
}

// applyPatch applies a non-empty codegenPatch, reporting whether it changed
// anything.
//
// The forward --check is tried first, then the REVERSE --check: a patch that
// applies cleanly in reverse is already in the tree, which makes a repeated repair
// over the same tree state a quiet no-op instead of a spurious failure (`nix fmt`
// followed by `--staged`, or simply running the hook twice). Only when neither
// direction applies is the patch genuinely in conflict, and that is the one
// failure this command refuses to bury.
func (s *session) applyPatch(ctx context.Context, check Check, patch string) (bool, error) {
	if err := s.gitApply(ctx, check, patch, "--check"); err != nil {
		if reverseErr := s.gitApply(ctx, check, patch, "--check", "--reverse"); reverseErr == nil {
			log.Debugf("codegen-repair: %s's patch is already applied", check.Name)

			return false, nil
		}

		return false, s.explainRefusal(ctx, check, patch, err)
	}

	// Snapshot before applying so the files this patch CREATES can be told from
	// the ones that were already untracked (see trackCreated).
	before, beforeErr := git.StatusEntriesWithUntracked(ctx, s.opts.TreeRoot)

	if err := s.gitApply(ctx, check, patch); err != nil {
		return false, fmt.Errorf(
			"%w: %s's codegen patch passed --check but failed to apply: %w",
			ErrRepairFailed, check.Name, err,
		)
	}

	if beforeErr != nil {
		log.Debugf(
			"codegen-repair: could not snapshot git status, so any file %s created stays"+
				" invisible to the next pass: %v",
			check.Name, beforeErr,
		)

		return true, nil
	}

	s.trackCreated(ctx, before)

	return true, nil
}

// trackCreated makes the files the patch just applied CREATED visible to the next
// pass's nix build. nix resolves a dirty `git+file` flake from TRACKED paths only,
// so a brand-new generated file is invisible to the generator that consumes it;
// `git add --intent-to-add` records the path without staging any content, which is
// all nix needs.
//
// Every path recorded here is removed from the index again by restoreIndex before
// this command returns. Staging a generated file is the --staged/--commit hook's
// decision — conformist#56's stage-new-outputs opt-in — not this command's, and an
// intent-to-add entry left behind would commit as an EMPTY file for a caller who
// did not opt in. Undoing it also means the hook's own status-delta attribution
// sees the file exactly as it would have: untracked and new.
func (s *session) trackCreated(ctx context.Context, before []git.StatusEntry) {
	after, err := git.StatusEntriesWithUntracked(ctx, s.opts.TreeRoot)
	if err != nil {
		log.Debugf("codegen-repair: could not re-read git status after applying a patch: %v", err)

		return
	}

	known := make(map[string]struct{}, len(before))
	for _, entry := range before {
		known[entry.Path] = struct{}{}
	}

	var created []string

	for _, entry := range after {
		// Untracked entries carry '?' in both porcelain columns.
		if entry.Staged != '?' {
			continue
		}

		if _, seen := known[entry.Path]; seen {
			continue
		}

		created = append(created, entry.Path)
	}

	if len(created) == 0 {
		return
	}

	args := append([]string{"add", "--intent-to-add", "--"}, created...)
	if err := s.git(ctx, args...); err != nil {
		log.Debugf(
			"codegen-repair: could not make %v visible to the next pass: %v", created, err,
		)

		return
	}

	s.intentAdded = append(s.intentAdded, created...)
}

// restoreIndex drops the intent-to-add entries this run added, leaving the index
// exactly as it was found. See trackCreated for why they were added and why they
// must not survive the run.
func (s *session) restoreIndex(ctx context.Context) {
	if len(s.intentAdded) == 0 {
		return
	}

	// `git update-index --force-remove` drops the entry without touching the
	// worktree file, and works in a repository with no commits — unlike
	// `git reset -- <path>`, which needs a HEAD to reset against.
	args := append([]string{"update-index", "--force-remove", "--"}, s.intentAdded...)
	if err := s.git(ctx, args...); err != nil {
		log.Warnf(
			"codegen-repair: failed to unstage the intent-to-add entries this run added"+
				" (%s); clear them with `git reset --` before committing: %v",
			strings.Join(s.intentAdded, " "), err,
		)
	}
}

// explainRefusal turns a patch that will not apply into a message naming the
// likely cause.
//
// The generic case is genuinely ambiguous — the tree changed under the build, or
// the patch is rooted somewhere else. But a check whose module root is UNKNOWN
// (nil codegenPrefix) and whose patch targets files absent from the tree root is
// very likely a subdirectory-rooted module, which is the one case this command
// cannot place a patch into; naming that saves the operator a hunt.
//
// A check that ASSERTED its root (an explicit "") gets the generic message even
// then: the root is not in question, so suggesting a subdirectory would send them
// after something the check has already ruled out.
func (s *session) explainRefusal(
	ctx context.Context, check Check, patch string, applyErr error,
) error {
	if check.Prefix == nil {
		if missing := s.missingTargets(ctx, check, patch); len(missing) > 0 {
			return fmt.Errorf(
				"%w: %s's codegen patch targets file(s) absent from the tree root (%s) and its"+
					" check publishes no passthru.codegenPrefix, so its module root is most likely a"+
					" repository subdirectory — which cannot be placed without that prefix."+
					" Have the check pass codegenPrefix (igloo#83 auto-derives it only for a"+
					" `src = self + \"/subdir\"` module), or regenerate from that subdirectory by"+
					" hand: %w",
				ErrRepairFailed, check.Name, strings.Join(missing, ", "), applyErr,
			)
		}
	}

	return fmt.Errorf(
		"%w: %s's codegen patch does not apply to this tree — it was generated from this very"+
			" tree by nix, so a refusal means the tree changed under the build: %w",
		ErrRepairFailed, check.Name, applyErr,
	)
}

// missingTargets lists the paths a patch would touch that it expects to ALREADY
// EXIST but that are absent from the tree. `git apply --numstat` reports each path
// with the -p strip and any --directory prefix already applied, so this asks git
// where the patch would land rather than re-deriving that from the diff here.
//
// Paths the patch CREATES are excluded, because their absence is what a creation
// hunk means. Without that exclusion any patch containing a new file would look
// like a wrong-prefix patch, and a root-rooted check that merely conflicted would
// be misdiagnosed as a subdirectory-module problem — a confidently wrong
// diagnosis, which is worse than the generic one it would replace.
func (s *session) missingTargets(ctx context.Context, check Check, patch string) []string {
	out, err := s.gitOutput(ctx, s.applyArgs(check, patch, "--numstat")...)
	if err != nil {
		log.Debugf("codegen-repair: could not list %s's patch targets: %v", check.Name, err)

		return nil
	}

	created := s.createdTargets(ctx, check, patch)

	var missing []string

	for _, line := range strings.Split(string(out), "\n") {
		// numstat is TAB-separated ("<added>\t<removed>\t<path>"); splitting on
		// whitespace would truncate a path containing a space.
		fields := strings.SplitN(strings.TrimRight(line, "\r"), "\t", 3)
		if len(fields) < 3 {
			continue
		}

		path := fields[2]
		if path == "" {
			continue
		}

		if _, isNew := created[path]; isNew {
			continue
		}

		if _, err := os.Lstat(filepath.Join(s.opts.TreeRoot, path)); err != nil {
			missing = append(missing, path)
		}
	}

	return missing
}

// createdTargets is the set of paths a patch creates, as `git apply --summary`
// reports them ("create mode <mode> <path>"), with the same -p strip and
// --directory prefix applied as missingTargets sees. Deletions are deliberately
// NOT collected: a deletion hunk's target must already exist, so its absence is
// real evidence.
func (s *session) createdTargets(
	ctx context.Context, check Check, patch string,
) map[string]struct{} {
	created := map[string]struct{}{}

	out, err := s.gitOutput(ctx, s.applyArgs(check, patch, "--summary")...)
	if err != nil {
		// Without the summary the caller falls back to treating every absent path
		// as missing, so say so rather than diagnosing from a partial picture.
		log.Debugf(
			"codegen-repair: could not list the files %s's patch creates, so its"+
				" wrong-prefix diagnosis may over-report: %v",
			check.Name, err,
		)

		return created
	}

	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(strings.TrimRight(line, "\r"))

		// " create mode 100644 <path>" — the path is the remainder, so rejoin it
		// in case it contains spaces.
		if len(fields) >= 4 && fields[0] == "create" && fields[1] == "mode" {
			created[strings.Join(fields[3:], " ")] = struct{}{}
		}
	}

	return created
}

// applyArgs builds one `git apply` argument list. It is rebuilt per invocation
// rather than appended to a shared slice, so two variants of the same call cannot
// end up sharing backing storage.
//
// --whitespace=nowarn keeps a pre-commit hook's output to the point: whitespace in
// generated content is the generator's business, and git's default `warn` would
// editorialise on every commit without changing the outcome.
func (s *session) applyArgs(check Check, patch string, extra ...string) []string {
	args := []string{"apply", patchStrip, "--whitespace=nowarn"}

	if dir := check.directory(); dir != "" {
		args = append(args, "--directory="+dir)
	}

	args = append(args, extra...)

	return append(args, patch)
}

// gitApply runs one `git apply` invocation for a check's patch.
func (s *session) gitApply(ctx context.Context, check Check, patch string, extra ...string) error {
	return s.git(ctx, s.applyArgs(check, patch, extra...)...)
}

// git runs one git subcommand at the tree root, discarding its stdout.
func (s *session) git(ctx context.Context, args ...string) error {
	_, err := s.gitOutput(ctx, args...)

	return err
}

// gitOutput runs one git subcommand at the tree root and returns its stdout.
func (s *session) gitOutput(ctx context.Context, args ...string) ([]byte, error) {
	full := make([]string, 0, len(args)+2)
	full = append(full, "-C", s.opts.TreeRoot)
	full = append(full, args...)

	cmd := exec.CommandContext(ctx, git.Binary, full...)

	var stdout, stderr bytes.Buffer

	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("%s: %w", messageOf(&stderr, err), err)
	}

	return stdout.Bytes(), nil
}

// runNix runs one nix invocation from the tree root and returns its stdout.
func runNix(ctx context.Context, opts Options, args []string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, opts.Nix, args...)
	cmd.Dir = opts.TreeRoot

	var stdout, stderr bytes.Buffer

	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("%s %s: %s: %w", opts.Nix, args[0], messageOf(&stderr, err), err)
	}

	return stdout.Bytes(), nil
}

// messageOf returns a subprocess's stderr trimmed to something loggable on one
// screen, falling back to the error itself when the tool said nothing. The TAIL
// is kept rather than the head: nix puts the actual cause last.
func messageOf(stderr *bytes.Buffer, err error) string {
	const maxMessage = 2000

	message := strings.TrimSpace(stderr.String())
	if message == "" {
		return err.Error()
	}

	if len(message) > maxMessage {
		message = "…" + message[len(message)-maxMessage:]
	}

	return message
}

// writeJSON emits the discovery result for --list. An empty result is written as
// `[]`, not `null`, so a consumer can pipe it into jq unconditionally.
func writeJSON(w io.Writer, checks []Check) error {
	if checks == nil {
		checks = []Check{}
	}

	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")

	if err := encoder.Encode(checks); err != nil {
		return fmt.Errorf("failed to write the discovery result: %w", err)
	}

	return nil
}
