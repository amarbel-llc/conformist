package git

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// StagedBlob returns the content of the stage-0 index entry for the given
// toplevel-relative path (`git cat-file blob :<path>`). The --staged
// partial-stage lane (#40) formats this blob in isolation rather than reading
// the working-tree file, which carries additional unstaged edits that must be
// preserved.
func StagedBlob(ctx context.Context, treeRoot, path string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", "-C", treeRoot, "cat-file", "blob", ":"+path)

	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to read staged blob for %s: %w", path, err)
	}

	return out, nil
}

// StagedFileMode returns the index mode (e.g. "100644", "100755") of the
// stage-0 entry for the given toplevel-relative path (`git ls-files --stage`).
// The mode is preserved when restaging the formatted blob so the exec bit
// survives (#40).
func StagedFileMode(ctx context.Context, treeRoot, path string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", "-C", treeRoot, "ls-files", "--stage", "-z", "--", path)

	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("failed to read staged mode for %s: %w", path, err)
	}

	// "<mode> <oid> <stage>\t<path>\0"; the mode is the first whitespace-
	// separated field.
	fields := strings.Fields(string(out))
	if len(fields) == 0 {
		return "", fmt.Errorf("no staged entry for %s", path)
	}

	return fields[0], nil
}

// HashObject writes content to the object store as a blob, applying the path's
// gitattributes filters (`git hash-object -w --path <path> --stdin`), and
// returns the resulting object id. Used to materialize a formatted staged blob
// (#40).
func HashObject(ctx context.Context, treeRoot, path string, content []byte) (string, error) {
	cmd := exec.CommandContext(ctx, "git", "-C", treeRoot, "hash-object", "-w", "--path", path, "--stdin")
	cmd.Stdin = bytes.NewReader(content)

	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("failed to hash-object %s: %w", path, err)
	}

	return strings.TrimSpace(string(out)), nil
}

// UpdateIndexCacheinfo repoints the index entry for the given toplevel-relative
// path at oid with mode, without touching the working tree
// (`git update-index --cacheinfo <mode>,<oid>,<path>`). This is how the
// partial-stage lane restages a formatted staged blob while leaving the working
// tree's unstaged hunks alone (#40).
func UpdateIndexCacheinfo(ctx context.Context, treeRoot, mode, oid, path string) error {
	cmd := exec.CommandContext(
		ctx, "git", "-C", treeRoot, "update-index", "--cacheinfo", mode+","+oid+","+path,
	)

	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git update-index failed for %s: %w: %s", path, err, out)
	}

	return nil
}
