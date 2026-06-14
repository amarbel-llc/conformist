# eng-design_patterns-justfile(7) RECIPE DESCRIPTIONS + LIFECYCLE GROUPS: throwaway
# recipes in the `debug` / `explore` groups must carry a doc comment (the one-line
# description `just --list` shows), so they get a periodic look and link their
# dev-loop or tracking issue rather than rotting silently (conformist#23). The
# manpage defines the debug group as "diagnostic / throwaway recipes, often paired
# with one-off issue references in their comments" and explore as "one-off
# experiments". Whole-tree check (passes-files=false): reads recipe metadata from
# `just --dump --dump-format json`, takes no file arguments. See
# eng-design_patterns-justfile(7) and amarbel-llc/conformist#23.
{
  config,
  lib,
  pkgs,
  ...
}:
let
  cfg = config.linters.justfile-debug-recipes;

  check = pkgs.writeShellApplication {
    name = "conformist-justfile-debug-recipes";
    runtimeInputs = with pkgs; [
      coreutils
      jq
      just
    ];
    text = ''
      [ -f justfile ] || {
        echo "justfile-debug-recipes: justfile missing at tree root" >&2
        exit 1
      }

      # Names of recipes in the debug/explore groups whose doc comment is empty.
      # `just` derives `doc` from the comment immediately above the recipe; a
      # group attribute serializes as {"group":"<name>"}.
      filter='.recipes | to_entries[]
        | select([.value.attributes[]? | .group?] | any(. == "debug" or . == "explore"))
        | select((.value.doc // "") == "")
        | .key'

      fail=0
      while read -r name; do
        [ -n "$name" ] || continue
        echo "justfile-debug-recipes: '$name' is a debug/explore recipe with no doc comment; add a one-line comment stating its dev-loop or linking a tracking issue (eng-design_patterns-justfile(7) RECIPE DESCRIPTIONS / LIFECYCLE GROUPS)" >&2
        fail=1
      done < <(just --dump --dump-format json | jq -r "$filter")

      if [ "$fail" -ne 0 ]; then
        exit 1
      fi
      echo "justfile-debug-recipes: all debug/explore recipes carry a doc comment"
    '';
  };
in
{
  options.linters.justfile-debug-recipes = {
    enable = lib.mkEnableOption "the debug/explore recipes-must-be-documented whole-tree check (eng-design_patterns-justfile(7), conformist#23)";
  };

  config = lib.mkIf cfg.enable {
    settings.linter.justfile-debug-recipes = {
      command = lib.getExe check;
      includes = [ "justfile" ];
      passes-files = false;
    };
  };
}
