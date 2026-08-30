# eng-design_patterns-justfile(7) RECIPE DESCRIPTIONS + LIFECYCLE GROUPS: throwaway
# recipes in the `debug` / `explore` groups must carry a doc comment (the one-line
# description `just --list` shows), so they get a periodic look and link their
# dev-loop or tracking issue rather than rotting silently (conformist#23). The
# manpage defines the debug group as "diagnostic / throwaway recipes, often paired
# with one-off issue references in their comments" and explore as "one-off
# experiments". Whole-tree check (passes-files=false): reads the native recipe
# model via `just --dump --dump-format model`, takes no file arguments. See
# eng-design_patterns-justfile(7) and amarbel-llc/conformist#23.
#
# Bodyless (aggregate) recipes are exempt — conformist-justfile(7) AGGREGATES AND
# LEAVES + the justfile-aggregate-comments linter forbid an aggregate from
# carrying a doc comment at all, exactly as justfile-recipe-descriptions already
# scopes itself to leaves (conformist#96). Private recipes are NOT exempt here,
# matching this check's previous behavior.
#
# conformist#89: this check read only the root `.recipes` of the raw dump and so
# silently skipped every `mod`-imported recipe — the exact place throwaway
# debug/explore recipes tend to live (a `mod explore 'zz-explore/justfile'` block
# is the canonical eng shape). The model's `recipes` is one FLAT list spanning the
# root and all modules, so a module debug recipe is now checked. `groups` is a
# normalized model field, replacing the old dig through raw `attributes`.
{
  config,
  lib,
  pkgs,
  ...
}:
let
  cfg = config.linters.justfile-debug-recipes;
  model = import ../justfile-model.nix { inherit pkgs lib; };

  check = model.mkModelCheck {
    name = "justfile-debug-recipes";
    justPackage = cfg.justPackage;
    okMessage = "all debug/explore recipes carry a doc comment";
    filter = ''
      recipes
      | .[]
      | select(.groups | any(. == "debug" or . == "explore"))
      | select(isLeaf)
      | select((.doc // "") == "")
      | "'\(.namepath)' is a debug/explore recipe with no doc comment; add a one-line comment stating its dev-loop or linking a tracking issue (eng-design_patterns-justfile(7) RECIPE DESCRIPTIONS / LIFECYCLE GROUPS)"
    '';
  };
in
{
  options.linters.justfile-debug-recipes = {
    enable = lib.mkEnableOption "the debug/explore recipes-must-be-documented whole-tree check (eng-design_patterns-justfile(7), conformist#23)";
    justPackage = model.mkJustPackageOption "justfile-debug-recipes";
  };

  config = lib.mkIf cfg.enable {
    settings.linter.justfile-debug-recipes = {
      command = lib.getExe check;
      includes = [ "justfile" ];
      passes-files = false;
    };
  };
}
