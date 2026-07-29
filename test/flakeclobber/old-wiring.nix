# Fixture: a repo wired by a PREVIOUS conformist — 3 of the 4
# conformistLetNames (conformistPkg, eval, impureEval; no justPkg) — with
# `pkgs.just` still in the devShell packages list. This is the canonical fleet
# migration target for conformist#99/#100: flakeclobber replaces the list
# element, and the additive `just-us` input / `justPkg` binding must already be
# (or be made) present or the rewrite references an undefined variable.
{
  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
    utils.url = "github:numtide/flake-utils";
    conformist.url = "git+https://code.linenisgreat.com/conformist.git";
    just-us.url = "git+https://code.linenisgreat.com/just-us.git";
  };

  outputs =
    {
      self,
      nixpkgs,
      utils,
      conformist,
      just-us,
    }:
    utils.lib.eachDefaultSystem (
      system:
      let
        pkgs = import nixpkgs { inherit system; };
        conformistPkg = conformist.packages.${system}.default;
        justPkg = just-us.packages.${system}.default;
        eval = conformist.lib.evalModule pkgs { package = conformistPkg; };
        impureEval = conformist.lib.evalModule pkgs { package = conformistPkg; };
      in
      {
        formatter = eval.config.build.wrapper;

        devShells.default = pkgs.mkShell {
          packages = [
            conformistPkg
            eval.config.build.preCommit
            impureEval.config.build.repair
            pkgs.just
            pkgs.go
          ];
        };
      }
    );
}
