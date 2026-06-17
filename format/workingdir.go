package format

import "path/filepath"

// resolveToolDir returns the directory a tool should run in: its configured
// working-dir joined onto root, or root itself when working-dir is empty (the
// default — unchanged from the historical "run at the tree root" behavior).
// See conformist#38.
func resolveToolDir(root, workingDir string) string {
	if workingDir == "" {
		return root
	}

	return filepath.Join(root, workingDir)
}

// relocateFileArg returns the argument to pass for a matched file to a tool
// running in dir, where the file lives under root (root/relPath, or absPath when
// the walker materialized it elsewhere).
//
// When dir == root (no working-dir configured) it returns the file's historical
// argument verbatim — its absolute walk path when one is set, else its
// tree-relative path — so existing configs are byte-for-byte unchanged. When the
// tool runs in a subdirectory it returns the path relative to that subdirectory,
// so the tool resolves it from its own cwd.
func relocateFileArg(root, dir, relPath, absPath string) string {
	if dir == root {
		if absPath != "" {
			return absPath
		}

		return relPath
	}

	full := absPath
	if full == "" {
		full = filepath.Join(root, relPath)
	}

	if rel, err := filepath.Rel(dir, full); err == nil {
		return rel
	}

	return full
}
