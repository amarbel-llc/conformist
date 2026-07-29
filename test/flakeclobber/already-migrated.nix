# Fixture: the migration is already done — `justPkg` is in the packages list and
# `pkgs.just` is gone. flakeclobber must report it satisfied and exit 0 without
# touching the file (idempotency).
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
            justPkg
            pkgs.go
          ];
        };
      }
    );
}
