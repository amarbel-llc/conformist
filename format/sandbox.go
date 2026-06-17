package format

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/amarbel-llc/conformist/walk"
)

// check evaluates a formatter in read-only mode and returns findings for the
// files it would change. It uses the native check command when configured (and
// sandbox is not forced), otherwise the sandbox-and-diff strategy (RFC 0001 §3).
func (f *Formatter) check(ctx context.Context, treeRoot string, files []*walk.File) ([]Finding, error) {
	if !f.config.Sandbox && f.config.CheckCommand != "" {
		nonzero, output, err := f.checkNative(ctx, files)
		if err != nil {
			return nil, err
		}

		if nonzero {
			if output != "" {
				fmt.Fprintln(os.Stderr, output)
			}

			// a native check reports at the invocation level, not per file
			return []Finding{{Tool: f.Name(), Kind: FindingFormat}}, nil
		}

		return nil, nil
	}

	changed, err := f.checkSandbox(ctx, treeRoot, files)
	if err != nil {
		return nil, err
	}

	findings := make([]Finding, 0, len(changed))
	for _, file := range changed {
		findings = append(findings, Finding{Tool: f.Name(), Kind: FindingFormat, Path: file.RelPath})
	}

	return findings, nil
}

// checkNative runs the formatter's configured read-only check command. It
// returns true if the command exited non-zero (at least one file is not
// conformant); a non-nil error indicates an operational failure.
func (f *Formatter) checkNative(ctx context.Context, files []*walk.File) (nonzero bool, output string, err error) {
	if len(files) == 0 {
		return false, "", nil
	}

	maxBatch := len(files)
	if f.HasNoPositionalArgSupport() {
		maxBatch = 1
	}

	var combined strings.Builder

	for start := 0; start < len(files); start += maxBatch {
		end := min(start+maxBatch, len(files))

		args := append([]string{}, f.config.CheckOptions...)
		for _, file := range files[start:end] {
			args = append(args, relocateFileArg(f.treeRoot, f.workingDir, file.RelPath, ""))
		}

		cmd := exec.CommandContext(ctx, f.checkExecutable, args...) //nolint:gosec
		cmd.Cancel = func() error { return cmd.Process.Signal(os.Interrupt) }
		cmd.Dir = f.workingDir

		out, runErr := cmd.CombinedOutput()
		combined.Write(out)

		if runErr != nil {
			var exitErr *exec.ExitError
			if errors.As(runErr, &exitErr) {
				nonzero = true

				continue
			}

			return false, combined.String(), fmt.Errorf("formatter '%s' check command failed: %w", f.name, runErr)
		}
	}

	return nonzero, combined.String(), nil
}

// checkSandbox synthesizes a read-only check for a fix-only formatter (RFC 0001
// §6): it copies the matched files into a private temp dir, runs the formatter's
// repair command there, and reports which files the formatter would change. The
// original files are never written.
func (f *Formatter) checkSandbox(ctx context.Context, treeRoot string, files []*walk.File) ([]*walk.File, error) {
	if len(files) == 0 {
		return nil, nil
	}

	// os.MkdirTemp creates the directory with 0o700 permissions.
	dir, err := os.MkdirTemp("", "conformist-check-")
	if err != nil {
		return nil, fmt.Errorf("failed to create sandbox dir: %w", err)
	}

	defer func() { _ = os.RemoveAll(dir) }()

	for _, file := range files {
		if err := copyIntoSandbox(treeRoot, dir, file); err != nil {
			return nil, err
		}
	}

	// Formatters discover config (rustfmt.toml, .editorconfig, …) by walking
	// upward from each formatted file. Those config files are not matched files,
	// so ship the declared ones into the sandbox at the same relative path,
	// otherwise the sandboxed tool runs with its default config and check mode
	// disagrees with repair mode (conformist#28).
	if err := copyConfigFilesIntoSandbox(treeRoot, dir, files, f.config.ConfigFiles); err != nil {
		return nil, err
	}

	maxBatch := len(files)
	if f.HasNoPositionalArgSupport() {
		maxBatch = 1
	}

	// working-dir (#38) is relative to the sandbox root here, not the tree root,
	// so the formatter runs inside the sandbox copy of its subdir. Ensure it
	// exists even when no matched file was copied under it.
	sandboxToolDir := resolveToolDir(dir, f.config.WorkingDir)
	if err := os.MkdirAll(sandboxToolDir, 0o700); err != nil {
		return nil, fmt.Errorf("failed to create sandbox working dir: %w", err)
	}

	for start := 0; start < len(files); start += maxBatch {
		end := min(start+maxBatch, len(files))

		args := append([]string{}, f.config.Options...)
		for _, file := range files[start:end] {
			args = append(args, relocateFileArg(dir, sandboxToolDir, file.RelPath, ""))
		}

		cmd := exec.CommandContext(ctx, f.executable, args...) //nolint:gosec
		cmd.Cancel = func() error { return cmd.Process.Signal(os.Interrupt) }
		cmd.Dir = sandboxToolDir

		if out, runErr := cmd.CombinedOutput(); runErr != nil {
			return nil, fmt.Errorf("formatter '%s' failed in sandbox: %w\n%s", f.name, runErr, out)
		}
	}

	var changed []*walk.File

	for _, file := range files {
		same, err := sameContent(file.Path, filepath.Join(dir, file.RelPath))
		if err != nil {
			return nil, err
		}

		if !same {
			changed = append(changed, file)
		}
	}

	return changed, nil
}

// copyIntoSandbox copies a file's content and permission bits into dir at its
// relative path. Symlinks are copied as their resolved regular-file contents; a
// link whose target resolves outside the tree root is a hard error (RFC 0001 §6
// and Security Considerations).
func copyIntoSandbox(treeRoot, dir string, file *walk.File) error {
	info, err := os.Lstat(file.Path)
	if err != nil {
		return fmt.Errorf("failed to lstat %s: %w", file.RelPath, err)
	}

	srcPath := file.Path

	if info.Mode()&os.ModeSymlink != 0 {
		resolved, err := filepath.EvalSymlinks(file.Path)
		if err != nil {
			return fmt.Errorf("failed to resolve symlink %s: %w", file.RelPath, err)
		}

		rel, err := filepath.Rel(treeRoot, resolved)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
			return fmt.Errorf("symlink %s resolves outside the tree root", file.RelPath)
		}

		srcPath = resolved
	}

	content, err := os.ReadFile(srcPath)
	if err != nil {
		return fmt.Errorf("failed to read %s: %w", file.RelPath, err)
	}

	// The sandbox copy must be writable by us: fix-only formatters rewrite it
	// in place during check mode, and the source may be read-only (e.g. a
	// /nix/store path under `nix flake check`). Preserving the source mode
	// verbatim would leave a read-only copy and the formatter would fail with
	// "permission denied", so keep the source perms but force owner read+write.
	mode := os.FileMode(0o600)
	if fi, statErr := os.Stat(srcPath); statErr == nil {
		mode = fi.Mode().Perm() | 0o600
	}

	dst := filepath.Join(dir, file.RelPath)
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return fmt.Errorf("failed to create sandbox subdir: %w", err)
	}

	// dst is conformist's own sandbox path (sandbox dir + the matched file's
	// RelPath), not an externally-tainted path, so the traversal warning is moot.
	if err := os.WriteFile(dst, content, mode); err != nil { //nolint:gosec
		return fmt.Errorf("failed to write sandbox copy: %w", err)
	}

	return nil
}

// copyConfigFilesIntoSandbox ships the formatter's declared config files into
// the sandbox so upward config discovery finds them there (conformist#28). For
// each matched file it walks the ancestor directories from the file's directory
// up to the tree root; any declared config-file name present in an ancestor is
// copied into the sandbox at the same relative path. Copying every ancestor's
// config is a superset of what discovery (e.g. .editorconfig's `root = true`)
// would stop at, which is correct: extra config the tool would not consult is
// harmless. Config files are read-only to the tool, but must be present at the
// right relative path. Matched files are skipped — they are already copied.
func copyConfigFilesIntoSandbox(treeRoot, dir string, files []*walk.File, names []string) error {
	if len(names) == 0 {
		return nil
	}

	// relative paths already in the sandbox (matched files + config files copied
	// on an earlier iteration), so we never copy the same path twice.
	copied := make(map[string]struct{}, len(files))
	for _, file := range files {
		copied[filepath.Clean(file.RelPath)] = struct{}{}
	}

	for _, file := range files {
		for relDir := filepath.Dir(file.RelPath); ; relDir = filepath.Dir(relDir) {
			for _, name := range names {
				rel := filepath.Join(relDir, name)
				if _, seen := copied[rel]; seen {
					continue
				}

				abs := filepath.Join(treeRoot, rel)
				if info, err := os.Lstat(abs); err != nil || info.IsDir() {
					continue
				}

				if err := copyIntoSandbox(treeRoot, dir, &walk.File{Path: abs, RelPath: rel}); err != nil {
					return err
				}

				copied[rel] = struct{}{}
			}

			if relDir == "." || relDir == string(os.PathSeparator) {
				break
			}
		}
	}

	return nil
}

func sameContent(a, b string) (bool, error) {
	ca, err := os.ReadFile(a)
	if err != nil {
		return false, fmt.Errorf("failed to read %s: %w", a, err)
	}

	cb, err := os.ReadFile(b)
	if err != nil {
		return false, fmt.Errorf("failed to read %s: %w", b, err)
	}

	return bytes.Equal(ca, cb), nil
}
