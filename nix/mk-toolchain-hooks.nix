# Three toolchain-hermetic conformist wrappers for a repo with a HAND-WRITTEN
# conformist.toml — the TOML-consumer mirror of the module's
# build.{wrapper,preCommit,repair} (conformist#59).
#
# A module adopter gets store-pinned hook commands for free
# (build.preCommit / build.repair in module-options.nix): the generated config
# pins every formatter/linter command to a store path. A repo that keeps a
# hand-written conformist.toml can't — conformist resolves each bare-name
# `command` (gofumpt, nixfmt, …) from PATH at run time, so a `conformist
# --staged` hook run from a shell missing those tools SILENTLY SKIPS their file
# types (the silent-skip trap of conformist#51). wrapWithToolchain (#51) fixes
# that for a SINGLE wrapper; this helper returns the THREE named wrappers a
# consumer wires the same way a module adopter wires build.{wrapper,preCommit,
# repair}, so the two consumer shapes stay 1:1 and a repo can move between them
# without renaming its hooks:
#
#   { formatter   = conformist;             # nix fmt / check / repair entrypoint
#     preCommit   = conformist-pre-commit;  # `--staged --exit-zero-on-fix`
#     repair      = conformist-repair; }    # `--commit --amend --exit-zero-on-fix`
#
# Each is a writeShellApplication that execs conformist with `tools` (and
# conformist itself, by absolute store path) on PATH, so output is reproducible
# regardless of the ambient environment. Like the module wrappers, all three
# bake `--tree-root-file=${projectRootFile}` (default flake.nix), so the tree
# root is the repo root regardless of the invoking CWD — a git hook and `nix
# fmt` both run from the repo root, so this matches a bare CWD-default run there
# while additionally being correct from a subdirectory. `unset PRJ_ROOT` keeps
# direnv's PRJ_ROOT from overriding the baked --tree-root-file (it binds to the
# mutually-exclusive --tree-root via viper; see cmd/root.go).
#
# `tools` is the formatter/linter toolchain; conformist supplies these on PATH.
# A linter that itself execs an AMBIENT tool by bare name (e.g. a `golangci-lint
# run ./...` linter needs `go`, a codegen-repair driver needs its generator) is
# NOT made hermetic by this wrapper — supply such tools via the consumer's
# devShell (or add them to `tools`). This mirrors build.preCommit's same caveat.
#
# Usage (eachDefaultSystem or flake-parts, with `pkgs` + the `conformist` input
# in scope):
#
#   hooks = conformist.lib.mkToolchainHooks pkgs {
#     conformist = conformist.packages.${system}.default;
#     tools = [ pkgs.gofumpt pkgs.gotools pkgs.nixfmt-rfc-style pkgs.shfmt ];
#     configFile = ./conformist.toml;   # optional; pins --config-file
#   };
#   # then:
#   formatter = hooks.formatter;                 # nix fmt
#   devShells.default = pkgs.mkShell {
#     packages = [ hooks.formatter hooks.preCommit hooks.repair ] ++ tools;
#   };
#   # and in the sweatfile:
#   #   pre-commit = "conformist-pre-commit"
#   #   repair     = "conformist-repair"
#
# Wire `hooks.formatter` (named `conformist`) onto PATH IN PLACE OF the bare
# conformist binary, not alongside it — otherwise PATH order decides which
# `conformist` a bare invocation resolves to.
pkgs:
{
  conformist,
  tools,
  configFile ? null,
  projectRootFile ? "flake.nix",
}:
let
  inherit (pkgs) lib;
  # Resolve conformist to its absolute store path and exec THAT, not the name
  # `conformist` on PATH — the formatter wrapper is itself named `conformist`,
  # so a bare `exec conformist` could re-resolve to the wrapper and recurse.
  conformistBin = lib.getExe' conformist "conformist";

  mkWrapper =
    {
      name,
      modeFlags,
    }:
    let
      args = lib.concatStringsSep " " (
        [ conformistBin ]
        ++ lib.optional (configFile != null) "--config-file=${configFile}"
        ++ [ "--tree-root-file=${projectRootFile}" ]
        ++ modeFlags
      );
    in
    pkgs.writeShellApplication {
      inherit name;
      # tools carry the formatter/linter toolchain on PATH; conformist itself is
      # exec'd by absolute path, so it need not be a runtimeInput.
      runtimeInputs = tools;
      text = ''
        unset PRJ_ROOT
        exec ${args} "$@"
      '';
    };
in
{
  # `nix fmt` / `conformist check` / repair entrypoint. Mirrors build.wrapper.
  formatter = mkWrapper {
    name = "conformist";
    modeFlags = [ ];
  };

  # git pre-commit hook: format+restage the index-staged content, exit 0 even
  # when it applied fixes (conformist#25/#40, #35/#39). Mirrors build.preCommit.
  preCommit = mkWrapper {
    name = "conformist-pre-commit";
    modeFlags = [
      "--staged"
      "--exit-zero-on-fix"
    ];
  };

  # repair hook: repair the worktree and fold the fixes into HEAD via
  # `git commit --amend --no-edit`, exit 0 even when it amended (conformist#24/
  # #33, #35/#39). Mirrors build.repair.
  repair = mkWrapper {
    name = "conformist-repair";
    modeFlags = [
      "--commit"
      "--amend"
      "--exit-zero-on-fix"
    ];
  };
}
