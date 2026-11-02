# The rendered-page manpage NAME-line check (conformist#114).
#
# `mkManpageNamesCheck pkgs { manpages; max ? 72; doppelgang ? <released binary>; }`
# returns a flake check that runs `doppelgang lint-man --max <max>` over
# `${manpages}/share/man`, the BUILT pages. The profile's `manpage-names` linter
# sees only `.scd` sources; a repo whose pages are generated (Go man-page
# codegen: a CLI reference, one page per MCP tool) has no source file for the
# long descriptions, and a profile cannot depend on a build output, so this
# check is the other half of the same contract (spinclass FDR 0030: a NAME line
# is `name - description`, non-empty, one physical line, at most 72 characters).
#
# doppelgang defaults to the released static binary, fetched as a
# fixed-output derivation, so conformist keeps no flake edge to doppelgang (the
# same reason mkLayoutCheck takes dagnabit as a parameter). The pin is the same
# sha256 conformist.profile pins for x86_64-linux; bump both together. A consumer
# on another system passes `doppelgang` itself (no aarch64-linux asset exists).
#
# `passthru.script` is the lint as an executable, so a regression test can run
# the failing case (a failing derivation cannot be built inside another).
pkgs:
{
  manpages,
  max ? 72,
  doppelgang ? import ./doppelgang-static.nix pkgs,
}:
assert builtins.isInt max && max > 0;
let
  script = pkgs.writeShellScript "manpage-names-lint" ''
    exec ${doppelgang}/bin/doppelgang lint-man --max ${toString max} ${manpages}/share/man
  '';
in
pkgs.runCommand "manpage-names-check" { passthru = { inherit script; }; } ''
  ${script}
  touch $out
''
