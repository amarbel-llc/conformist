# The eng-hybrid (conformist#65), paren-wrapped AND trailing-merged — the
# dodder/piggy shape, exercising both optionals at once. The merge side holds
# only system-independent outputs (nixosModules/homeManagerModules), which is
# what the whole fleet does, so nothing it defines shadows the per-system
# wiring conform splices in.
{
  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
    utils.url = "github:numtide/flake-utils";
  };

  outputs =
    {
      self,
      nixpkgs,
      utils,
    }:
    (utils.lib.eachDefaultSystem (
      system:
      let
        pkgs = import nixpkgs { inherit system; };

        buildTools = with pkgs; [
          pkg-config
          openssl
        ];
      in
      {
        packages.default = pkgs.hello;
        devShells.default = pkgs.mkShell {
          packages = buildTools ++ [
            pkgs.just
          ];
        };
      }
    ))
    // {
      nixosModules.default = ./nix/module.nix;
      homeManagerModules.default = ./nix/hm.nix;
    };
}
