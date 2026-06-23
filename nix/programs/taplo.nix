{
  lib,
  pkgs,
  config,
  mkFormatterModule,
  mkTomlFormat,
  ...
}:
let
  inherit (lib) mkOption types;

  cfg = config.programs.taplo;
  configFormat = mkTomlFormat pkgs; # remarshal-free TOML (conformist#60)
  settingsSchema = mkOption {
    description = "Configuration to generate taplo.toml with";
    default = { };
    type = types.submodule { freeformType = configFormat.type; };
  };
  settingsFile =
    if cfg.settings != { } then
      configFormat.generate "taplo.toml" (
        cfg.settings
        // {
          include = cfg.includes;
          exclude = cfg.excludes;
        }
      )
    else
      null;
in
{
  meta.maintainers = [ ];

  imports = [
    (mkFormatterModule {
      name = "taplo";
      args = [ "format" ];
      includes = [ "*.toml" ];
    })
  ];

  options.programs.taplo = {
    settings = settingsSchema;
  };

  config = lib.mkIf cfg.enable {
    settings.formatter.taplo = {
      options = lib.optionals (settingsFile != null) [
        "--config"
        (toString settingsFile)
      ];
    };
  };
}
