// Package papi resolves a domain's PAPI-advertised flake templates (PAPI
// RFC-0001 §7 Flake Template Advertisement / §8 Template Resolution). It fetches
// the domain's `.well-known/papi` discovery document, follows the advertised
// `resources.templates` collection (falling back to `<base>/papi/templates`),
// reads the visible `templates[]`, and selects one by id or interactively.
//
// Resolution is restricted to the operator-named domain's own DNS tree: the
// discovery document and the templates collection are fetched over HTTPS from
// the operator-named host or a subdomain of it (the reference deployment
// delegates linenisgreat.com's templates to api.linenisgreat.com). An advertised
// templates URL pointing at an unrelated host is ignored in favor of the
// same-host fallback (RFC-0001 §8 "restrict resolution to the operator-named
// domain").
//
// This first cut is public-only (§8.4): gated templates that a domain projects
// out via visibility/acl simply do not appear in the collection, so a domain
// advertising only private templates reads as "no templates visible".
package papi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// KindFlakeTemplate is the only template kind conform understands; an entry with
// this kind (or an omitted/empty kind, which defaults to it per §7) is a flake
// template. Any other kind is skipped (§8.1: "Skip entries whose kind is not
// understood").
const KindFlakeTemplate = "flake-template"

// maxBodyBytes caps a PAPI response read, so a hostile or misconfigured endpoint
// cannot stream an unbounded body into memory.
const maxBodyBytes = 1 << 20 // 1 MiB

// defaultTimeout bounds a PAPI fetch when the caller supplies no Client, so an
// unresponsive domain cannot hang `conform` indefinitely. A caller wanting a
// different deadline passes its own Client (or a deadline via the context).
const defaultTimeout = 30 * time.Second

// Template is one entry of a domain's PAPI `templates[]` collection (RFC-0001
// §7). `id` and `flakeref` are required; `description` and `kind` are optional.
type Template struct {
	ID          string `json:"id"`
	Flakeref    string `json:"flakeref"`
	Description string `json:"description,omitempty"`
	Kind        string `json:"kind,omitempty"`
}

// discoveryDoc is the subset of `.well-known/papi` conform consumes: the
// universal {data, meta} envelope (RFC-0001 Amendment 18), of which only
// data.resources.templates is read.
type discoveryDoc struct {
	Data struct {
		Resources struct {
			Templates string `json:"templates"`
		} `json:"resources"`
	} `json:"data"`
}

// templatesDoc is the {data, meta} envelope of GET /papi/templates; templates
// live at `.data`.
type templatesDoc struct {
	Data []Template `json:"data"`
}

var (
	// ErrNoTemplates means the domain advertises no visible flake template
	// (none at all, or only gated/private ones this public-only cut cannot see).
	ErrNoTemplates = errors.New("domain advertises no visible flake templates")
	// ErrNoMatch means an explicit `#<id>` matched no visible template. There is
	// deliberately no fallback to another entry (§8.2).
	ErrNoMatch = errors.New("no template matches the requested id")
	// ErrAmbiguous means a bare domain resolved more than one template and no
	// interactive chooser was available to disambiguate (§8.2: fail, never guess).
	ErrAmbiguous = errors.New("multiple templates; specify <domain>#<id>")
)

// Chooser picks one template from an ambiguous set (more than one visible
// template under a bare domain). It is provided only in an interactive (TTY)
// context; a nil chooser makes Select fail with ErrAmbiguous rather than guess.
type Chooser func([]Template) (Template, error)

// Resolver fetches and resolves a domain's advertised flake templates.
type Resolver struct {
	// Client performs the HTTPS requests; nil uses http.DefaultClient.
	Client *http.Client
}

func (r Resolver) client() *http.Client {
	if r.Client != nil {
		return r.Client
	}

	return &http.Client{Timeout: defaultTimeout}
}

// Resolve fetches domain's discovery document, follows the templates collection,
// and returns the visible flake templates in advertised order. domain is a bare
// host (e.g. "api.example.com"); any scheme or path the caller passed is
// stripped by SplitTarget before this point.
func (r Resolver) Resolve(ctx context.Context, domain string) ([]Template, error) {
	base := "https://" + domain

	var disco discoveryDoc
	if err := r.getJSON(ctx, base+"/.well-known/papi", &disco); err != nil {
		return nil, fmt.Errorf("fetching %s discovery document: %w", domain, err)
	}

	templatesURL := resolveTemplatesURL(disco.Data.Resources.Templates, domain, base)

	var doc templatesDoc
	if err := r.getJSON(ctx, templatesURL, &doc); err != nil {
		return nil, fmt.Errorf("fetching templates from %s: %w", domain, err)
	}

	return understoodTemplates(doc.Data), nil
}

// resolveTemplatesURL picks the templates collection URL, keeping resolution
// within the operator-named domain's DNS tree: the advertised URL is used only
// when it is HTTPS on the operator's host or a subdomain of it (the
// frontend→api-subdomain delegation the reference deployment uses); otherwise
// the same-host `<base>/papi/templates` fallback is used (§8, and the absent-key
// fallback of the behavior spec).
func resolveTemplatesURL(advertised, domain, base string) string {
	advertised = strings.TrimSpace(advertised)
	if advertised == "" {
		return base + "/papi/templates"
	}

	u, err := url.Parse(advertised)
	if err != nil || u.Scheme != "https" || !hostWithinDomain(u.Host, domain) {
		return base + "/papi/templates"
	}

	return advertised
}

// hostWithinDomain reports whether host is the operator-named domain or a
// subdomain of it, so an advertised templates URL may delegate down the
// operator's own DNS tree (linenisgreat.com → api.linenisgreat.com) but not jump
// to an unrelated third-party host.
func hostWithinDomain(host, domain string) bool {
	return host == domain || strings.HasSuffix(host, "."+domain)
}

// understoodTemplates filters to entries conform can bootstrap: an understood
// kind (empty/"flake-template") with both an id and a flakeref. Entries with an
// unrecognized kind or missing required fields are skipped (§8.1).
func understoodTemplates(raw []Template) []Template {
	out := make([]Template, 0, len(raw))
	for _, t := range raw {
		if t.Kind != "" && t.Kind != KindFlakeTemplate {
			continue
		}
		t.ID = strings.TrimSpace(t.ID)
		t.Flakeref = strings.TrimSpace(t.Flakeref)
		if t.ID == "" || t.Flakeref == "" {
			continue
		}
		out = append(out, t)
	}

	return out
}

func (r Resolver) getJSON(ctx context.Context, rawURL string, dst any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return fmt.Errorf("building request for %s: %w", rawURL, err)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := r.client().Do(req)
	if err != nil {
		return fmt.Errorf("requesting %s: %w", rawURL, err)
	}

	// Read then close explicitly (no defer): the body is bounded, and closing
	// inline lets the close error be handled rather than discarded.
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if cerr := resp.Body.Close(); cerr != nil && err == nil {
		err = cerr
	}
	if err != nil {
		return fmt.Errorf("reading %s: %w", rawURL, err)
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s returned HTTP %d", rawURL, resp.StatusCode)
	}

	if err := json.Unmarshal(body, dst); err != nil {
		return fmt.Errorf("decoding %s as PAPI JSON: %w", rawURL, err)
	}

	return nil
}

// Select chooses a template from the visible set. id is the explicit `#<id>`
// (empty for a bare domain). With an id it is an exact match with no fallback;
// bare with exactly one template uses it; bare with more than one calls chooser
// (or fails ErrAmbiguous when chooser is nil); an empty set is ErrNoTemplates
// (§8.2).
func Select(templates []Template, id string, chooser Chooser) (Template, error) {
	if len(templates) == 0 {
		return Template{}, ErrNoTemplates
	}

	if id != "" {
		for _, t := range templates {
			if t.ID == id {
				return t, nil
			}
		}

		return Template{}, fmt.Errorf("%w %q (available: %s)", ErrNoMatch, id, strings.Join(IDs(templates), ", "))
	}

	if len(templates) == 1 {
		return templates[0], nil
	}

	if chooser == nil {
		return Template{}, fmt.Errorf("%w (available: %s)", ErrAmbiguous, strings.Join(IDs(templates), ", "))
	}

	return chooser(templates)
}

// IDs returns the ids of templates, for diagnostics and prompts.
func IDs(templates []Template) []string {
	ids := make([]string, len(templates))
	for i, t := range templates {
		ids[i] = t.ID
	}

	return ids
}

// SplitTarget splits a `conform` target argument into a bare domain host and an
// optional `#<id>` selector. Any scheme (https://) and trailing path/slash on
// the domain portion are stripped, so both "example.com" and
// "https://example.com/#eng" normalize to ("example.com", "eng").
func SplitTarget(target string) (domain, id string) {
	domain = strings.TrimSpace(target)

	if i := strings.IndexByte(domain, '#'); i >= 0 {
		id = strings.TrimSpace(domain[i+1:])
		domain = strings.TrimSpace(domain[:i])
	}

	// Strip an explicit scheme and any path, keeping only the host, so the
	// discovery URL is always https://<host>/.well-known/papi.
	if i := strings.Index(domain, "://"); i >= 0 {
		domain = domain[i+3:]
	}
	domain = strings.TrimSuffix(domain, "/")
	if i := strings.IndexByte(domain, '/'); i >= 0 {
		domain = domain[:i]
	}

	return domain, id
}
