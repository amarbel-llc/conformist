package profile_test

import (
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"code.linenisgreat.com/conformist/profile"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/test_ui"
	"github.com/stretchr/testify/require"
)

// Vectors from madder's own encoder over vectorContent (`just
// explore-markl-roundtrip`). They pin this package's blech32 port to madder's,
// so the two cannot drift into pins that silently never verify.
const (
	vectorContent    = "conformist RFC 0005 artifact pin round-trip\n"
	vectorSHA256Hex  = "3e20e9cff1463002c20e6f375ea51b8ba874ac3e8521a4a7da7ce32af30d3bc2"
	vectorSHA256ID   = "sha256-8cswnnl3gccq9sswdum4afgm3w58ftp7s5s6ff760n3j4ucd80pq5pm63c"
	vectorBlake2bHex = "675694554f2a0f9cda24b198ece4c4fb2dd4e828f1a6120e66c464835899f99b"
	vectorBlake2bID  = "blake2b256-vatfg4209g8eek3ykxvweexylvkaf6pg7xnpyrnxc3jgxkyelxdsjez5vl"
	purpose          = profile.PurposeArtifactDigest
)

func TestMarklMatchesMadderVectors(t *testing.T) {
	for format, tc := range map[string]struct{ hex, id string }{
		"sha256":     {vectorSHA256Hex, vectorSHA256ID},
		"blake2b256": {vectorBlake2bHex, vectorBlake2bID},
	} {
		t.Run(format, func(t *testing.T) {
			as := require.New(t)

			id := profile.NewMarklID(format, []byte(vectorContent))
			as.Equal(tc.hex, hex.EncodeToString(id.Digest))
			as.Equal(purpose+"@"+tc.id, id.String(), "encoder must reproduce madder's spelling")

			parsed, err := profile.ParseMarklID(purpose + "@" + tc.id)
			as.NoError(err)
			as.Equal(id, parsed, "decoder must recover madder's digest")

			as.NoError(parsed.Verify([]byte(vectorContent)))
			as.ErrorIs(parsed.Verify([]byte(vectorContent+"tampered")), profile.ErrMarklMismatch)
		})
	}
}

func TestMarklRejections(t *testing.T) {
	badChecksum := vectorSHA256ID[:len(vectorSHA256ID)-1] + "q"

	for name, tc := range map[string]struct {
		in   string
		want error
	}{
		"bare, no purpose": {vectorSHA256ID, profile.ErrMarklPurposeMissing},
		"unknown purpose":  {"piggy-piv_auth-v1@" + vectorSHA256ID, profile.ErrMarklUnknownPurpose},
		// The purpose pins used to borrow; a leftover pin must fail loudly.
		"borrowed dodder purpose": {"dodder-blob-digest-sha256-v1@" + vectorSHA256ID, profile.ErrMarklUnknownPurpose},
		"pending pin":             {"PENDING: " + purpose + "@sha256-...", profile.ErrMarklUnknownPurpose},
		"bad checksum":            {purpose + "@" + badChecksum, profile.ErrBlech32Checksum},
		"uppercase":               {purpose + "@" + strings.ToUpper(vectorSHA256ID), profile.ErrBlech32Case},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := profile.ParseMarklID(tc.in)
			require.ErrorIs(t, err, tc.want)
		})
	}
}

const validProfile = `---
# a test profile
! toml-conformist_profile-v1
---

[artifact.tool]
form = "static"
url = "file:///unused"
markl = "unused"

[linter.recipes]
command = "tool --dump"
rule-tool = "jq"
includes = ["justfile"]
passes-files = false
rule = '''
.[] | "it's '\(.)'"
'''
`

func TestParseValidProfile(t *testing.T) {
	as := require.New(t)

	doc, err := profile.Parse("test.profile", []byte(validProfile))
	as.NoError(err)
	as.Equal("a test profile", doc.Description)
	as.Equal("static", doc.Artifacts["tool"].Form)
	as.True(doc.Artifacts["tool"].IsExecutable(), "executable defaults to true")
	as.Equal(".[] | \"it's '\\(.)'\"\n", doc.Linters["recipes"].Rule)
}

// TestParseCommittedProfile keeps conformist's own conformist.profile parseable.
// Its pins are still pending, which Parse deliberately does not judge.
func TestParseCommittedProfile(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "conformist.profile"))
	require.NoError(t, err)

	_, err = profile.Parse("conformist.profile", data)
	require.NoError(t, err)
}

func TestParseRejections(t *testing.T) {
	for name, tc := range map[string]struct {
		old, new string
		want     error
	}{
		"unknown type tag": {
			"! toml-conformist_profile-v1", "! toml-conformist_profile-v2", profile.ErrUnknownType,
		},
		"no body separator": {"---\n\n[artifact", "---\n[artifact", profile.ErrNotHyphence},
		"delegation line": {
			"# a test profile\n", "# a test profile\n- baseline=x\n", profile.ErrUnsupportedInPOC,
		},
		"formatter stanza": {
			"[linter.recipes]", "[formatter.x]\ncommand = \"x\"\n\n[linter.recipes]", profile.ErrUnknownField,
		},
		"unknown linter field": {"passes-files = false", "passes-files = false\nnope = 1", profile.ErrUnknownField},
		"oci form":             {`form = "static"`, `form = "oci"`, profile.ErrUnsupportedInPOC},
		"rule both ways": {
			"passes-files = false", "passes-files = false\nrule-artifact = \"tool\"", profile.ErrRuleCarriedTwice,
		},
		"rule stanza passes files": {"passes-files = false", "passes-files = true", profile.ErrInvalidLinter},
		"unknown rule tool":        {`rule-tool = "jq"`, `rule-tool = "sed"`, profile.ErrUnknownRuleTool},
		"undeclared prelude": {
			"passes-files = false", "passes-files = false\npreludes = [\"nope\"]", profile.ErrUnknownPrelude,
		},
		"prelude both ways": {
			"[linter.recipes]", "[prelude.p]\nrule-tool = \"jq\"\nrule = \"def x: 1;\"\nartifact = \"tool\"\n\n[linter.recipes]",
			profile.ErrRuleCarriedTwice,
		},
		"prelude carries nothing": {
			"[linter.recipes]", "[prelude.p]\nrule-tool = \"jq\"\n\n[linter.recipes]", profile.ErrInvalidPrelude,
		},
		"prelude on an executable artifact": {
			"[linter.recipes]", "[prelude.p]\nrule-tool = \"jq\"\nartifact = \"tool\"\n\n[linter.recipes]",
			profile.ErrInvalidPrelude,
		},
	} {
		t.Run(name, func(t *testing.T) {
			as := require.New(t)

			src := strings.Replace(validProfile, tc.old, tc.new, 1)
			as.NotEqual(validProfile, src, "the case must actually change the profile")

			_, err := profile.Parse("test.profile", []byte(src))
			as.ErrorIs(err, tc.want)
		})
	}
}

// TestParseUnknownFieldSuggestsANewerConformist pins the hint: a profile newer
// than the resolver fails on fields it predates, and the error must point at
// updating conformist (or refreshing a cached `nix run`) rather than read as a
// malformed profile.
func TestParseUnknownFieldSuggestsANewerConformist(t *testing.T) {
	src := strings.Replace(validProfile, "passes-files = false", "passes-files = false\nfrom-the-future = 1", 1)

	_, err := profile.Parse("test.profile", []byte(src))
	require.ErrorIs(t, err, profile.ErrUnknownField)
	require.ErrorContains(t, err, "update conformist")
	require.ErrorContains(t, err, "nix run --refresh")
}

// resolveFixture writes an artifact and a profile pinning pinned (normally the
// artifact's own bytes), and returns the parsed document and a resolver whose
// cache lives in a fresh temp dir.
func resolveFixture(t *test_ui.T, content, pinned []byte) (*profile.Document, profile.Resolver) {
	t.Helper()

	dir := t.TempDir()
	artifact := filepath.Join(dir, "tool-src")
	require.NoError(t, os.WriteFile(artifact, content, 0o644))

	src := strings.Replace(validProfile, `url = "file:///unused"`, `url = "file://`+artifact+`"`, 1)
	src = strings.Replace(src, `markl = "unused"`, `markl = "`+profile.NewMarklID("sha256", pinned).String()+`"`, 1)

	doc, err := profile.Parse("test.profile", []byte(src))
	require.NoError(t, err)

	return doc, profile.Resolver{CacheDir: filepath.Join(dir, "cache")}
}

func TestResolveMaterializesAndTranslates(tt *testing.T) {
	t := &test_ui.T{T: tt}
	as := require.New(t)
	content := []byte("#!/bin/sh\necho '[]'\n")

	doc, r := resolveFixture(t, content, content)

	res, err := r.Resolve(t.Context(), doc)
	as.NoError(err)

	as.Len(res.PathDirs, 1)
	tool := filepath.Join(res.PathDirs[0], "tool")

	info, err := os.Stat(tool)
	as.NoError(err)
	as.Equal(os.FileMode(0o555), info.Mode().Perm(), "executable artifacts are materialized read-only +x")

	lc := res.Linters["recipes"]
	as.NotContains(lc.Command, "it's", "the rule must never be interpolated into the command line")
	as.Contains(lc.Command, "tool --dump")
	as.Contains(lc.Command, profile.NewMarklID("sha256", content).String(), "pins are part of the cache key")

	as.Len(lc.Options, 1)
	rule, err := os.ReadFile(lc.Options[0])
	as.NoError(err)
	as.Equal(doc.Linters["recipes"].Rule, string(rule))

	// A second resolve reuses the verified cache entry.
	again, err := r.Resolve(t.Context(), doc)
	as.NoError(err)
	as.Equal(res.PathDirs, again.PathDirs)
}

// TestResolveJoinsPreludesInOrder pins the assembled program: preludes in the
// order the rule lists them (not declaration or name order), an artifact
// prelude contributing its verified bytes, then the rule.
func TestResolveJoinsPreludesInOrder(tt *testing.T) {
	t := &test_ui.T{T: tt}
	as := require.New(t)
	dir := t.TempDir()

	tool := []byte("#!/bin/sh\n")
	defs := []byte("def fromartifact: 1;")
	toolPath := filepath.Join(dir, "tool-src")
	defsPath := filepath.Join(dir, "defs-src")
	as.NoError(os.WriteFile(toolPath, tool, 0o644))
	as.NoError(os.WriteFile(defsPath, defs, 0o644))

	src := fmt.Sprintf(`---
! toml-conformist_profile-v1
---

[artifact.tool]
form = "static"
url = "file://%s"
markl = %q

[artifact.defs]
form = "static"
executable = false
url = "file://%s"
markl = %q

[prelude.a-from-artifact]
rule-tool = "jq"
artifact = "defs"

[prelude.z-inline]
rule-tool = "jq"
rule = "def inline: 2;"

[linter.recipes]
command = "tool --dump"
rule-tool = "jq"
preludes = ["z-inline", "a-from-artifact"]
includes = ["justfile"]
passes-files = false
rule = "inline + fromartifact"
`, toolPath, profile.NewMarklID("sha256", tool), defsPath, profile.NewMarklID("blake2b256", defs))

	doc, err := profile.Parse("test.profile", []byte(src))
	as.NoError(err)

	res, err := profile.Resolver{CacheDir: filepath.Join(dir, "cache")}.Resolve(t.Context(), doc)
	as.NoError(err)

	program, err := os.ReadFile(res.Linters["recipes"].Options[0])
	as.NoError(err)
	as.Equal("def inline: 2;\ndef fromartifact: 1;\ninline + fromartifact", string(program))
}

func TestResolveRejectsMismatchWithoutCaching(tt *testing.T) {
	t := &test_ui.T{T: tt}
	as := require.New(t)

	doc, r := resolveFixture(t, []byte("the real bytes"), []byte("the pinned bytes"))

	_, err := r.Resolve(t.Context(), doc)
	as.ErrorIs(err, profile.ErrMarklMismatch)

	_, statErr := os.Stat(filepath.Join(r.CacheDir, "artifacts"))
	as.ErrorIs(statErr, fs.ErrNotExist, "a failed artifact must never reach the cache")
}

func TestResolveRejectsCorruptedCache(tt *testing.T) {
	t := &test_ui.T{T: tt}
	as := require.New(t)
	content := []byte("#!/bin/sh\necho '[]'\n")

	doc, r := resolveFixture(t, content, content)

	res, err := r.Resolve(t.Context(), doc)
	as.NoError(err)

	cached := filepath.Join(res.PathDirs[0], "tool")
	as.NoError(os.Chmod(cached, 0o644))
	as.NoError(os.WriteFile(cached, []byte("swapped"), 0o644))

	_, err = r.Resolve(t.Context(), doc)
	as.ErrorIs(err, profile.ErrCachedCorrupt)
}

func TestResolveChecksPinsBeforeFetching(t *testing.T) {
	src := strings.Replace(validProfile, `markl = "unused"`, `markl = "PENDING"`, 1)

	doc, err := profile.Parse("test.profile", []byte(src))
	require.NoError(t, err)

	// url is file:///unused, which does not exist: a markl error (not a fetch
	// error) proves the pin was judged first.
	_, err = profile.Resolver{CacheDir: t.TempDir()}.Resolve(t.Context(), doc)
	require.ErrorIs(t, err, profile.ErrMarklPurposeMissing)
}
