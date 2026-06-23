{
  lib,
  pkgs,
  config,
  mkFormatterModule,
  mkTomlFormat,
  ...
}:
let
  cfg = config.programs.flake-edit;
  configFormat = mkTomlFormat pkgs; # remarshal-free TOML (conformist#60)

  settingsFile =
    let
      settings = lib.filterAttrsRecursive (_n: v: v != null && v != [ ] && v != { }) cfg.settings;
    in
    if settings != { } then configFormat.generate "flake-edit.toml" settings else null;
in
{
  meta = {
    maintainers = [ "a-kenji" ];
  };

  imports = [
    (mkFormatterModule {
      name = "flake-edit";
      args = [
        "--non-interactive"
      ];
      includes = [ "flake.nix" ];
    })
  ];

  options.programs.flake-edit = {
    settings = {
      follow = {
        ignore = lib.mkOption {
          description = ''
            List of inputs to ignore when following.
            Supports simple names ("systems") or full paths ("crane.nixpkgs").
          '';
          type = lib.types.listOf lib.types.str;
          default = [ ];
          example = [
            "systems"
            "crane.flake-utils"
          ];
        };

        aliases = lib.mkOption {
          description = ''
            Mapping of canonical input names to alternative names.
            Allows nested inputs like "nixpkgs-lib" to follow "nixpkgs".
          '';
          type = lib.types.attrsOf (lib.types.listOf lib.types.str);
          default = { };
          example = {
            nixpkgs = [ "nixpkgs-lib" ];
          };
        };
      };
    };

    noLock = lib.mkOption {
      description = "Skip updating the lockfile after editing flake.nix";
      type = lib.types.bool;
      default = false;
    };
  };

  config = lib.mkIf cfg.enable {
    settings.formatter.flake-edit = {
      options =
        (lib.optionals (settingsFile != null) [
          "--config"
          (toString settingsFile)
        ])
        ++ (lib.optional cfg.noLock "--no-lock")
        ++ [ "follow" ];
    };
  };
}
