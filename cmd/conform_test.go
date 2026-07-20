package cmd_test

import (
	"os"
	"path/filepath"
	"testing"

	"code.linenisgreat.com/conformist/cmd"
	"code.linenisgreat.com/conformist/test"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/test_ui"
	"github.com/stretchr/testify/require"
)

// TestConformWorkingDir pins that `conformist conform` honors --working-dir /
// -C (conformist#82): cmd/conform.go's RunE used to resolve os.Getwd()
// directly instead of routing through changeWorkingDir(v) (the path
// check.go/identity.go/root.go's format command all take), so -C was
// silently ignored and conform always scaffolded into the process's actual
// cwd. The scaffold's plain write-if-absent files (conformist.nix,
// version.env, sweatfile, flake.nix, justfile — see conform.Run) give an
// easy externally observable signal: they must land in the -C target, and
// must NOT land in the unrelated directory the process happens to be in.
//
// The domain-bootstrap mode (`conform <domain>`) shares the same dir
// resolution (both branches read `dir` from the same changeWorkingDir(v)
// call in cmd/conform.go's RunE), so this test's coverage of the local
// scaffold mode also pins the fix for bootstrap mode. A dedicated CLI-level
// test for bootstrap mode isn't practical here: the cobra layer wires
// Bootstrap with a nil Resolver/FlakeInit (real PAPI HTTP resolution + a
// real `nix flake init` subprocess), unlike cmd/conform/bootstrap_test.go's
// direct calls into the conform package, which inject fixtures.
func TestConformWorkingDir(tt *testing.T) {
	t := &test_ui.T{T: tt}
	as := require.New(t)

	// An unrelated empty directory standing in for whatever the process's
	// actual cwd happens to be. If -C were ignored, conform would scaffold
	// here instead of into the target below.
	wrongDir := t.TempDir()
	test.ChangeWorkDir(t, wrongDir)

	target := t.TempDir()

	conformist(t, withArgs("-C", target, "conform"), withError(func(as *require.Assertions, err error) {
		as.ErrorIs(err, cmd.ErrScaffolded)
	}))

	scaffolded := []string{"conformist.nix", "version.env", "sweatfile", "flake.nix", "justfile"}
	for _, name := range scaffolded {
		_, err := os.Stat(filepath.Join(target, name))
		as.NoError(err, "expected %s to be scaffolded into the -C target", name)

		_, err = os.Stat(filepath.Join(wrongDir, name))
		as.True(os.IsNotExist(err), "expected %s NOT to be scaffolded into the actual cwd", name)
	}
}
