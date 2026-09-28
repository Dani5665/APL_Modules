package http

import (
	"bytes"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"path"
	"sort"
	"strings"

	"haynesproform/internal/dates"
	"haynesproform/internal/modules"
	"haynesproform/internal/store"
	"haynesproform/web"
)

// Layout names. Every page is rendered into exactly one of them.
const (
	LayoutCustomer = "layout_customer"
	LayoutAdmin    = "layout_admin"
	LayoutBare     = "layout_bare"
)

// renderer holds one parsed template set per page.
//
// A page cannot simply be added to one big set: every page defines a template
// called "content", and those definitions would overwrite each other. Parsing
// layouts and partials afresh for each page keeps them independent.
type renderer struct {
	pages map[string]*template.Template
}

func newRenderer() (*renderer, error) {
	funcs := templateFuncs()

	shared, err := fs.Glob(web.FS, "templates/layouts/*.html")
	if err != nil {
		return nil, err
	}
	partials, err := fs.Glob(web.FS, "templates/partials/*.html")
	if err != nil {
		return nil, err
	}
	shared = append(shared, partials...)
	if len(shared) == 0 {
		return nil, fmt.Errorf("no layout or partial templates were embedded")
	}

	var pageFiles []string
	for _, dir := range []string{"templates/customer", "templates/admin"} {
		matches, err := fs.Glob(web.FS, dir+"/*.html")
		if err != nil {
			return nil, err
		}
		pageFiles = append(pageFiles, matches...)
	}
	if len(pageFiles) == 0 {
		return nil, fmt.Errorf("no page templates were embedded")
	}
	sort.Strings(pageFiles)

	r := &renderer{pages: make(map[string]*template.Template, len(pageFiles))}
	for _, p := range pageFiles {
		name := pageName(p)
		files := append(append([]string{}, shared...), p)
		t, err := template.New(path.Base(p)).Funcs(funcs).ParseFS(web.FS, files...)
		if err != nil {
			return nil, fmt.Errorf("parse template %s: %w", p, err)
		}
		r.pages[name] = t
	}
	return r, nil
}

// pageName turns "templates/admin/requests.html" into "admin/requests".
func pageName(p string) string {
	p = strings.TrimPrefix(p, "templates/")
	return strings.TrimSuffix(p, ".html")
}

// render writes a page into a layout.
//
// The page is rendered into a buffer first: a template error halfway through
// would otherwise leave a half-written response with a 200 status already
// sent.
func (r *renderer) render(w http.ResponseWriter, status int, layout, page string, data any) error {
	t, ok := r.pages[page]
	if !ok {
		return fmt.Errorf("unknown page template %q", page)
	}

	var buf bytes.Buffer
	if err := t.ExecuteTemplate(&buf, layout, data); err != nil {
		return fmt.Errorf("render %s in %s: %w", page, layout, err)
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, err := buf.WriteTo(w)
	return err
}

// renderPartial writes one named template, for htmx fragment responses.
func (r *renderer) renderPartial(w http.ResponseWriter, status int, page, name string, data any) error {
	t, ok := r.pages[page]
	if !ok {
		return fmt.Errorf("unknown page template %q", page)
	}

	var buf bytes.Buffer
	if err := t.ExecuteTemplate(&buf, name, data); err != nil {
		return fmt.Errorf("render partial %s of %s: %w", name, page, err)
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, err := buf.WriteTo(w)
	return err
}

func templateFuncs() template.FuncMap {
	return template.FuncMap{
		// date formats a stored ISO date as DD.MM.YYYY.
		"date": dates.FormatDisplay,
		// datetime formats a stored UTC timestamp in Sofia local time.
		"datetime": dates.DisplayTimestamp,
		"moduleLabel": func(k modules.Key) string {
			return modules.Label(k)
		},
		"tierLabel": func(k modules.Key, t modules.Tier) string {
			return modules.TierLabel(k, t)
		},
		"describeModule": func(k modules.Key, t modules.Tier) string {
			return modules.Describe(k, t)
		},
		"duration": store.DurationLabel,
		"join": func(sep string, items []string) string {
			return strings.Join(items, sep)
		},
		"yesno": func(b bool) string {
			if b {
				return "Да"
			}
			return "Не"
		},
		"add": func(a, b int) int { return a + b },
		"sub": func(a, b int) int { return a - b },
		"seq": func(from, to int) []int {
			if to < from {
				return nil
			}
			out := make([]int, 0, to-from+1)
			for i := from; i <= to; i++ {
				out = append(out, i)
			}
			return out
		},
		// dict builds a map so a partial can be given several values.
		"dict": func(pairs ...any) (map[string]any, error) {
			if len(pairs)%2 != 0 {
				return nil, fmt.Errorf("dict needs an even number of arguments")
			}
			m := make(map[string]any, len(pairs)/2)
			for i := 0; i < len(pairs); i += 2 {
				key, ok := pairs[i].(string)
				if !ok {
					return nil, fmt.Errorf("dict keys must be strings")
				}
				m[key] = pairs[i+1]
			}
			return m, nil
		},
		// queryString rebuilds a query string with one parameter replaced,
		// used by the pagination links.
		"queryString": func(base map[string]string, key string, value any) template.URL {
			parts := make([]string, 0, len(base)+1)
			for k, v := range base {
				if k == key || v == "" {
					continue
				}
				parts = append(parts, template.URLQueryEscaper(k)+"="+template.URLQueryEscaper(v))
			}
			parts = append(parts, template.URLQueryEscaper(key)+"="+template.URLQueryEscaper(fmt.Sprint(value)))
			sort.Strings(parts)
			return template.URL("?" + strings.Join(parts, "&"))
		},
	}
}
