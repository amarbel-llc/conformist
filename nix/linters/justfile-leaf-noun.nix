# conformist-justfile(7) AGGREGATES AND LEAVES: a leaf recipe (one with a body)
# must be verb-noun — its name must NOT be a bare verb (`test-go`, not `test`),
# because a bare verb names an aggregate (eng-design_patterns-justfile(7)
# ANTI-PATTERNS, "redundant nouns"). Whole-tree check (passes-files=false): reads
# the native recipe model via `just --dump --dump-format model`, takes no file
# arguments.
#
# Exempt: the verb-noun-exempt release recipes `tag` / `release` (single-segment
# by convention, like justfile-recipe-names) and private recipes.
#
# conformist#89: this check read only the root `.recipes` of the raw dump and so
# silently skipped every `mod`-imported recipe. The model's `recipes` is one FLAT
# list spanning the root and all modules, so a bare-verb leaf hiding in a module
# is now flagged. The noun test runs on the model's BARE `name`, never the
# qualified `namepath` (conformist#85) — testing the namepath would see the `::`
# separator's segments and let a genuinely bare-verb module recipe pass.
#
# See conformist-justfile(7) AGGREGATES AND LEAVES and amarbel-llc/conformist#17.
{
  config,
  lib,
  pkgs,
  ...
}:
let
  cfg = config.linters.justfile-leaf-noun;
  model = import ../justfile-model.nix { inherit pkgs lib; };

  check = model.mkModelCheck {
    name = "justfile-leaf-noun";
    justPackage = cfg.justPackage;
    okMessage = "every leaf recipe is verb-noun";
    filter = ''
      recipes
      | public
      | .[]
      | select(isLeaf)
      | select(.name != "tag" and .name != "release")
      | select((.name | test("-")) | not)
      | "leaf recipe '\(.namepath)' is a bare verb; a leaf must be verb-noun (e.g. 'test-go', not 'test') — a bare verb names an aggregate (conformist-justfile(7) AGGREGATES AND LEAVES)"
    '';
  };
in
{
  options.linters.justfile-leaf-noun = {
    enable = lib.mkEnableOption "the leaf-recipes-must-have-a-noun whole-tree check (conformist-justfile(7), conformist#17)";
    justPackage = model.mkJustPackageOption "justfile-leaf-noun";
  };

  config = lib.mkIf cfg.enable {
    settings.linter.justfile-leaf-noun = {
      command = lib.getExe check;
      includes = [ "justfile" ];
      passes-files = false;
    };
  };
}
