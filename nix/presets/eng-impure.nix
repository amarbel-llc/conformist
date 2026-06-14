# conformist eng preset (impure lane): the eng-convention checks that need a live
# working tree or host tools — a real `.git` (git-remotes, git-default-branch,
# agents-md's `git mv` repair) or a profile-installed `spinclass` (sweatfile).
# They CANNOT run in the sandboxed checks.formatting (which sees only a
# /nix/store copy of tracked files), so a consumer wires them into a working-tree
# `conformist check` lane (the `lint-worktree` recipe), separate from the pure
# `conformist.lib.presets.eng`.
#
#     imports = [ conformist.lib.presets.eng-impure ];
#
# conformist self-consumes this preset (nix/conformist-impure.nix).
# See eng-ssh(7)/eng(7) (git remotes), eng-rcm(7) (sweatfile), conformist#18 (agents-md).
{ ... }:
{
  linters.git-remotes.enable = true;
  linters.git-default-branch.enable = true;
  linters.sweatfile.enable = true;
  linters.agents-md.enable = true;
}
