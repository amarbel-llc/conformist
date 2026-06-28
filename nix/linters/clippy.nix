# clippy as a first-class conformist linter (conformist#69).
#
# Every Rust repo otherwise re-derives the same boilerplate via the generic
# custom-linter hook (RFC 0001 §4); this ships it once. The read-only `command`
# runs `cargo clippy ... -- -D warnings` (fails on any lint); the
# `repair-command` (repair mode / `nix fmt`) runs `cargo clippy --fix`, applying
# the machine-applicable suggestions. Findings with no mechanical rewrite are
# left for a human and still fail the check.
#
# CONSUMER-OWNED TOOLCHAIN: conformist pins NO Rust. The `packages` default is
# cargo/clippy/rustc/gcc resolved from the SAME nixpkgs the consumer passes to
# `conformist.lib.evalModule <pkgs> …` (so the Rust version tracks the
# consumer's nixpkgs), and a consumer on a pinned toolchain (rust-overlay,
# fenix, a rust-toolchain.toml channel) overrides `packages` with their exact
# derivations. The toolchain is baked into the check via `runtimeInputs` —
# hermetic, no runtime `nix shell`, so the check never depends on the flake
# registry or network being reachable from inside `conformist check`.
#
# IMPURE: clippy COMPILES the crate, so it can only run in the working-tree lane
# (`conformist-impure` / `just lint-worktree`), never the sandboxed
# checks.formatting (a read-only /nix/store copy of tracked files). For that
# reason it is OPT-IN: it is a registered module (so `linters.clippy.enable =
# true` works) but is NOT in the eng-impure preset roster — auto-enabling it
# would force conformist's own (Rust-free) lint lane to build a Rust toolchain
# for a no-op, and would couple the preset to a pinned Rust. A consumer enables
# it explicitly in their impure overlay. Whole-tree check (passes-files=false):
# the includes are a fire-trigger, not the input set (the check reads the crate
# via --manifest-path).
{
  config,
  lib,
  pkgs,
  ...
}:
let
  cfg = config.linters.clippy;

  manifestArg = "--manifest-path ${lib.escapeShellArg cfg.manifest-path}";
  workspaceFlag = lib.optionalString cfg.workspace " --workspace";
  allTargetsFlag = lib.optionalString cfg.all-targets " --all-targets";
  extraArgs = lib.optionalString (cfg.extra-args != [ ]) " ${lib.escapeShellArgs cfg.extra-args}";

  denyArgs = map (l: "-D ${lib.escapeShellArg l}") cfg.deny;
  allowArgs = map (l: "-A ${lib.escapeShellArg l}") cfg.allow;
  # clippy applies trailing lint flags left-to-right, so deny first then allow
  # lets an `allow` entry override a broad `deny` group.
  lintFlags = lib.concatStringsSep " " (denyArgs ++ allowArgs);
  trailingLints = lib.optionalString (lintFlags != "") " -- ${lintFlags}";

  check = pkgs.writeShellApplication {
    name = "conformist-clippy";
    runtimeInputs = cfg.packages;
    text = ''
      # Whole-tree check (passes-files=false): cwd is the tree root, no file args.
      # Self-gate on the manifest so the check no-ops in a non-Rust repo.
      [ -f ${lib.escapeShellArg cfg.manifest-path} ] || exit 0
      cargo clippy ${manifestArg}${workspaceFlag}${allTargetsFlag}${extraArgs}${trailingLints}
    '';
  };

  repair = pkgs.writeShellApplication {
    name = "conformist-clippy-repair";
    runtimeInputs = cfg.packages;
    text = ''
      [ -f ${lib.escapeShellArg cfg.manifest-path} ] || exit 0
      # `clippy --fix` applies only the machine-applicable suggestions; the rest
      # are left for a human and still fail the check. No trailing `-D` so an
      # unfixable remainder does not abort the repair. The fixes land in the
      # working tree; restage-repair-outputs (below) stages those tracked files
      # under the --staged / --commit hooks, so no `git add` here.
      cargo clippy --fix --allow-dirty --allow-staged${workspaceFlag}${allTargetsFlag} ${manifestArg}${extraArgs}
    '';
  };
in
{
  options.linters.clippy = {
    enable = lib.mkEnableOption "the clippy whole-tree Rust lint (impure: compiles the crate; working-tree lane only — conformist#69)";

    packages = lib.mkOption {
      type = lib.types.listOf lib.types.package;
      default = with pkgs; [
        cargo
        clippy
        rustc
        gcc
      ];
      defaultText = lib.literalExpression "with pkgs; [ cargo clippy rustc gcc ]";
      description = ''
        The Rust toolchain placed on PATH for the check and repair: cargo,
        clippy, rustc, and gcc (the linker cargo invokes for `--all-targets`).
        Defaults to those attributes from the SAME nixpkgs the consumer passes
        to `conformist.lib.evalModule <pkgs> …`, so clippy tracks the consumer's
        Rust version and conformist pins nothing. Override with a pinned
        toolchain (e.g. from rust-overlay or fenix) to control the exact Rust
        version.
      '';
    };

    manifest-path = lib.mkOption {
      type = lib.types.str;
      default = "Cargo.toml";
      example = "v2-rust/Cargo.toml";
      description = ''
        Path (relative to the tree root) of the Cargo manifest cargo clippy runs
        against. Its presence also gates the check, so a non-Rust tree no-ops.
      '';
    };

    workspace = lib.mkOption {
      type = lib.types.bool;
      default = true;
      description = "Pass --workspace so clippy covers every workspace member, not just the root crate.";
    };

    all-targets = lib.mkOption {
      type = lib.types.bool;
      default = true;
      description = "Pass --all-targets so clippy covers tests, benches, and examples in addition to the library/binary.";
    };

    extra-args = lib.mkOption {
      type = lib.types.listOf lib.types.str;
      default = [ ];
      example = [ "--no-default-features" ];
      description = "Extra arguments passed to `cargo clippy` before the `--` lint separator (e.g. feature flags), in both check and repair.";
    };

    deny = lib.mkOption {
      type = lib.types.listOf lib.types.str;
      default = [ "warnings" ];
      description = ''
        Lints (or lint groups) passed as `-D <lint>` after `--`, failing the
        check on any occurrence. The default `warnings` turns every clippy
        warning into an error.
      '';
    };

    allow = lib.mkOption {
      type = lib.types.listOf lib.types.str;
      default = [ ];
      example = [ "clippy::too_many_arguments" ];
      description = "Lints passed as `-A <lint>` after `--`, suppressing them. Applied after `deny`, so an `allow` entry overrides a broad `deny` group.";
    };

    includes = lib.mkOption {
      type = lib.types.listOf lib.types.str;
      default = [
        "**/*.rs"
        "Cargo.toml"
      ];
      description = ''
        Fire-trigger globs: the whole-tree check runs when any matched file has
        changed. Not the input set — the check reads the crate via
        --manifest-path.
      '';
    };
  };

  config = lib.mkIf cfg.enable {
    settings.linter.clippy = {
      command = lib.getExe check;
      "repair-command" = lib.getExe repair;
      inherit (cfg) includes;
      passes-files = false;
      # clippy --fix rewrites tracked .rs files; have the --staged/--commit hook
      # restage exactly the files the repair changed (conformist#55).
      "restage-repair-outputs" = true;
    };
  };
}
