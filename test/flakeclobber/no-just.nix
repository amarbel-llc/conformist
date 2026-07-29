# Fixture: recognized shape, but the migration does not apply — neither
# `pkgs.just` nor `justPkg` appears in the packages list. flakeclobber must say
# so on stdout (a silent exit 0 is indistinguishable from success in a 34-repo
# sweep log) and leave the file unchanged.
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
      in
      {
        devShells.default = pkgs.mkShell {
          packages = [
            conformistPkg
            pkgs.go
            pkgs.gopls
          ];
        };
      }
    );
}
