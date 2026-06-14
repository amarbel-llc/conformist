# conformist-nix(7) FLAKE OUTPUTS (conformist#9): a flake's `outputs` formal must
# name every declared input, or use a `...` catch-all (Nix passes all inputs to
# outputs; a closed formal that omits one is a hard eval error that only surfaces
# at eval time). Whole-tree check (passes-files=false): reads flake.nix +
# flake.lock (committed, so it runs in the sandboxed checks.formatting). Inputs
# come from flake.lock so no `nix` invocation is needed inside the sandbox. See
# conformist-nix(7) FLAKE OUTPUTS and amarbel-llc/conformist#9.
{
  config,
  lib,
  pkgs,
  ...
}:
let
  cfg = config.linters.flake-outputs;

  check = pkgs.writeShellApplication {
    name = "conformist-flake-outputs";
    runtimeInputs = with pkgs; [
      coreutils
      gnugrep
      gnused
      jq
    ];
    text = ''
      [ -f flake.nix ] || {
        echo "flake-outputs(#9): flake.nix missing at tree root" >&2
        exit 1
      }
      [ -f flake.lock ] || {
        echo "flake-outputs(#9): flake.lock missing; cannot resolve inputs" >&2
        exit 1
      }

      inputs=$(jq -r '.nodes.root.inputs | keys[]' flake.lock)
      [ -n "$inputs" ] || {
        echo "flake-outputs(#9): no inputs declared"
        exit 0
      }

      # Extract the `outputs = <formal>:` formal text (comments stripped, flattened).
      formal=$(sed 's/#.*$//' flake.nix | tr '\n' ' ' | grep -oP 'outputs\s*=\s*\K[^:]*:' | head -1)

      case "$formal" in
      *"..."*)
        echo "flake-outputs(#9): outputs formal uses ... (accepts all inputs)"
        exit 0
        ;;
      *"{"*) : ;;
      *)
        echo "flake-outputs(#9): outputs formal is a simple parameter (accepts all inputs)"
        exit 0
        ;;
      esac

      names=" $(printf '%s' "$formal" | grep -oE '[a-zA-Z_][a-zA-Z0-9_-]*' | tr '\n' ' ') "
      missing=""
      for i in $inputs; do
        case "$names" in
        *" $i "*) : ;;
        *) missing="$missing $i" ;;
        esac
      done

      if [ -n "$missing" ]; then
        echo "conformist-nix(7) FLAKE OUTPUTS (#9): outputs formal omits declared input(s):$missing — add them or use '...'" >&2
        exit 1
      fi
      echo "flake-outputs(#9): outputs formal names all declared inputs"
    '';
  };
in
{
  options.linters.flake-outputs = {
    enable = lib.mkEnableOption "the flake outputs-formal-accepts-all-inputs whole-tree check (conformist#9)";
  };

  config = lib.mkIf cfg.enable {
    settings.linter.flake-outputs = {
      command = lib.getExe check;
      includes = [ "flake.nix" ];
      passes-files = false;
    };
  };
}
