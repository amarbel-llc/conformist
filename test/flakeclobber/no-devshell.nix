# Fixture: recognized eachDefaultSystem shape, but there is no
# devShells.default packages list to migrate. flakeclobber must report
# ErrNoDevShell, exit 1, and leave the file unchanged.
{
  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
    utils.url = "github:numtide/flake-utils";
    conformist.url = "git+https://code.linenisgreat.com/conformist.git";
  };

  outputs =
    {
      self,
      nixpkgs,
      utils,
      conformist,
    }:
    utils.lib.eachDefaultSystem (
      system:
      let
        pkgs = import nixpkgs { inherit system; };
        conformistPkg = conformist.packages.${system}.default;
        eval = conformist.lib.evalModule pkgs { package = conformistPkg; };
      in
      {
        formatter = eval.config.build.wrapper;
        packages.default = conformistPkg;
      }
    );
}
