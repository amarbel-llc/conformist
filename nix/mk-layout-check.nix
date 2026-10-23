# The dagnabit package-layout check for a godyn Go module (conformist#32).
#
# `mkLayoutCheck { app; dagnabit; root ? "internal"; }` returns a flake check
# that fails when a package under <root>/<level>/<leaf> sits at a level other
# than the one dagnabit computes from its imports. It is `app.passthru.
# codegenCheck` (igloo#80, a buildGoAuto/buildGodynModule result) running
# `dagnabit -n <root>` against the rendered module, where go.mod exists; a
# conformist linter would see only the raw tree, which a go.nix repo has no
# go.mod in.
#
# dagnabit is a PARAMETER, never an input of this flake: it ships from
# purse-first, which takes conformist as an input, so conformist depending on it
# would be a cycle. The consumer passes `purse-first.packages.${system}.dagnabit`.
#
# The check is a dry run (-n) that fails on any would-move line: applying a move
# needs `git mv`, and the rendered tree is not a git checkout (purse-first#196).
# It does not see a package with no import edges at all.
{
  app,
  dagnabit,
  root ? "internal",
}:
assert builtins.isString root && builtins.match "[A-Za-z0-9_./-]+" root != null;
app.passthru.codegenCheck {
  command = ''
    # Not `out`: that is the derivation's output path.
    moves=$(dagnabit -n ${root})
    if [ -n "$moves" ]; then
      echo "$moves"
      echo "FAIL: dagnabit would move packages (above); move them by hand to the level shown." >&2
      exit 1
    fi
  '';
  nativeBuildInputs = [ dagnabit ];
}
