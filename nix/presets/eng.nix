# conformist eng preset (pure lane): enable the language-agnostic eng-convention
# enforcers in one import. A repo adopting conformist gets the whole roster with
#
#     imports = [ conformist.lib.presets.eng ];
#
# instead of enabling each linter by hand. These checks read only committed files
# (version.env, go.mod/Cargo.toml, flake.nix/flake.lock, justfile), so they run in
# the sandboxed checks.formatting lane.
#
# Deliberately NOT enabled here:
#   - non-Go formatters (nixfmt/...) and `shellcheck` — a repo picks its own;
#     the canonical Go formatter chain (goimports+gofumpt) lives in the sibling
#     `conformist.lib.presets.eng-go`, kept separate so a non-Go repo importing
#     `eng` never pulls a Go toolchain;
#   - `golangci-dewey` — Go *linting* (a separate adoption decision from the
#     eng-go formatter chain; enable it explicitly);
#   - the git-state checks (git-remotes, sweatfile, ...) — they need a live .git
#     and live in conformist.lib.presets.eng-impure (the impure lane);
#   - THE justfile-* CONVENTION LINTERS — they moved to just-us. See below.
#
# THIS PRESET IS NOT THE WHOLE eng ROSTER ANYMORE. The seven justfile-*
# convention linters (justfile-default, justfile-recipe-names,
# justfile-debug-recipes, justfile-recipe-descriptions, justfile-task-hierarchy,
# justfile-leaf-noun, justfile-aggregate-comments) used to be enabled here. They
# now ship from just-us, alongside justfile-orphan-summary, because they read
# `just --dump --dump-format model` — a fork-only dump format a stock `just`
# rejects. conformist must stay strictly UPSTREAM of its consumers and so cannot
# take just-us as an input (just-us already inputs conformist; that would be a
# cycle), which is why the coupling lives in the repo that owns the parser. The
# rules themselves are still conformist's: conformist-justfile(7) remains their
# normative home, and these modules cite it.
#
# To get the full eng roster, import BOTH and set the shared package once:
#
#     imports = [
#       conformist.lib.presets.eng
#       just-us.lib.conformistPresets.justfile
#     ];
#     linters.justfile-common.justPackage = just-us.packages.${system}.default;
#
# Importing this preset ALONE is legitimate — a repo may not want the justfile
# rules — but it is silently a smaller roster than it was, so it is spelled out
# here rather than left to be discovered. templates/eng/ wires both.
#
# conformist self-consumes this preset (nix/conformist.nix), so it can't drift.
# See conformist-nix(7), conformist-justfile(7), eng-versioning(7).
{ ... }:
{
  # eng-versioning(7): version.env declares the canonical <REPO>_VERSION; no
  # deprecated version.txt / flake.nix named version var.
  linters.eng-versioning.enable = true;
  linters.eng-versioning-deprecated-file.enable = true;

  # conformist-nix(7): flake outputs formal accepts all inputs; flake.lock committed.
  linters.flake-outputs.enable = true;
  linters.flake-lock.enable = true;

  # conformist-git(7) MERGE DRIVERS: .gitattributes binds flake.lock to the
  # regenerate-on-conflict merge driver, so a rebase does not stall on a lock
  # whose two sides are both merely stale. Pure — it reads .gitattributes and
  # flake.nix, not live git state, so it belongs here rather than in eng-impure.
  # Only flake.lock is bound by default; a repo adds its own generated-source
  # globs via linters.git-merge-drivers.entries.
  linters.git-merge-drivers.enable = true;

  # conformist-justfile(7): the justfile convention roster is NOT enabled here.
  # It ships from just-us as `lib.conformistPresets.justfile` — see the header.
}
