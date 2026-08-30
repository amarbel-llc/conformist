# conformist-justfile(7) VERB LIST: every recipe follows a verb-noun pattern
# where the verb is one of the canonical verbs. Whole-tree check
# (passes-files=false): reads the native recipe model via
# `just --dump --dump-format model`, takes no file arguments.
# conformist-justfile(7) VERB LIST is the single source of truth for the verb set
# below (it absorbed eng-design_patterns-justfile(7)'s prose, eng#189) — keep this
# hardcoded mirror in sync with that page alone.
#
# Exceptions: `default` (the special first recipe) and the eng-versioning(7)
# release recipes `tag` / `release` (which are not verb-noun by convention).
# Private recipes are exempt: they never appear in `just --list`, and this check
# previously read `just --summary`, which lists only public recipes — the model
# carries private ones too, so the exemption is now explicit.
#
# conformist#85: the verb comes from the model's BARE `name`, which never carries
# a `mod::` qualifier (that lives in `module`/`namepath`). A `mod`-imported
# recipe's verb is therefore correct with no stripping step; the old code split
# the qualified path and produced verbs like `explore::debug`, failing every
# recipe in a module. Findings report the `namepath` so the recipe is locatable.
{
  config,
  lib,
  pkgs,
  ...
}:
let
  cfg = config.linters.justfile-recipe-names;
  model = import ../justfile-model.nix { inherit pkgs lib; };

  check = model.mkModelCheck {
    name = "justfile-recipe-names";
    justPackage = cfg.justPackage;
    okMessage = "all recipes follow verb-noun naming";
    filter = ''
      ["build","test","validate","verify","lint","run","list","codemod","install",
       "deploy","load","migrate","provision","restart","bump","update","clean",
       "debug","explore"] as $verbs
      | ["default","tag","release"] as $exceptions
      | recipes
      | public
      | .[]
      # Bind before testing membership: jq evaluates `index(f)`'s argument
      # against index's OWN input, so `$verbs | index(verb)` would run `verb`
      # against the $verbs array, not the recipe.
      | .name as $name
      | (verb) as $verb
      | select(($exceptions | index($name)) == null)
      | select(($verbs | index($verb)) == null)
      | "'\(.namepath)' does not start with a known verb (conformist-justfile(7) VERB LIST)"
    '';
  };
in
{
  options.linters.justfile-recipe-names = {
    enable = lib.mkEnableOption "the verb-noun recipe-naming whole-tree check (conformist-justfile(7))";
    justPackage = model.mkJustPackageOption "justfile-recipe-names";
  };

  config = lib.mkIf cfg.enable {
    settings.linter.justfile-recipe-names = {
      command = lib.getExe check;
      includes = [ "justfile" ];
      passes-files = false;
    };
  };
}
