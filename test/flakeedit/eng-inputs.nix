# The conformist#83 shape: igloo + nixpkgs-master inputs, NO nixpkgs, and a
# strict (no-`...`) outputs destructuring. The input splice must dedupe via
# follows inside the conformist input rather than adding a top-level nixpkgs
# the outputs pattern does not name.
{
  inputs = {
    igloo.url = "github:amarbel-llc/nixpkgs/nixos-unstable";
    nixpkgs-master.url = "github:NixOS/nixpkgs/master";
    utils.url = "github:numtide/flake-utils";
  };

  outputs =
    {
      self,
      igloo,
      nixpkgs-master,
      utils,
    }:
    utils.lib.eachDefaultSystem (
      system:
      let
        pkgs = igloo.legacyPackages.${system};
      in
      {
        packages.default = pkgs.hello;
      }
    );
}
