# conformist-justfile(7) AGGREGATES AND LEAVES: an aggregate recipe (no body,
# only dependencies) must NOT carry a doc comment — its dependency list is
# self-documenting (eng-design_patterns-justfile(7) ANTI-PATTERNS, "comments on
# aggregates"). The inverse of justfile-recipe-descriptions, which requires LEAF
# recipes to be documented. Whole-tree check (passes-files=false): reads the
# native recipe model via `just --dump --dump-format model`, takes no file
# arguments.
#
# conformist#89: this check read only the root `.recipes` of the raw dump and so
# silently skipped every `mod`-imported recipe. The model's `recipes` is one FLAT
# list spanning the root and all modules, so a commented aggregate inside a module
# is now flagged.
#
# See conformist-justfile(7) AGGREGATES AND LEAVES and amarbel-llc/conformist#17.
{
  config,
  lib,
  pkgs,
  ...
}:
let
  cfg = config.linters.justfile-aggregate-comments;
  model = import ../justfile-model.nix { inherit pkgs lib; };

  check = model.mkModelCheck {
    name = "justfile-aggregate-comments";
    justPackage = cfg.justPackage;
    okMessage = "no aggregate recipe carries a doc comment";
    filter = ''
      recipes
      | .[]
      | select(isAggregate)
      | select((.doc // "") != "")
      | "aggregate recipe '\(.namepath)' has a doc comment; aggregates are self-documenting via their dependency list — drop the comment (conformist-justfile(7) AGGREGATES AND LEAVES)"
    '';
  };
in
{
  options.linters.justfile-aggregate-comments = {
    enable = lib.mkEnableOption "the aggregates-must-not-be-commented whole-tree check (conformist-justfile(7), conformist#17)";
    justPackage = model.mkJustPackageOption "justfile-aggregate-comments";
  };

  config = lib.mkIf cfg.enable {
    settings.linter.justfile-aggregate-comments = {
      command = lib.getExe check;
      includes = [ "justfile" ];
      passes-files = false;
    };
  };
}
