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
      jq
      just
    ];
    text = ''
      [ -f justfile ] || {
        echo "justfile-default: justfile missing at tree root" >&2
        exit 1
      }

      # Read the parsed recipe model from just itself rather than sniffing raw
      # text. `.first` is just's own first-recipe-in-file; a recipe is an
      # aggregate iff its `.body` is empty. The earlier awk heuristic decided
      # "has a body" by checking whether the NEXT PHYSICAL LINE after a dep's
      # definition was indented, which misread a backslash-continued aggregate
      # (`foo: \` then an indented continuation line) as a body and
      # false-positived (conformist#51, reported by purse-first). just's parser
      # handles line continuations correctly — same plumbing as
      # justfile-task-hierarchy.
      #
      # jq emits one tagged finding per problem (FIRST:<name> for a wrong first
      # recipe, DEP:<name> for a non-aggregate dependency) or nothing when clean.
      # $root is a jq binding, not a shell var — keep it literal in the
      # single-quoted program.
      # shellcheck disable=SC2016
      filter='. as $root
        | if $root.first != "default" then
            "FIRST:\($root.first // "<none>")"
          else
            ($root.recipes.default.dependencies // [])
            | map(.recipe)[]
            | . as $dep
            | select(($root.recipes[$dep].body | length) > 0)
            | "DEP:\($dep)"
          end'

      # Capture (not process substitution) so a just/jq failure aborts loudly
      # rather than yielding an empty stream that reads as "no findings".
      if ! findings=$(just --dump --dump-format json | jq -r "$filter"); then
        echo "justfile-default: failed to read recipes via just/jq" >&2
        exit 2
      fi

      fail=0
      while read -r line; do
        case "$line" in
          "") continue ;;
          FIRST:*)
            name=''${line#FIRST:}
            echo "justfile-default: the first recipe must be 'default' (found: '$name') — eng-design_patterns-justfile(7)" >&2
            fail=1
            ;;
          DEP:*)
            name=''${line#DEP:}
            echo "justfile-default: 'default' lists leaf recipe '$name' (it has a body); default must contain only aggregate targets — eng-design_patterns-justfile(7)" >&2
            fail=1
            ;;
        esac
      done <<< "$findings"
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
