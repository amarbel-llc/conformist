# A fully static (pkgsStatic/musl) build of just-us's `just`, for the RFC 0005
# POC. It stands in for the static `just` just-us will publish to the fleet
# cache (RFC 0005 §7 step 1), which does not exist yet. Read-only w.r.t. just-us:
# it builds from a pinned rev and adds no flake input, so conformist stays
# strictly upstream. Shared by the `explore-static-just` and
# `explore-profile-check` recipes so the pin lives in one place.
{ pkgs }:
let
  src = pkgs.fetchgit {
    url = "https://code.linenisgreat.com/just-us.git";
    rev = "308ef38000c220c59eff9ef6dc91b5d8ee885a54";
    hash = "sha256-d2+UNPI0WCaabsVFKAYrGlMQbSfnHLuljXaPsqvzE3A=";
  };
in
pkgs.pkgsStatic.rustPlatform.buildRustPackage {
  pname = "just-static";
  version = "poc";
  inherit src;
  cargoLock.lockFile = "${src}/Cargo.lock";
  doCheck = false;
}
