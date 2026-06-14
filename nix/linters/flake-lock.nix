# conformist-nix(7) FLAKE HYGIENE (conformist#11): a flake's flake.lock must be
# committed so a checkout reproducibly resolves the same input closure. Whole-tree
# check (passes-files=false): if flake.nix is present at the tree root, flake.lock
# must be too. Reads only committed files, so it runs in the sandboxed
# checks.formatting derivation as well as `nix fmt`. See conformist-nix(7) FLAKE
# HYGIENE and amarbel-llc/conformist#11.
{
  config,
  lib,
  pkgs,
  ...
}:
let
  cfg = config.linters.flake-lock;

  check = pkgs.writeShellApplication {
    name = "conformist-flake-lock";
    runtimeInputs = with pkgs; [
      coreutils
    ];
    text = ''
      # cwd is the tree root; the includes gate runs this only when flake.nix
      # matched, but re-check so a direct invocation is a clean no-op too.
      [ -f flake.nix ] || {
        echo "flake-lock: no flake.nix at tree root; nothing to check"
        exit 0
      }

      if [ ! -f flake.lock ]; then
        echo "conformist-nix(7) FLAKE HYGIENE: flake.lock missing; commit it to pin inputs reproducibly" >&2
        exit 1
      fi

      echo "conformist-nix(7) FLAKE HYGIENE: flake.lock is committed"
    '';
  };
in
{
  options.linters.flake-lock = {
    enable = lib.mkEnableOption "the committed-flake.lock whole-tree check (conformist-nix(7) FLAKE HYGIENE, conformist#11)";
  };

  config = lib.mkIf cfg.enable {
    settings.linter.flake-lock = {
      command = lib.getExe check;
      includes = [ "flake.nix" ];
      passes-files = false;
    };
  };
}
