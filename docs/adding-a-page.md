# Adding a page — and the thinking behind it

Adding a page to this template is deliberately boring: a templ file, a route, a nav link. The interesting part is **why it stays boring** — how much of the layout, data, and client behavior you get for free. This walks through a `/dashboard/team` page and calls out the "why" at each step.

## The mental model, in one line

> **You never build a page. You build a leaf, and drop it under a segment — the segment already knows how to draw itself around it, fetch its data, and keep the rest of the page honest.**

That's the whole payoff of the segment stack: the layout, its cached data, the title, the badge, the tabs, and the modal all come from _where the route lives_, not from code you write per page.

## 1. The leaf

```templ
templ TeamIndex() {
    <div class="mx-auto max-w-2xl space-y-8">
        <h1 class="text-2xl font-bold tracking-tight">Team</h1>
        <p class="mt-1 text-sm text-muted-foreground">People in your workspace.</p>
    </div>
}
```

Notice what's **missing**: no `<html>`, no sidebar, no header, no title tag. The leaf is only the part that's unique to this page. Everything around it is someone else's job — the `dashboard` segment that wraps it, and the renderer that provides the `<title>`.

## 2. The route — this is the layout

```go
r.Route("/dashboard", func(r chi.Router) {
    r.Use(d.Sessions.RequireAuth)
    r.Use(seg.Use(dashboard))
    // ...
    r.Route("/team", func(r chi.Router) {
        r.Get("/", func(w http.ResponseWriter, r *http.Request) {
            seg.Page(w, r, "Team", pages.TeamIndex())
        })
    })
})
```

The layout isn't something you assemble here — **it's the position in the tree.** Register the route under `dashboard` and the page is _inside_ the dashboard chrome, automatically. Register it elsewhere and it gets a different layout, with zero changes to the leaf.

## 3. The nav link — the diff does the rest

```templ
<a href="/dashboard/team" hx-get="/dashboard/team" hx-push-url="true">Team</a>
```

The browser already has `dashboard` mounted. On click it tells the server that, the server sees `root, dashboard` is a shared prefix, and renders **only the leaf** — the sidebar, header, and everything else stay exactly where they are. There's no "render the page" logic on the client at all; the URL and the mounted-stack diff handle it.

## 4. Data — only if this page's layout needs some

If `TeamIndex` itself needs data, load it in the leaf's handler or a `Load`. But the more interesting question is **where the data lives**, because that determines how often it's fetched:

- **In the leaf** → fetched on every visit to this page. Right for page-specific, cheap, or always-volatile data.
- **In a segment** (`Load` + `TTL` + `Scoped`) → fetched once per TTL and available to _every_ page under that segment. Right for data several pages share, like the profile in the sidebar — fetched once, not per page.

The default here is "don't add a segment for data you don't have." The `dashboard` segment already exists and already loads the user; your new page can read it with `seg.Data(ctx, "dashboard")` for free.

## 5. Mutations — one line to stay correct

If a page writes data, the cache needs to know:

```go
r.Post("/team/invite", func(w http.ResponseWriter, r *http.Request) {
    // ...write...
    seg.Invalidate("team", seg.SessionID(w, r)) // this user's team data is stale
    w.Header().Set("HX-Trigger", `showToast`)
    w.WriteHeader(http.StatusOK)
})
```

Two decisions in that snippet, both "why":

- **Explicit invalidation, not a shorter TTL** — the handler that writes is the only one that knows what changed. Telling the cache directly keeps correctness in the same place as the write.
- **`HX-Trigger` instead of a fake swap** — the action has no visible result, so the response carries a toast event rather than fabricating HTML. (See the OOB doc.)

## What you did NOT have to write

| Concern                                 | Who provides it                                 |
| --------------------------------------- | ----------------------------------------------- |
| Full HTML document, `<head>`, `<title>` | `root` segment + renderer (`OOBTitle`)          |
| Sidebar, header, auth gating            | `dashboard` segment + `RequireAuth` middleware  |
| Profile data in the sidebar, cached     | `dashboard` segment's `Load` / `TTL` / `Scoped` |
| Only the new leaf sent over the wire    | mounted-stack diff in `seg.Page`                |
| Tab/browser title follows navigation    | `OOBTitle` appended by the renderer             |
| History, back button, deep links        | real URLs + server-side full render             |
| Opening the page as a modal             | `seg.Modalable` on the same route               |

## When you'd reach for more

- **Same URL, modal + full page** → use `seg.Modalable` (like the settings routes) instead of `seg.Page`. Same leaf, same route; the `X-Modal` header picks the destination.
- **Chrome that must update on mutation** (badge, tabs) → append an OOB component or swap it in the handler (see the OOB doc).
- **Per-user or global caching** → `Scoped` true/false on the segment.
