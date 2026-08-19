// Package seg implements a server-driven "segment stack": every layout is a
// segment (stable ID + outlet + optional loader). Middleware records the
// stack a route sits under; the renderer diffs it against the client's
// mounted segments (X-Mounted-Segments header) and returns only the missing
// tail as an htmx fragment. The whole "framework" is one middleware, one
// renderer, one modal wrapper; everything else is plain templ + chi.
package seg

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/a-h/templ"
)

const (
	headerMounted   = "X-Mounted-Segments"
	headerModal     = "X-Modal"
	headerModalOwner = "X-Modal-Owner"
	headerDropSegs  = "X-Drop-Segments"
)

// Segment is one level of the layout tree.
type Segment struct {
	ID string
	// Render wraps child in this segment's layout. data is this segment's
	// cached Load result, available on every request in the subtree.
	Render func(ctx context.Context, data any, child templ.Component) templ.Component
	// Load runs for every request under this segment (see Use); its result is
	// cached per the TTL/Scoped settings so the real fetch happens once.
	Load func(ctx context.Context) (any, error)
	// TTL is how long the loaded data stays cached; 0 = never cache (always
	// fetch on every request).
	TTL time.Duration
	// Scoped: true keys the cache per session (user data), false shares one
	// entry across all visitors (global config, feature flags, ...).
	Scoped bool
}

type ctxKey int

const (
	keyStack ctxKey = iota
	keyData
	keyTitle
	keyStackIDs
	keyPath
	keyInModal
)

func pushSegment(ctx context.Context, s Segment) context.Context {
	stack, _ := ctx.Value(keyStack).([]Segment)
	return context.WithValue(ctx, keyStack, append(stack, s))
}

func withData(ctx context.Context, id string, data any) context.Context {
	m, _ := ctx.Value(keyData).(map[string]any)
	if m == nil {
		m = make(map[string]any)
	}
	m[id] = data
	return context.WithValue(ctx, keyData, m)
}

func withTitle(ctx context.Context, title string) context.Context {
	return context.WithValue(ctx, keyTitle, title)
}

func withStackIDs(ctx context.Context, ids []string) context.Context {
	return context.WithValue(ctx, keyStackIDs, ids)
}

func withPath(ctx context.Context, p string) context.Context {
	return context.WithValue(ctx, keyPath, p)
}

func withInModal(ctx context.Context) context.Context {
	return context.WithValue(ctx, keyInModal, true)
}

func StackFrom(r *http.Request) []Segment {
	s, _ := r.Context().Value(keyStack).([]Segment)
	return s
}

func Data(ctx context.Context, id string) any {
	m, _ := ctx.Value(keyData).(map[string]any)
	return m[id]
}

// Path returns the request path of the current render, so components can
// derive active state (e.g. tab highlights) from the URL server-side.
func Path(ctx context.Context) string {
	p, _ := ctx.Value(keyPath).(string)
	return p
}

// InModal reports whether the current render is inside a modal shell.
func InModal(ctx context.Context) bool {
	v, _ := ctx.Value(keyInModal).(bool)
	return v
}

// ModalOwner returns the segment the current modal belongs to: the innermost
// segment of the stack ("" when not in a modal).
func ModalOwner(ctx context.Context) string {
	if !InModal(ctx) {
		return ""
	}
	stack, _ := ctx.Value(keyStack).([]Segment)
	if len(stack) == 0 {
		return ""
	}
	return stack[len(stack)-1].ID
}

func PageTitle(ctx context.Context) string {
	t, _ := ctx.Value(keyTitle).(string)
	return t
}

func StackIDs(ctx context.Context) []string {
	ids, _ := ctx.Value(keyStackIDs).([]string)
	return ids
}

// Use runs a segment's middleware for every request in the subtree,
// unconditionally — full page load or fragment alike. Its Load result is
// served from (and stored to) the cache, so the real fetch happens once per
// TTL, but the data is always in the request context for any leaf to read,
// regardless of whether this request renders this segment's HTML. This is
// what lets a deeply nested leaf assume its ancestors' data without knowing
// whether the response will be a full render or a renderTail fragment.
func Use(s Segment) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if s.Load != nil {
				sessionID := SessionID(w, r)
				data, err := DefaultCache.Get(r.Context(), cacheKey(s, sessionID), s)
				if err != nil {
					http.Error(w, "load segment "+s.ID+": "+err.Error(), http.StatusInternalServerError)
					return
				}
				r = r.WithContext(withData(r.Context(), s.ID, data))
			}
			next.ServeHTTP(w, r.WithContext(pushSegment(r.Context(), s)))
		})
	}
}

// Cache stores segment Load results. The in-memory sync.Map is the reference
// implementation; swap the backend (Redis, ...) behind the same interface when
// scaling past one process. Correctness comes from the expiresAt check at
// read time — a background sweeper is only a memory optimization.
type Cache interface {
	Get(ctx context.Context, key string, seg Segment) (any, error)
	Invalidate(segID, sessionID string)
	InvalidateGlobal(segID string)
}

type cacheEntry struct {
	data      any
	expiresAt time.Time
}

type memoryCache struct {
	store sync.Map
}

// DefaultCache is the process-wide cache used by the middleware.
var DefaultCache Cache = &memoryCache{}

func cacheKey(seg Segment, sessionID string) string {
	if seg.Scoped {
		return "u:" + sessionID + ":" + seg.ID
	}
	return "g:" + seg.ID
}

func (c *memoryCache) Get(ctx context.Context, key string, seg Segment) (any, error) {
	if e, ok := c.store.Load(key); ok {
		entry := e.(cacheEntry)
		if time.Now().Before(entry.expiresAt) {
			return entry.data, nil
		}
	}
	val, err := seg.Load(ctx)
	if err != nil {
		return nil, err
	}
	if seg.TTL > 0 {
		c.store.Store(key, cacheEntry{val, time.Now().Add(seg.TTL)})
	}
	return val, nil
}

func (c *memoryCache) Invalidate(segID, sessionID string) {
	c.store.Delete("u:" + sessionID + ":" + segID)
}

func (c *memoryCache) InvalidateGlobal(segID string) {
	c.store.Delete("g:" + segID)
}

// Invalidate and InvalidateGlobal are package-level helpers over DefaultCache,
// for use from mutation handlers (same request that performs the write).
func Invalidate(segID, sessionID string)     { DefaultCache.Invalidate(segID, sessionID) }
func InvalidateGlobal(segID string)           { DefaultCache.InvalidateGlobal(segID) }

// SessionID returns a stable per-browser id, creating one on first visit.
func SessionID(w http.ResponseWriter, r *http.Request) string {
	const name = "seg_session"
	if c, err := r.Cookie(name); err == nil && c.Value != "" {
		return c.Value
	}
	id := fmt.Sprintf("%d-%d", time.Now().UnixNano(), len(r.Header.Get("User-Agent")))
	http.SetCookie(w, &http.Cookie{Name: name, Value: id, Path: "/", HttpOnly: true})
	return id
}

func Page(w http.ResponseWriter, r *http.Request, title string, leaf templ.Component) {
	PageWith(w, r, title, leaf)
}

// PageWith is Page plus extra out-of-band components (e.g. tab chrome).
func PageWith(w http.ResponseWriter, r *http.Request, title string, leaf templ.Component, extra ...templ.Component) {
	stack := StackFrom(r)
	if len(stack) == 0 {
		// bare handler with no segments: just the leaf, no htmx wiring
		_ = leaf.Render(r.Context(), w)
		return
	}

	mounted := mountedSet(r)
	i := commonPrefixLen(stack, mounted) // client already has stack[:i]

	if !isHX(r) || i == 0 {
		renderFull(w, r, title, stack, leaf)
		return
	}
	renderTail(w, r, title, stack, i, leaf, extra)
}

// renderFull renders the whole tree. A fragment with no shared prefix forces
// a full page reload (HX-Refresh) rather than rebuilding the body, which
// would destroy #outlet-root.
func renderFull(w http.ResponseWriter, r *http.Request, title string, stack []Segment, leaf templ.Component) {
	ids := stackIDs(stack)
	ctx := withTitle(withPath(withStackIDs(r.Context(), ids), r.URL.Path), title)

	w.Header().Set("HX-Trigger", fmt.Sprintf(`{"segments":%q}`, strings.Join(ids, ",")))

	if isHX(r) {
		w.Header().Set("HX-Refresh", "true")
		return
	}

	// full document load
	comp := leaf
	for j := len(stack) - 1; j >= 0; j-- {
		s := stack[j]
		comp = s.Render(ctx, Data(ctx, s.ID), comp)
	}
	_ = comp.Render(ctx, w)
}

// renderExpanded leaves a page-modal and renders the full page into
// #outlet-root, ignoring the client's mounted prefix (the modal's segments
// are reported mounted but only exist inside the modal).
func renderExpanded(w http.ResponseWriter, r *http.Request, title string, leaf templ.Component) {
	stack := StackFrom(r)
	ids := stackIDs(stack)
	ctx := withTitle(withPath(withStackIDs(r.Context(), ids), r.URL.Path), title)

	w.Header().Set("HX-Retarget", "#outlet-root")
	w.Header().Set("HX-Reswap", "innerHTML")
	w.Header().Set("HX-Push-Url", r.URL.Path)
	w.Header().Set("HX-Trigger", fmt.Sprintf(`{"segments":%q}`, strings.Join(ids, ",")))

	comp := leaf
	for j := len(stack) - 1; j >= 0; j-- {
		s := stack[j]
		comp = s.Render(ctx, Data(ctx, s.ID), comp)
	}

	parts := []templ.Component{comp}
	if title != "" {
		parts = append(parts, OOBTitle(title))
	}
	parts = append(parts, ClearModal())
	_ = templ.Join(parts...).Render(ctx, w)
}

// renderTail renders only the missing tail of the stack plus the leaf, and
// tells htmx where to put it. Fragment navigations clear an open page-modal
// (ClearModal OOB); keeping a modal open is solely the job of responses that
// target #modal-root (Modalable).
func renderTail(w http.ResponseWriter, r *http.Request, title string, stack []Segment, i int, leaf templ.Component, extra []templ.Component) {
	ids := stackIDs(stack)
	ctx := withPath(withStackIDs(r.Context(), ids), r.URL.Path)

	w.Header().Set("HX-Retarget", "#outlet-"+stack[i-1].ID)
	w.Header().Set("HX-Reswap", "innerHTML")
	w.Header().Set("HX-Push-Url", r.URL.Path)
	w.Header().Set("HX-Trigger", fmt.Sprintf(`{"segments":%q}`, strings.Join(ids, ",")))

	comp := leaf
	for j := len(stack) - 1; j >= i; j-- {
		s := stack[j]
		comp = s.Render(ctx, Data(ctx, s.ID), comp)
	}

	parts := []templ.Component{comp}
	if title != "" {
		parts = append(parts, OOBTitle(title))
	}
	parts = append(parts, ClearModal())
	parts = append(parts, extra...)
	_ = templ.Join(parts...).Render(ctx, w)
}

func OOBTitle(title string) templ.Component {
	return templ.ComponentFunc(func(ctx context.Context, w io.Writer) error {
		_, err := io.WriteString(w, "<title hx-swap-oob=\"true\">"+templ.EscapeString(title)+"</title>")
		return err
	})
}

// ClearModal is included in every fragment navigation so leaving a modal
// cleans up.
func ClearModal() templ.Component {
	return templ.ComponentFunc(func(ctx context.Context, w io.Writer) error {
		_, err := io.WriteString(w, `<div id="modal-root" hx-swap-oob="innerHTML"></div>`)
		return err
	})
}

// NotifBadge updates the dashboard badge out-of-band; a count of zero swaps
// in the hidden variant so no "0" bubble is left.
func NotifBadge(n int) templ.Component {
	return templ.ComponentFunc(func(ctx context.Context, w io.Writer) error {
		if n > 0 {
			_, err := io.WriteString(w, fmt.Sprintf(
				`<span id="notif-badge" hx-swap-oob="true" class="absolute -right-1 -top-1 grid h-4 min-w-4 place-items-center rounded-full bg-foreground px-1 text-[10px] font-semibold text-background">%d</span>`, n))
			return err
		}
		_, err := io.WriteString(w, `<span id="notif-badge" class="hidden" hx-swap-oob="true" aria-hidden="true"></span>`)
		return err
	})
}

// ModalOpts describes how a page-modal shell is drawn.
type ModalOpts struct {
	CloseX   bool   // show the × close button
	Esc      bool   // Escape closes
	Backdrop bool   // clicking the backdrop closes
	Expand   bool   // show the expand-to-page button
	Size     string // "", "sm", "md", "lg"
}

type ShellFunc func(ctx context.Context, opts ModalOpts, owner, current, parent string, child templ.Component) templ.Component

type LeafFunc func(r *http.Request) (title string, leaf templ.Component)

// X-Modal values: "page" opens a page-modal, "keep" stays inside the open
// shell (leaf-only swap), "none" expands to the full page.
const (
	ModalPage = "page"
	ModalKeep = "keep"
	ModalNone = "none"
)

// Header helpers for links. ModalNoneHeader also names the segment to drop
// from the mounted stack (it only exists inside the modal); ModalKeepHeader
// is baked in by the server, so the client reports no modal state.
func PageModalHeader() string { return `{"X-Modal":"page"}` }
func ModalNoneHeader(owner string) string {
	return `{"X-Modal":"none","X-Drop-Segments":"` + owner + `"}`
}
func ModalKeepHeader(owner string) string {
	return `{"X-Modal":"keep","X-Modal-Owner":"` + owner + `"}`
}

func ModalMarker(r *http.Request) string {
	return r.Header.Get(headerModal)
}

// Modalable serves the same URL differently per the X-Modal header: "" renders
// through the full stack, "page" wraps the leaf in a modal shell at
// #modal-root, "keep" returns just the leaf into the open shell's outlet,
// "none" renders the full page. oob lists out-of-band components appended so
// on-screen chrome stays fresh.
func Modalable(shell ShellFunc, leaf LeafFunc, opts ModalOpts, oob ...templ.Component) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		marker := ModalMarker(r)
		title, comp := leaf(r)

		switch marker {
		case "":
			// Full-page navigation: render through the stack and OOB-refresh
			// chrome (tabs) so the active state updates with the URL.
			PageWith(w, r, title, comp, oob...)
			return

		case ModalNone:
			renderExpanded(w, r, title, comp)
			return

		case ModalKeep:
			// owner comes from X-Modal-Owner, baked into the link by the server
			// at render time (seg.ModalOwner) — not client-reported state.
			owner := r.Header.Get(headerModalOwner)
			w.Header().Set("HX-Retarget", "#modal-root [data-page-modal] #outlet-"+owner)
			w.Header().Set("HX-Reswap", "innerHTML")
			w.Header().Set("HX-Push-Url", r.URL.Path)
			w.Header().Set("HX-Trigger", `{"segments":"`+strings.Join(stackIDs(StackFrom(r)), ",")+`"}`)
			// OOB chrome (tabs) must render with the in-modal context so its
			// links keep targeting the modal.
			ctx := withInModal(withPath(r.Context(), r.URL.Path))
			parts := []templ.Component{comp}
			if title != "" {
				parts = append(parts, OOBTitle(title))
			}
			parts = append(parts, oob...)
			_ = templ.Join(parts...).Render(ctx, w)
			return
		}

		// "page": wrap the leaf in the innermost segment's layout so the modal
		// looks like the page it belongs to (tabs, chrome).
		owner := ""
		ctx := withInModal(withPath(r.Context(), r.URL.Path))
		if stack := StackFrom(r); len(stack) > 0 {
			last := stack[len(stack)-1]
			owner = last.ID
			comp = last.Render(ctx, Data(ctx, last.ID), comp)
		}

		// "Back"/close should return to the page the modal was opened from.
		// htmx sends HX-Current-URL on every request; on a direct modal-URL
		// load it's absent, so fall back to the URL parent.
		parent := r.Header.Get("HX-Current-URL")
		if parent == "" {
			parent = parentURL(r.URL.Path)
		}

		w.Header().Set("HX-Retarget", "#modal-root")
		w.Header().Set("HX-Reswap", "innerHTML")
		if marker == ModalPage {
			w.Header().Set("HX-Push-Url", r.URL.Path)
		}
		_ = shell(ctx, opts, owner, r.URL.Path, parent, comp).Render(ctx, w)
	}
}

func parentURL(p string) string {
	if p == "" || p == "/" {
		return "/"
	}
	d := path.Dir(p)
	if d == "." || d == "/" {
		return "/"
	}
	return d
}

func isHX(r *http.Request) bool {
	return r.Header.Get("HX-Request") == "true"
}

func mountedSet(r *http.Request) map[string]bool {
	set := make(map[string]bool)
	for _, id := range strings.Split(r.Header.Get(headerMounted), ",") {
		if id != "" {
			set[id] = true
		}
	}
	return set
}

func commonPrefixLen(stack []Segment, mounted map[string]bool) int {
	i := 0
	for i < len(stack) && mounted[stack[i].ID] {
		i++
	}
	return i
}

func stackIDs(stack []Segment) []string {
	ids := make([]string, len(stack))
	for i, s := range stack {
		ids[i] = s.ID
	}
	return ids
}
