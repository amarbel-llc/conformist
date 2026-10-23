// Package buildinfo formats the binary's identity line. It imports nothing from
// this module, so it sits at level 0; see AGENTS.md (LAYOUT).
package buildinfo

// Line is "<name> <version>+<commit>", the line the binary prints for itself.
func Line(name, version, commit string) string {
	return name + " " + version + "+" + commit
}
