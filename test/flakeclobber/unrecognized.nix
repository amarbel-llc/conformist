# Fixture: a raw forAllSystems/genAttrs flake — NOT the eachDefaultSystem shape
# flakeparse recognizes. flakeclobber must refuse with ErrUnrecognized, exit 1,
# and leave the file byte-identical. Refusing an unfamiliar shape is the whole
# safety story for a destructive sweep; this fixture pins it.
{
  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
  };

  outputs =
    { self, nixpkgs }:
    let
      systems = [
        "x86_64-linux"
        "aarch64-darwin"
      ];
      forAllSystems = nixpkgs.lib.genAttrs systems;
    in
    {
      devShells = forAllSystems (
        system:
        let
          pkgs = nixpkgs.legacyPackages.${system};
        in
        {
          default = pkgs.mkShell {
            packages = [
              pkgs.just
              pkgs.go
            ];
          };
        }
      );
    };
}
