# go.nix — this module's dependencies (igloo FDR 0008, godyn(7)); go.mod,
# gomod2nix.toml and the package graph are rendered or derived from it inside
# nix. Edit through the escape hatch (godyn-go) or by hand. Fleet modules go in
# flakeInputs (an input of flake.nix, resolved as its go-pkgs); third-party
# modules go in require (version, vendor hash, optional go version).
{
  module = "code.linenisgreat.com/EXAMPLE_NAME";
  go = "1.26";
  flakeInputs = { };
  replace = { };
  require = { };
}
