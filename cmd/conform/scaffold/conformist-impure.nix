# This repo's IMPURE conformist overlay, merged with conformist.lib.presets.eng-impure
# in flake.nix. The impure lane runs `conformist check` against the live working
# tree (`just lint-worktree`), so settings for checks that need a real .git or
# toolchain belong here rather than in conformist.nix.
{ ... }:
{
  # Repo-specific impure-lane settings go here, for example an origin remote on a
  # host other than code.linenisgreat.com:
  #   linters.git-remotes.allowed-hosts = [ "forge.example.org" ];
  # `conformist conform --allow-host <host>` writes that line for you (and adds the
  # origin's host automatically).
}
