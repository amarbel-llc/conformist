package conform

import (
	"fmt"

	"code.linenisgreat.com/conformist/cmd/conform/papi"
	"github.com/charmbracelet/huh"
)

// promptTemplate presents an interactive huh selector over templates and returns
// the chosen one. It is wired as a papi.Chooser only in an interactive (TTY)
// context; non-interactively an ambiguous selection fails instead (§8.2, never
// guess).
func promptTemplate(templates []papi.Template) (papi.Template, error) {
	options := make([]huh.Option[int], len(templates))
	for i, t := range templates {
		label := t.ID
		if t.Description != "" {
			label = fmt.Sprintf("%s — %s", t.ID, t.Description)
		}
		options[i] = huh.NewOption(label, i)
	}

	chosen := 0
	form := huh.NewForm(
		huh.NewGroup(
			huh.NewSelect[int]().
				Title("Select a flake template to bootstrap from").
				Options(options...).
				Value(&chosen),
		),
	)

	if err := form.Run(); err != nil {
		return papi.Template{}, fmt.Errorf("template selection cancelled: %w", err)
	}

	return templates[chosen], nil
}
