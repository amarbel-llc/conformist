package format //nolint:testpackage

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"code.linenisgreat.com/conformist/config"
	"code.linenisgreat.com/conformist/stats"
	"code.linenisgreat.com/conformist/test"
	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/test_ui"
	"github.com/stretchr/testify/require"
)

func TestInvalidFormatterName(t *testing.T) {
	as := require.New(t)

	const batchSize = 1024

	cfg := &config.Config{}
	cfg.OnUnmatched = "info"

	statz := stats.New()

	// simple "empty" config
	_, err := NewCompositeFormatter(cfg, &statz, batchSize)
	as.NoError(err)

	// valid name using all the acceptable characters
	cfg.FormatterConfigs = map[string]*config.Formatter{
		"echo_command-1234567890": {
			Command:  "echo",
			Includes: []string{"*"},
		},
	}

	_, err = NewCompositeFormatter(cfg, &statz, batchSize)
	as.NoError(err)

	// test with some bad examples
	for _, character := range []string{
		" ", ":", "?", "*", "[", "]", "(", ")", "|", "&", "<", ">", "\\", "/", "%", "$", "#", "@", "`", "'",
	} {
		cfg.FormatterConfigs = map[string]*config.Formatter{
			"touch_" + character: {
				Command: "touch",
			},
		}

		_, err = NewCompositeFormatter(cfg, &statz, batchSize)
		as.ErrorIs(err, ErrInvalidName)
	}
}

func TestFormatSignature(tt *testing.T) {
	t := &test_ui.T{T: tt}
	as := require.New(t)

	const batchSize = 1024

	statz := stats.New()

	tempDir := t.TempDir()

	// symlink some formatters into temp dir, so we can mess with their mod times
	binPath := filepath.Join(tempDir, "bin")
	as.NoError(os.Mkdir(binPath, 0o755))

	binaries := []string{"black", "rufo", "gofmt"}

	for _, name := range binaries {
		src, err := exec.LookPath(name)
		as.NoError(err)
		as.NoError(os.Symlink(src, filepath.Join(binPath, name)))
	}

	// prepend our test bin directory to PATH
	t.Setenv("PATH", binPath+":"+os.Getenv("PATH"))

	// start with 2 formatters
	cfg := &config.Config{
		OnUnmatched: "info",
		FormatterConfigs: map[string]*config.Formatter{
			"python": {
				Command:  "black",
				Includes: []string{"*.py"},
			},
			"ruby": {
				Command:  "rufo",
				Options:  []string{"-x"},
				Includes: []string{"*.rb"},
			},
		},
	}

	oldSignature := assertSignatureChangedAndStable(t, as, cfg, nil)

	t.Run(test_ui.MakeTestCaseInfo("change formatter mod time"), func(t *test_ui.T) {
		for _, name := range []string{"black", "rufo"} {
			t.Logf("changing mod time of %s", name)

			// tweak mod time
			newTime := time.Now().Add(-time.Minute)
			as.NoError(test.Lutimes(t, filepath.Join(binPath, name), newTime, newTime))

			oldSignature = assertSignatureChangedAndStable(t, as, cfg, oldSignature)
		}
	})

	t.Run(test_ui.MakeTestCaseInfo("modify formatter options"), func(_ *test_ui.T) {
		f, err := NewCompositeFormatter(cfg, &statz, batchSize)
		as.NoError(err)

		oldSignature = assertSignatureChangedAndStable(t, as, cfg, nil)

		// adjust python includes
		python := cfg.FormatterConfigs["python"]
		python.Includes = []string{"*.py", "*.pyi"}

		newHash, err := f.signature()
		as.NoError(err)
		as.Equal(oldSignature, newHash, "hash should not have changed")

		// adjust python excludes
		python.Excludes = []string{"*.pyi"}

		newHash, err = f.signature()
		as.NoError(err)
		as.Equal(oldSignature, newHash, "hash should not have changed")

		// adjust python options
		python.Options = []string{"-w", "-s"}
		oldSignature = assertSignatureChangedAndStable(t, as, cfg, oldSignature)

		// adjust python priority
		python.Priority = 100
		oldSignature = assertSignatureChangedAndStable(t, as, cfg, oldSignature)

		// adjust command
		python.Command = "deadnix"
		oldSignature = assertSignatureChangedAndStable(t, as, cfg, oldSignature)
	})

	t.Run(test_ui.MakeTestCaseInfo("add/remove formatters"), func(_ *test_ui.T) {
		cfg.FormatterConfigs["go"] = &config.Formatter{
			Command:  "gofmt",
			Options:  []string{"-w"},
			Includes: []string{"*.go"},
		}

		oldSignature = assertSignatureChangedAndStable(t, as, cfg, oldSignature)

		// remove python formatter
		delete(cfg.FormatterConfigs, "python")
		oldSignature = assertSignatureChangedAndStable(t, as, cfg, oldSignature)

		// remove elm formatter
		delete(cfg.FormatterConfigs, "ruby")
		oldSignature = assertSignatureChangedAndStable(t, as, cfg, oldSignature)
	})
}

func assertSignatureChangedAndStable(
	t *test_ui.T,
	as *require.Assertions,
	cfg *config.Config,
	oldSignature signature,
) (h signature) {
	t.Helper()

	statz := stats.New()
	f, err := NewCompositeFormatter(cfg, &statz, 1024)
	as.NoError(err)

	newHash, err := f.signature()
	as.NoError(err)
	as.NotEqual(oldSignature, newHash, "hash should have changed")

	sameHash, err := f.signature()
	as.NoError(err)
	as.Equal(newHash, sameHash, "hash should not have changed")

	return newHash
}
