# conformist#10: a Go repo that gates on golangci-lint (has a .golangci.yml /
# .golangci.yaml) SHOULD ride the dewey static analyzers, which are a golangci-lint
# v2 module plugin. That requires a `.custom-gcl.yml` wiring the plugin so a custom
# golangci-lint binary can be built. This whole-tree check (passes-files=false)
# flags a golangci-gating repo that lacks such a .custom-gcl.yml (or whose
# .custom-gcl.yml doesn't reference the dewey plugin module).
#
# Scope note: this checks the *wiring* (the .custom-gcl.yml referencing the
# plugin), not that `dewey` is enabled in .golangci.yaml or actually runs —
# enabling + running need a custom-gcl binary, which is gated on a reusable Nix
# builder (coordinated with igloo). Tightening this rule to also require
# dewey-enablement is a follow-up once that builder lands. It reads only committed
# files (.golangci.*, .custom-gcl.yml), so it runs in the sandboxed
# checks.formatting derivation as well as `nix fmt`. See amarbel-llc/conformist#10
# and purse-first/libs/dewey/gclplugin.
{
  config,
  lib,
  pkgs,
  ...
}:
let
  cfg = config.linters.golangci-dewey;

  check = pkgs.writeShellApplication {
    name = "conformist-golangci-dewey";
    runtimeInputs = with pkgs; [
      coreutils
      gnugrep
    ];
    text = ''
      # cwd is the tree root (conformist runs whole-tree checks there); this
      # check takes no file arguments.
      if [ ! -f .golangci.yml ] && [ ! -f .golangci.yaml ]; then
        echo "golangci-dewey(#10): repo does not gate on golangci-lint; nothing to check"
        exit 0
      fi

      if [ ! -f .custom-gcl.yml ]; then
        echo "golangci-dewey(#10): repo gates on golangci-lint but has no .custom-gcl.yml wiring the dewey plugin (code.linenisgreat.com/purse-first/libs/dewey) — add one so a custom golangci-lint can be built" >&2
        exit 1
      fi

      if ! grep -qF 'code.linenisgreat.com/purse-first/libs/dewey' .custom-gcl.yml; then
        echo "golangci-dewey(#10): .custom-gcl.yml does not reference the dewey plugin module (code.linenisgreat.com/purse-first/libs/dewey)" >&2
        exit 1
      fi

      echo "golangci-dewey(#10): dewey plugin wired in .custom-gcl.yml"
    '';
  };
in
{
  options.linters.golangci-dewey = {
    enable = lib.mkEnableOption "the conformist#10 dewey-golangci-lint-plugin wiring whole-tree check";
  };

  config = lib.mkIf cfg.enable {
    settings.linter.golangci-dewey = {
      command = lib.getExe check;
      includes = [
        ".golangci.yml"
        ".golangci.yaml"
        ".custom-gcl.yml"
      ];
      passes-files = false;
    };
  };
}
