{
  config,
  lib,
  mkFormatterModule,
  ...
}:
{
  meta.maintainers = [ ];

  imports = [
    (mkFormatterModule {
      name = "clang-format";
      package = "clang-tools";
      mainProgram = "clang-format";
      args = [ "-i" ];
      includes = [
        "*.c"
        "*.cc"
        "*.cpp"
        "*.h"
        "*.hh"
        "*.hpp"
        "*.glsl"
        "*.vert"
        ".tesc"
        ".tese"
        ".geom"
        ".frag"
        ".comp"
      ];
    })
  ];

  config = lib.mkIf config.programs.clang-format.enable {
    # clang-format has no read-only mode, so check mode formats a sandbox copy,
    # which carries only the files a formatter declares here. clang-format finds
    # its style file by walking up from each formatted file; without it the
    # sandbox falls back to the built-in LLVM style and `check` reports every
    # file of a non-LLVM repo as "would reformat" while repair says it is clean
    # (conformist#49). `_clang-format` is the Windows spelling it also honours.
    settings.formatter.clang-format.config-files = [
      ".clang-format"
      "_clang-format"
    ];
  };
}
