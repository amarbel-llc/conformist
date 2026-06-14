# conformist's IMPURE self-check config: whole-tree checks that need the live
# working tree or host tools (a real .git, or `spinclass` from the user profile)
# and therefore CANNOT run in the sandboxed checks.formatting (which sees only a
# /nix/store copy of tracked files). Consumed by `just lint-worktree`, which runs
# `conformist check` against the working tree. `package` is injected by flake.nix
# (conformistImpureEval). See the non-sandbox lane.
{ ... }:
{
  projectRootFile = "flake.nix";

  # The impure eng roster (git-remotes / git-default-branch need a live .git;
  # sweatfile runs `spinclass validate`; agents-md's repair runs `git mv` and the
  # check must see the real CLAUDE.md symlink in the working tree, not a
  # /nix/store copy). These need the working tree / host tools, so they run via
  # `just lint-worktree`, not the sandboxed checks.formatting. Same roster a
  # downstream repo gets from `imports = [ conformist.lib.presets.eng-impure ]`.
  imports = [ ./presets/eng-impure.nix ];
}
