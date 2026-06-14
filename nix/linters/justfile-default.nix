# conformist-justfile(7) AGGREGATES AND LEAVES: `default` must be the FIRST
# recipe, and it must contain only aggregate targets (recipes with no body of
# their own) — never leaves directly. Whole-tree check (passes-files=false):
# reads the justfile, takes no file arguments. Prose origin:
# eng-design_patterns-justfile(7) DEFAULT RECIPE.
{
  config,
  lib,
  pkgs,
  ...
}:
let
  cfg = config.linters.justfile-default;

  check = pkgs.writeShellApplication {
    name = "conformist-justfile-default";
    runtimeInputs = with pkgs; [
      coreutils
      gawk
    ];
    text = ''
      [ -f justfile ] || {
        echo "justfile-default: justfile missing at tree root" >&2
        exit 1
      }

      # The first recipe = first column-0 line that is a recipe (not a comment,
      # blank, [attribute], or `name :=` assignment) and contains a `:`.
      first=$(awk '
        /^[[:space:]]*#/ { next }
        /^[[:space:]]*$/ { next }
        /^\[/ { next }
        /^[A-Za-z_]/ {
          if ($0 ~ /:=/) next
          if ($0 !~ /:/) next
          n = $0
          sub(/[[:space:]:].*/, "", n)
          print n
          exit
        }
      ' justfile)

      if [ "$first" != "default" ]; then
        echo "justfile-default: the first recipe must be 'default' (found: '$first') — eng-design_patterns-justfile(7)" >&2
        exit 1
      fi

      # `default` must list only aggregate targets: each dependency recipe must
      # have no body of its own (the line after its definition is not indented).
      deps=$(awk '/^default[[:space:]]*:/ { sub(/^[^:]*:[[:space:]]*/, ""); sub(/#.*/, ""); print; exit }' justfile)
      fail=0
      for dep in $deps; do
        body=$(awk -v r="$dep" '
          !found && $0 ~ ("^" r "([[:space:]].*)?:") { found = 1; next }
          found { if ($0 ~ /^[[:space:]]/ && $0 !~ /^[[:space:]]*$/) print "body"; exit }
        ' justfile)
        if [ -n "$body" ]; then
          echo "justfile-default: 'default' lists leaf recipe '$dep' (it has a body); default must contain only aggregate targets — eng-design_patterns-justfile(7)" >&2
          fail=1
        fi
      done
      [ "$fail" -eq 0 ] || exit 1

      echo "justfile-default: 'default' is the first recipe and lists only aggregates"
    '';
  };
in
{
  options.linters.justfile-default = {
    enable = lib.mkEnableOption "the 'default is first + aggregates-only' whole-tree check (eng-design_patterns-justfile(7))";
  };

  config = lib.mkIf cfg.enable {
    settings.linter.justfile-default = {
      command = lib.getExe check;
      includes = [ "justfile" ];
      passes-files = false;
    };
  };
}
