# A conformist wrapper that carries its formatter/linter toolchain on PATH
# (conformist#51).
#
# conformist resolves each formatter/linter's `command` from PATH at run time
# (for a bare-name command like `gofumpt`). A git pre-commit hook
# (`conformist --staged --exit-zero-on-fix`) runs the bare `conformist` binary,
# so if the author's PATH lacks the toolchain (gofumpt, goimports, nixfmt, …)
# the staged repair SILENTLY SKIPS those file types. Module adopters avoid this
# via build.preCommit (store-pinned commands); a repo with a HAND-WRITTEN
# conformist.toml needs the toolchain baked onto the wrapper's PATH instead.
#
# This is the reusable form of the wrapper every such repo otherwise hand-rolls
# (cf. eng-design_patterns-conformist(7) "THE CWD-AWARE WRAPPER"). It builds a
# `writeShellApplication` that execs conformist with `tools` (and conformist
# itself) on PATH, so output is reproducible regardless of the ambient
# environment. The wrapper is general: it backs `nix fmt` (repair), `conformist
# check`, AND the `--staged` hook — point each at the same wrapper.
#
# Usage:
#   # nix fmt / check / repair entrypoint:
#   formatter = conformist.lib.wrapWithToolchain pkgs {
#     conformist = conformist.packages.${system}.default;
#     tools = [ pkgs.gotools pkgs.gofumpt pkgs.nixfmt pkgs.shfmt ];
#     configFile = ./conformist.toml;   # optional; pins --config-file
#   };
#   # then the sweatfile pre-commit hook just adds the staged flags:
#   #   pre-commit = "conformist --staged --exit-zero-on-fix"
#   # (or set name = "conformist-fmt" and reference that.)
pkgs:
{
  conformist,
  tools,
  name ? "conformist",
  configFile ? null,
}:
let
  inherit (pkgs) lib;
  configArg = lib.optionalString (configFile != null) " --config-file=${configFile}";
  # Resolve conformist to its absolute store path and exec THAT, not the name
  # `conformist` on PATH. This keeps the wrapper safe even when its own binary is
  # also named `conformist` (the default) and on PATH — a bare `exec conformist`
  # could otherwise re-resolve to the wrapper itself and recurse.
  conformistBin = lib.getExe' conformist "conformist";
in
pkgs.writeShellApplication {
  inherit name;
  # tools carry the formatter/linter toolchain on PATH; conformist itself is
  # exec'd by absolute path, so it need not be a runtimeInput.
  runtimeInputs = tools;
  text = ''
    exec ${conformistBin}${configArg} "$@"
  '';
}
