# This repo's IMPURE conformist overlay, merged with conformist.lib.presets.eng-impure
# in flake.nix. The impure lane runs `conformist check` against the live working
# tree (`just lint-worktree`), so unlike the sandboxed checks.formatting it can host
# checks that need a real toolchain -- here, clippy.
{
  pkgs,
  lib,
  ...
}:
let
  # Bake the Rust toolchain into the check via runtimeInputs (hermetic: no runtime
  # `nix shell`, so the check never depends on the flake registry, network, or nix
  # being reachable from inside `conformist check`). gcc supplies the linker cargo
  # invokes.
  rustToolchain = with pkgs; [
    cargo
    clippy
    rustc
    gcc
  ];

  check = pkgs.writeShellApplication {
    name = "conformist-clippy";
    runtimeInputs = rustToolchain;
    text = ''
      # Whole-tree check (passes-files=false): cwd is the tree root, no file args.
      # The workspace root is the repo-root Cargo.toml; --workspace covers every
      # member crate.
      [ -f Cargo.toml ] || exit 0
      cargo clippy --manifest-path Cargo.toml --workspace --all-targets -- -D warnings
    '';
  };

  repair = pkgs.writeShellApplication {
    name = "conformist-clippy-repair";
    runtimeInputs = rustToolchain ++ [ pkgs.git ];
    text = ''
      [ -f Cargo.toml ] || exit 0
      # Partial autofix (conformist#54 repair lane / `nix fmt`): clippy --fix applies
      # only the machine-applicable suggestions. Findings with no mechanical rewrite
      # are left for a human and still fail the check. Self-stage the fixes.
      cargo clippy --fix --allow-dirty --allow-staged --all-targets --workspace \
        --manifest-path Cargo.toml
      # Stage only what clippy can have rewritten (sources and the lockfile), never
      # unrelated tracked edits the author has in flight.
      git add -u -- '*.rs' Cargo.lock
    '';
  };
in
{
  # No gomod2nix.toml here. The eng-impure preset enables the gomod2nix linter
  # unconditionally, and its module references pkgs.gomod2nix -- which is not in
  # nixpkgs (it ships via conformist's own overlay), so a plain consumer hits
  # `undefined variable 'gomod2nix'` when the impure config evaluates. Disable it.
  # mkForce overrides the preset's `enable = true`.
  linters.gomod2nix.enable = lib.mkForce false;

  # clippy as a custom impure linter (RFC 0001 §4: a linter name may be anything).
  # It compiles the crate, so it can only run in the working-tree lane, never the
  # sandboxed checks.formatting.
  settings.linter.clippy = {
    command = lib.getExe check;
    "repair-command" = lib.getExe repair;
    # Whole-tree check: the includes are a fire-trigger, not the input set (the
    # check reads the workspace via --manifest-path Cargo.toml).
    includes = [
      "**/*.rs"
      "**/Cargo.toml"
      "Cargo.toml"
    ];
    passes-files = false;
  };
}
