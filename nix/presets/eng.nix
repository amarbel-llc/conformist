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
#     and live in conformist.lib.presets.eng-impure (the impure lane).
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

  # conformist-justfile(7): the full justfile convention roster.
  linters.justfile-default.enable = true;
  linters.justfile-recipe-names.enable = true;
  linters.justfile-debug-recipes.enable = true;
  linters.justfile-recipe-descriptions.enable = true;
  linters.justfile-task-hierarchy.enable = true;
  linters.justfile-leaf-noun.enable = true;
  linters.justfile-aggregate-comments.enable = true;
}
