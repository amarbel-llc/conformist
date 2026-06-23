# yamllint as a conformist LINTER (RFC 0001 §4). It reports YAML style/syntax
# problems and exits non-zero on findings; yamllint has no autofix, so it is
# check-only (a no-op in repair mode). Reclassified from a treefmt-nix
# "formatter" (conformist#6).
{
  lib,
  pkgs,
  config,
  mkLinterModule,
  mkYamlFormat,
  ...
}:
let
  cfg = config.linters.yamllint;
  settingsFormat = mkYamlFormat pkgs; # remarshal-free YAML (conformist#60)
in
{
  meta.maintainers = [
    "DigitalBrewStudios/Treefmt-nix"
  ];

  imports = [
    (mkLinterModule {
      name = "yamllint";
      includes = [
        "*.yaml"
        "*.yml"
      ];
    })
  ];

  options.linters.yamllint = {
    settings = lib.mkOption {
      type = lib.types.submodule { freeformType = settingsFormat.type; };
      default = { };
      description = ''
        Configuration for yamllint, see
        <link xlink:href="https://yamllint.readthedocs.io/en/stable/configuration.html"/>
        for supported values.
      '';
    };
  };

  config = lib.mkIf cfg.enable {
    settings.linter.yamllint = lib.mkIf (cfg.settings != { }) {
      options = lib.mkAfter [ "-c=${settingsFormat.generate "yamllint.yaml" cfg.settings}" ];
    };
  };
}
