// Package web wires the segment tree: chi nesting maps 1:1 onto the segment
// stack — leaf handlers never name their layouts; they're implied by where
// the route is registered.
package web

import (
	"context"
	"net/http"

	"github.com/a-h/templ"
	"github.com/go-chi/chi/v5"

	"example.com/segments/internal/web/domain"
	"example.com/segments/internal/web/layouts"
	"example.com/segments/internal/web/pages"
	"example.com/segments/internal/web/seg"
)

// Router returns the fully wired http.Handler.
func Router() http.Handler {
	r := chi.NewRouter()

	r.Handle("/static/*", http.StripPrefix("/static/", http.FileServer(http.Dir("assets"))))

	root := seg.Segment{
		ID:     "root",
		Render: func(ctx context.Context, _ any, child templ.Component) templ.Component { return layouts.RootSegment(child) },
	}
	marketing := seg.Segment{
		ID:     "marketing",
		Render: func(ctx context.Context, _ any, child templ.Component) templ.Component { return layouts.MarketingSegment(child) },
	}
	auth := seg.Segment{
		ID:     "auth",
		Render: func(ctx context.Context, _ any, child templ.Component) templ.Component { return layouts.AuthSegment(child) },
	}
	dashboard := seg.Segment{
		ID: "dashboard",
		Render: func(ctx context.Context, _ any, child templ.Component) templ.Component {
			return layouts.DashboardSegment(child)
		},
		// Load runs once, when the segment mounts — never per child navigation.
		Load: func(r *http.Request) (any, error) {
			return &domain.User{
				Name:   "Ada Lovelace",
				Email:  "ada@acme.example",
				Notifs: 3,
			}, nil
		},
	}
	settings := seg.Segment{
		ID:     "settings",
		Render: func(ctx context.Context, _ any, child templ.Component) templ.Component { return layouts.SettingsSegment(child) },
		Load:   func(r *http.Request) (any, error) { return &domain.BadgeCounts{N: 7}, nil },
	}

	// The root segment owns the document; every branch sits under it.
	r.Route("/", func(r chi.Router) {
		r.Use(seg.Use(root))

		// A group (not a nested route) because these live at the root path.
		r.Group(func(r chi.Router) {
			r.Use(seg.Use(marketing))
			r.Get("/", func(w http.ResponseWriter, r *http.Request) {
				seg.Page(w, r, "Segments — server-driven layouts", pages.Home())
			})
			r.Get("/about", func(w http.ResponseWriter, r *http.Request) {
				seg.Page(w, r, "How it works — Segments", pages.About())
			})
		})

		// The auth branch: gradient, scroll-lock, back button.
		r.Group(func(r chi.Router) {
			r.Use(seg.Use(auth))
			r.Post("/login", handleLogin)
			r.Get("/login", func(w http.ResponseWriter, r *http.Request) {
				seg.Page(w, r, "Sign in — Segments", pages.Login())
			})
		})

		// The authed app.
		r.Route("/dashboard", func(r chi.Router) {
			r.Use(seg.Use(dashboard))

			r.Get("/", func(w http.ResponseWriter, r *http.Request) {
				seg.Page(w, r, "Overview — Acme", pages.DashHome())
			})

			// Badge freshness via OOB swap (button posts with hx-swap="none").
			r.Post("/notifications/read", func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("HX-Trigger", `showToast`)
				_ = seg.NotifBadge(0).Render(r.Context(), w)
			})

			r.Route("/settings", func(r chi.Router) {
				r.Use(seg.Use(settings))

				// Same URL serves the full page or a modal (see seg.Modalable).
				// oob keeps the tab bar fresh on modal responses.
				shell := func(ctx context.Context, opts seg.ModalOpts, owner, current, parent string, child templ.Component) templ.Component {
					return layouts.ModalShell(opts, owner, current, parent, child)
				}
				oob := []templ.Component{layouts.SettingsTabsOOB()}

				r.Get("/", seg.Modalable(shell, pages.SettingsLeaf, pages.SettingsModalOpts(), oob...))
				r.Get("/billing", seg.Modalable(shell, pages.BillingLeaf, pages.SettingsModalOpts(), oob...))
			})
		})

		// A leaf action; the toast comes via the HX-Trigger.
		r.Post("/billing/export", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("HX-Trigger", `showToast`)
			w.WriteHeader(http.StatusOK)
		})
	})

	return r
}

func handleLogin(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	w.Header().Set("HX-Push-Url", "/dashboard")
	http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
}
