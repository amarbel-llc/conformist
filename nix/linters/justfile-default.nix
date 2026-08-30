# conformist-justfile(7) AGGREGATES AND LEAVES: `default` must be the FIRST
# recipe, and it must contain only aggregate targets (recipes with no body of
# their own) — never leaves directly. Whole-tree check (passes-files=false):
# reads the native recipe model via `just --dump --dump-format model`, takes no
# file arguments. Prose origin: eng-design_patterns-justfile(7) DEFAULT RECIPE.
#
# WHY `root_default` IS THE RIGHT FIELD FOR "the first recipe". just resolves a
# justfile's default as: the `[default]`-attributed recipe if one exists, else the
# lowest-line-number recipe defined in the ROOT justfile (src/analyzer.rs). The
# raw dump's `.first` — what this check read before — is that same
# `Justfile::default` under a `serde(rename)`, so reading `root_default` is a
# behavior-preserving swap, not a new rule. It is also why this check does NOT
# reconstruct "first" from `line`/`source`: doing so would silently start
# disagreeing with just about which recipe bare `just` actually runs.
#
# SCOPE: the ROOT default only. The model's per-recipe `is_default` is PER-SCOPE
# (a module's own first recipe reads true), and `root_default` is the unambiguous
# root one. Requiring every module to have a `default` would be new enforcement,
# so this deliberately checks the root and leaves module scopes alone.
#
# The earlier awk heuristic (pre-#51) decided "has a body" by checking whether the
# NEXT PHYSICAL LINE after a dependency's definition was indented, which misread a
# backslash-continued aggregate (`foo: \` then an indented continuation line) as a
# body and false-positived (conformist#51, reported by purse-first). just's parser
# handles line continuations correctly, and `has_body` is that parser's own
# answer.
#
# conformist#89 / #85: dependency targets are now resolved by the model's
# `dependencies[].namepath` and looked up in the FLAT recipe list, so `default`
# depending on a `mod`-imported aggregate resolves correctly. The raw dump dropped
# the `mod::` qualifier from dependencies and this check only indexed the ROOT
# recipe table, so such a dependency silently found nothing and was never checked.
{
  config,
  lib,
  pkgs,
  ...
}:
let
  cfg = config.linters.justfile-default;
  model = import ../justfile-model.nix { inherit pkgs lib; };

  check = model.mkModelCheck {
    name = "justfile-default";
    justPackage = cfg.justPackage;
    okMessage = "'default' is the first recipe and lists only aggregates";
    filter = ''
      recipes as $all
      | ($all | byNamepath) as $by
      | (model | .root_default) as $rd
      | if ($rd // "") != "default" then
          "the first recipe must be 'default' (found: '\($rd // "<none>")') — eng-design_patterns-justfile(7)"
        else
          ($by["default"].dependencies // [])[]
          | .namepath as $dep
          | ($by[$dep] // null) as $target
          | select($target != null and ($target | isLeaf))
          | "'default' lists leaf recipe '\($dep)' (it has a body); default must contain only aggregate targets — eng-design_patterns-justfile(7)"
        end
    '';
  };
in
{
  options.linters.justfile-default = {
    enable = lib.mkEnableOption "the 'default is first + aggregates-only' whole-tree check (eng-design_patterns-justfile(7))";
    justPackage = model.mkJustPackageOption "justfile-default";
  };

  config = lib.mkIf cfg.enable {
    settings.linter.justfile-default = {
      command = lib.getExe check;
      includes = [ "justfile" ];
      passes-files = false;
    };
  };
}
