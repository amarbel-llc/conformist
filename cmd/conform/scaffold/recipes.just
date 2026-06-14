Add these recipes to your justfile (conventions: conformist-justfile(7)), and add
`lint` to your `default` aggregate:

# --- lint ---

lint: lint-fmt

# Read-only formatting + the eng preset's file-based linters, via the sandboxed
# checks.formatting derivation.
lint-fmt:
    #!/usr/bin/env bash
    set -euo pipefail
    system=$(nix eval --raw --impure --expr 'builtins.currentSystem')
    nix build ".#checks.${system}.formatting" --no-link --print-build-logs

lint-impure: lint-worktree

# Impure eng checks (git remotes, sweatfile, agents-md) against the working tree;
# needs SSH remotes + a sweatfile. Add lint-impure to `lint` once your repo has them.
lint-worktree:
    #!/usr/bin/env bash
    set -euo pipefail
    cfg=$(nix build --no-link --print-out-paths '.#conformist-impure-config')
    conformist check --config-file "$cfg" --tree-root .

# --- codemod ---

codemod-fmt: codemod-fmt-tree

# Format the tree in place (repair mode) via `nix fmt`.
codemod-fmt-tree:
    nix fmt
