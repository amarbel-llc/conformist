# statix as a conformist LINTER (RFC 0001 §4). The check action is
# `statix check` (reports Nix antipatterns, exits non-zero on findings); the
# repair action is `statix fix` (applies them in repair mode). treefmt-nix
# shipped this as a "formatter" that always ran `statix fix` via a per-file bash
# loop; conformist splits check from fix and relies on no-positional-arg-support
# for statix's one-file-at-a-time limitation instead of a hand-rolled loop
# (conformist#6).
{
  lib,
  pkgs,
  config,
  mkLinterModule,
  mkTomlFormat,
  ...
}:
let
  cfg = config.linters.statix;
  configFormat = mkTomlFormat pkgs; # remarshal-free TOML (conformist#60)
  settingsFile = configFormat.generate "statix.toml" { disabled = cfg.disabled-lints; };

  # statix requires its configuration file to be named statix.toml exactly.
  # See: https://github.com/nerdypepper/statix/pull/54
  settingsDir = pkgs.runCommandLocal "statix-config" { } ''
    mkdir "$out"
    cp ${settingsFile} "''${out}/statix.toml"
  '';
  configArgs = [
    "--config"
    "${toString settingsDir}/statix.toml"
  ];
in
{
  meta.maintainers = [ ];

  imports = [
    (mkLinterModule {
      name = "statix";
      args = [ "check" ];
      repairArgs = [ "fix" ];
      includes = [ "*.nix" ];
    })
  ];

  options.linters.statix = {
    disabled-lints = lib.mkOption {
      description = ''
        List of ignored lints. Run `statix list` to see all available lints.
      '';
      type = with lib.types; listOf str;
      example = [ "empty_pattern" ];
      default = [ ];
    };
  };

  config = lib.mkIf cfg.enable {
    settings.linter.statix = {
      # statix processes a single file target at a time.
      no-positional-arg-support = true;
      options = lib.mkAfter configArgs;
      "repair-options" = lib.mkAfter configArgs;
    };
  };
}
