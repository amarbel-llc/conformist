# conformist-justfile(7) VERB LIST: every recipe follows a verb-noun pattern
# where the verb is one of the canonical verbs. Whole-tree check
# (passes-files=false): reads recipe names via `just --summary`, takes no file
# arguments. conformist-justfile(7) VERB LIST is the source of truth for the verb
# set below; it mirrors eng-design_patterns-justfile(7) VERB CATEGORIES (the prose
# origin) — keep the two in sync.
#
# Exceptions: `default` (the special first recipe) and the eng-versioning(7)
# release recipes `tag` / `release` (which are not verb-noun by convention).
{
  config,
  lib,
  pkgs,
  ...
}:
let
  cfg = config.linters.justfile-recipe-names;

  check = pkgs.writeShellApplication {
    name = "conformist-justfile-recipe-names";
    runtimeInputs = with pkgs; [
      coreutils
      just
    ];
    text = ''
      [ -f justfile ] || {
        echo "justfile-recipe-names: justfile missing at tree root" >&2
        exit 1
      }

      verbs=" build test validate verify lint run list codemod install deploy load bump update clean debug explore "
      exceptions=" default tag release "
      fail=0

      while read -r name; do
        [ -n "$name" ] || continue
        # A `mod`-imported child justfile's recipes are listed fully qualified
        # (explore::debug-foo, nested a::b::c). Strip every module qualifier
        # before verb extraction — the naming rule applies to the recipe's own
        # name, not its module path (conformist#85).
        bare=''${name##*::}
        case "$exceptions" in
        *" $bare "*) continue ;;
        esac
        first=''${bare%%-*}
        case "$verbs" in
        *" $first "*) continue ;;
        esac
        echo "justfile-recipe-names: '$name' does not start with a known verb (eng-design_patterns-justfile(7))" >&2
        fail=1
      done < <(just --summary | tr ' ' '\n')

      if [ "$fail" -ne 0 ]; then
        exit 1
      fi
      echo "justfile-recipe-names: all recipes follow verb-noun naming"
    '';
  };
in
{
  options.linters.justfile-recipe-names = {
    enable = lib.mkEnableOption "the verb-noun recipe-naming whole-tree check (eng-design_patterns-justfile(7))";
  };

  config = lib.mkIf cfg.enable {
    settings.linter.justfile-recipe-names = {
      command = lib.getExe check;
      includes = [ "justfile" ];
      passes-files = false;
    };
  };
}
