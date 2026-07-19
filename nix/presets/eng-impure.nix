# conformist eng preset (impure lane): the eng-convention checks that need a live
# working tree or host tools — a real `.git` (git-remotes, git-default-branch,
# agents-md's `git mv` repair, gomod2nix's `git add` repair), a profile-installed
# `spinclass` (sweatfile), or module resolution against the real source
# (gomod2nix regenerates from go.mod/go.sum). They CANNOT run in the sandboxed
# checks.formatting (which sees only a /nix/store copy of tracked files), so a
# consumer wires them into a working-tree `conformist check` lane (the
# `lint-worktree` recipe), separate from the pure `conformist.lib.presets.eng`.
#
#     imports = [ conformist.lib.presets.eng-impure ];
#
# conformist self-consumes this preset (nix/conformist-impure.nix). Each linter
# self-gates on file presence (gomod2nix no-ops without a go.mod), so the roster
# is safe for repos that don't use every convention. The gate holds at nix-eval
# time too: a pkgs without igloo's overlay (no `pkgs.gomod2nix`) still
# evaluates — the gomod2nix linter degrades to a fallback that only fails, with
# the actionable remedy, when a go.mod is actually present (conformist#93).
# See eng-ssh(7)/eng(7) (git remotes), eng-rcm(7) (sweatfile), conformist#18 (agents-md).
{ ... }:
{
  linters.git-remotes.enable = true;
  linters.git-default-branch.enable = true;
  linters.sweatfile.enable = true;
  linters.agents-md.enable = true;
  linters.gomod2nix.enable = true;
}
