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

  cfg = config.programs.sizelint;
  configFormat = mkTomlFormat pkgs; # remarshal-free TOML (conformist#60)
  settingsSchema = mkOption {
    description = "Configuration to generate sizelint.toml with";
    default = { };
    type = types.submodule { freeformType = configFormat.type; };
  };
  settingsFile =
    if cfg.settings != { } then configFormat.generate "sizelint.toml" (cfg.settings) else null;
in
{
  meta.maintainers = [ "a-kenji" ];

  imports = [
    (mkFormatterModule {
      name = "sizelint";
      args = [ "check" ];
      includes = [ "*" ];
    })
  ];

  options.programs.sizelint = {
    settings = settingsSchema;
    failOnWarn = lib.mkEnableOption "fail-on-warn";
  };

  config = lib.mkIf cfg.enable {
    settings.formatter.sizelint = {
      options =
        lib.optionals (settingsFile != null) [
          "--config"
          (toString settingsFile)
        ]
        ++ lib.optional cfg.failOnWarn "--fail-on-warn";
    };
  };
}
