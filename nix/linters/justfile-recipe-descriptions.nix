# conformist-justfile(7) RECIPE DESCRIPTIONS: every leaf recipe carries a
# doc comment — the single comment line immediately above it, which `just --list`
# shows as the recipe's description. Whole-tree check (passes-files=false): reads
# the native recipe model via `just --dump --dump-format model`, takes no file
# arguments. Prose origin: eng-design_patterns-justfile(7) RECIPE DESCRIPTIONS.
#
# Scope: LEAF recipes only (those with a body). Aggregate recipes (no body, only
# dependencies) are self-documenting via their dependency list and are exempt.
# debug/explore recipes are excluded here because justfile-debug-recipes (#23)
# already requires their doc comment with a throwaway-specific message, so this
# rule would otherwise double-report them. Private recipes (underscore-prefixed or
# [private]) don't appear in `just --list` and are exempt too.
#
# conformist#89: this check read only the root `.recipes` of the raw dump and so
# silently skipped every `mod`-imported recipe. The model's `recipes` is one FLAT
# list spanning the root and all modules, so a module leaf is now checked like
# any other. `groups` is a normalized model field, replacing the old dig through
# raw `attributes` for `{"group": "..."}` entries.
#
# See eng-design_patterns-justfile(7) and amarbel-llc/conformist#17.
{
  config,
  lib,
  pkgs,
  ...
}:
let
  cfg = config.linters.justfile-recipe-descriptions;
  model = import ../justfile-model.nix { inherit pkgs lib; };

  check = model.mkModelCheck {
    name = "justfile-recipe-descriptions";
    justPackage = cfg.justPackage;
    okMessage = "all leaf recipes carry a doc comment";
    filter = ''
      recipes
      | public
      | .[]
      | select(isLeaf)
      | select((.groups | any(. == "debug" or . == "explore")) | not)
      | select((.doc // "") == "")
      | "leaf recipe '\(.namepath)' has no doc comment; add a one-line summary immediately above it (eng-design_patterns-justfile(7) RECIPE DESCRIPTIONS)"
    '';
  };
in
{
  options.linters.justfile-recipe-descriptions = {
    enable = lib.mkEnableOption "the leaf-recipes-must-be-documented whole-tree check (eng-design_patterns-justfile(7), conformist#17)";
    justPackage = model.mkJustPackageOption "justfile-recipe-descriptions";
  };

  config = lib.mkIf cfg.enable {
    settings.linter.justfile-recipe-descriptions = {
      command = lib.getExe check;
      includes = [ "justfile" ];
      passes-files = false;
    };
  };
}
