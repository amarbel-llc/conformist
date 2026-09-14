# Prints one top-level string field (`module`, `go`) of a go.nix module manifest
# (igloo FDR 0008) without evaluating nix. The parse is line-based and accepts
# `<field> = "<value>";` only at attrset depth 1, so the per-module `go` fields
# nested under `require` are never mistaken for the module's own. Exits 1 when
# the field is absent; a manifest whose top-level attrs share a line with the
# opening brace (never emitted by nixfmt) is not understood.
{ pkgs }:
pkgs.writeShellApplication {
  name = "conformist-go-nix-field";
  runtimeInputs = [ pkgs.gawk ];
  text = ''
    field=$1
    file=$2
    awk -v field="$field" '
      {
        if (depth == 1 && match($0, "^[[:space:]]*" field "[[:space:]]*=[[:space:]]*\"[^\"]*\"[[:space:]]*;")) {
          value = substr($0, RSTART, RLENGTH)
          sub(/^[^"]*"/, "", value)
          sub(/".*$/, "", value)
          print value
          found = 1
          exit
        }
        code = $0
        gsub(/"([^"\\]|\\.)*"/, "", code)
        sub(/#.*/, "", code)
        depth += gsub(/\{/, "", code)
        depth -= gsub(/\}/, "", code)
      }
      END { exit(found ? 0 : 1) }
    ' "$file"
  '';
}
