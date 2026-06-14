{
  lib,
  config,
  mkFormatterModule,
  ...
}:
let
  cfg = config.programs.rustfmt;
in
{
  meta.maintainers = [ ];

  imports = [
    (mkFormatterModule {
      name = "rustfmt";
      mainProgram = "rustfmt";
      args = [
        "--config"
        "skip_children=true"
      ];
      includes = [ "*.rs" ];
    })
  ];

  options.programs.rustfmt = {
    edition = lib.mkOption {
      type = lib.types.str;
      default = "2024";
      description = ''
        Rust edition to target when formatting
      '';
    };
  };

  config = lib.mkIf cfg.enable {
    settings.formatter.rustfmt = {
      options = [
        "--edition"
        cfg.edition
      ];
      # rustfmt discovers rustfmt.toml/.editorconfig by walking upward from each
      # formatted file. Ship them into the sandbox so check mode agrees with
      # repair mode instead of formatting at rustfmt's defaults (conformist#28).
      config-files = [
        "rustfmt.toml"
        ".rustfmt.toml"
        ".editorconfig"
      ];
    };
  };
}
