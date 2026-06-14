# Eval-only smoke test for the program (formatter) and linter registries.
#
# For every programs/<name>.nix and linters/<name>.nix, enable it in isolation
# and force its config to fully evaluate. Forcing evaluation catches schema
# breakage — every option type-check, every `config = mkIf cfg.enable` block,
# the package-attr resolution (`mkPackageOption pkgs <name>` forces pkgs.<name>
# to EXIST, though not to build), and the value serialization. So this catches
# unknown options, wrong types, missing-required-`includes`, and wrong package
# attr names, without building each underlying tool.
#
# It does NOT prove a tool's package builds or that its mainProgram/binary name
# is correct — those need a real build (see the dogfooded set in flake.nix and
# the representative complex ports verified separately).
#
# Why not just build mkConfigFile's conformist.toml? That derivation embeds
# `command = "${pkg}/bin/<prog>"`, and the Nix string context on that store path
# makes the generated toml depend on the tool. `nix flake check` would then
# realize EVERY formatter and linter in the registry just to emit TOML files,
# and a nixpkgs bump (which rotates every tool's hash) is a simultaneous cache
# miss on all ~130 checks. Instead we toJSON the evaluated settings (same deep
# eval, same package resolution) and unsafeDiscardStringContext the result, so
# the marker derivation carries no tool in its inputDrvs. The only thing dropped
# is the build-time remarshal JSON->TOML step, which always round-trips for
# these flat tables.
#
# Usage (see flake.nix): import ./nix/checks.nix { inherit pkgs; lib = conformistLib; }
{
  pkgs,
  lib,
}:
let
  # Force full module eval for one tool's config, then write a marker that does
  # NOT depend on the tool. toJSON forces config.settings deeply (including the
  # `command` string, which forces `cfg.package` = pkgs.<attr> to exist);
  # unsafeDiscardStringContext strips the tool store paths so building the marker
  # never realizes the tool. runCommandLocal keeps the marker off substituters —
  # it is a sub-millisecond local build with no value to cache.
  toSmokeCheck =
    kind: name: configuration:
    let
      mod = lib.evalModule pkgs configuration;
      rendered = builtins.unsafeDiscardStringContext (builtins.toJSON mod.config.settings);
    in
    pkgs.runCommandLocal "${kind}-${name}-smoke" { inherit rendered; } ''
      printf '%s' "$rendered" > "$out"
    '';

  toFormatterCheck =
    name:
    toSmokeCheck "formatter" name {
      # enableDefaultExcludes pulls in a large static list irrelevant to per-tool
      # schema validation; turn it off to keep each evaluated config lean.
      enableDefaultExcludes = false;
      programs.${name}.enable = true;
    };

  toLinterCheck =
    name:
    toSmokeCheck "linter" name {
      enableDefaultExcludes = false;
      linters.${name}.enable = true;
    };

  formatterConfigs = builtins.listToAttrs (
    map (name: {
      name = "formatter-${name}";
      value = toFormatterCheck name;
    }) lib.programs.names
  );

  linterConfigs = builtins.listToAttrs (
    map (name: {
      name = "linter-${name}";
      value = toLinterCheck name;
    }) lib.linters.names
  );
in
formatterConfigs // linterConfigs
