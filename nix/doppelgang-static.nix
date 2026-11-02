# The released doppelgang static binary as a fixed-output derivation, on PATH as
# `doppelgang`. Same url and sha256 as the [artifact.doppelgang] pin in
# conformist.profile (conformist#114); bump them together. x86_64-linux only.
pkgs:
assert
  pkgs.stdenv.hostPlatform.system == "x86_64-linux"
  || throw "doppelgang-static: only an x86_64-linux asset is published; pass `doppelgang` explicitly";
let
  asset = pkgs.fetchurl {
    name = "doppelgang-static-x86_64-linux";
    url = "https://code.linenisgreat.com/doppelgang/releases/download/v0.2.1/doppelgang-static-x86_64-linux";
    sha256 = "f9b3b898d3d05527fcbf58f8b199e3eb4cebaf85c9eb87eb6cfbc0dcb8e01a73";
  };
in
pkgs.runCommand "doppelgang-static" { } ''
  install -Dm755 ${asset} $out/bin/doppelgang
''
