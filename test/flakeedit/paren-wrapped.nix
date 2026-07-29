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

        version =
          let
            raw = "v1.2.3";
          in
          nixpkgs.lib.removePrefix "v" raw;
      in
      {
        packages.default = pkgs.hello;
        devShells.default = pkgs.mkShell {
          packages = [
            pkgs.just
          ];
        };
      }
    ));
}
