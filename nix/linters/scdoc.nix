# scdoc as a conformist LINTER (conformist#128): compile each *.scd with scdoc
# and fail on its diagnostics, so a syntax error surfaces at the commit that
# introduces it rather than at the `manpages` derivation (eng-manpages(7)
# PRINCIPLE 4) at merge time.
#
# The check is scdoc ITSELF, not a pattern: its rules are contextual (a leading
# `---` is a list-entry error in a definition-list body but fine as a continuation
# inside a list; `_` is only an inline delimiter at a word boundary), so no regex
# can stand in for it. scdoc reads stdin and writes roff to stdout, so a small
# wrapper loops over the file arguments and discards the roff.
#
# This does not replace the `manpages` derivation: that build still catches
# everything and is what produces the pages. No repair — rewording a sentence to
# satisfy the parser is a judgment call, not a mechanical fix.
#
# Pure (scdoc is a nixpkgs package; no network, no git state), so it runs in the
# sandboxed checks.formatting lane and the pre-commit hook, and is on by default
# in the pure `eng` preset.
{
  config,
  lib,
  pkgs,
  mkLinterModule,
  ...
}:
let
  cfg = config.linters.scdoc;

  check = pkgs.writeShellApplication {
    name = "conformist-scdoc";
    runtimeInputs = [ cfg.scdocPackage ];
    text = ''
      rc=0
      for file in "$@"; do
        # Capture stderr (scdoc's diagnostics), drop the roff on stdout. `if !`
        # keeps a failing scdoc from tripping errexit before the next file.
        if ! diagnostics=$(scdoc <"$file" 2>&1 >/dev/null); then
          printf '%s: %s\n' "$file" "$diagnostics" >&2
          rc=1
        fi
      done
      exit "$rc"
    '';
  };
in
{
  meta.maintainers = [ ];

  imports = [
    (mkLinterModule {
      name = "scdoc";
      includes = [ "*.scd" ];
    })
  ];

  options.linters.scdoc.scdocPackage = lib.mkPackageOption pkgs "scdoc" { };

  # The linter's `package` is the wrapper (its mainProgram is the check), not
  # scdoc itself; scdocPackage is the compiler it runs.
  config = lib.mkIf cfg.enable {
    linters.scdoc.package = lib.mkDefault check;
  };
}
