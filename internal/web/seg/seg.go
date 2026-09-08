// Package seg implements a server-driven "segment stack": layouts are
// segments (stable ID + outlet + optional loader). Middleware records the
// stack a route sits under; the renderer diffs it against the client's
// mounted segments (X-Mounted-Segments) and returns only the missing tail.
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
	headerMounted    = "X-Mounted-Segments"
	headerModal      = "X-Modal"
	headerModalOwner = "X-Modal-Owner"
	headerDropSegs   = "X-Drop-Segments"
)

type Segment struct {
	ID     string
	Render func(ctx context.Context, data any, child templ.Component) templ.Component
	// Load runs for every request under this segment; its result is cached per
	// the TTL/Scoped settings so the real fetch happens once.
	Load func(ctx context.Context) (any, error)
	// Decode rebuilds a cached value from its serialized form (required by
	// external caches like Redis that store JSON; ignored by the in-memory
	// cache, which keeps the live value).
	Decode func([]byte) (any, error)
	// TTL: how long loaded data stays cached; 0 = never cache.
	TTL time.Duration
	// Scoped: true keys the cache per session, false shares one entry across
	// all visitors.
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

func Path(ctx context.Context) string {
	p, _ := ctx.Value(keyPath).(string)
	return p
}

func InModal(ctx context.Context) bool {
	v, _ := ctx.Value(keyInModal).(bool)
	return v
}

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

// Use runs a segment's middleware on every request in the subtree, full load
// or fragment alike. Load is served from the cache, so the real fetch happens
// once per TTL, and the data is always in the request context for any leaf to
// read regardless of whether this response renders this segment's HTML.
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
// implementation; external backends (Redis) implement the same interface.
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

var DefaultCache Cache = &memoryCache{}

func cacheKey(seg Segment, sessionID string) string {
	if seg.Scoped {
		return "u:" + sessionID + ":" + seg.ID
	}
	return "g:" + seg.ID
}

// CacheKey returns the storage key for a segment under a session, so external
// backends (Redis) use the same key scheme as the in-memory one.
func CacheKey(seg Segment, sessionID string) string { return cacheKey(seg, sessionID) }
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

func Invalidate(segID, sessionID string) { DefaultCache.Invalidate(segID, sessionID) }
func InvalidateGlobal(segID string)      { DefaultCache.InvalidateGlobal(segID) }

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

func PageWith(w http.ResponseWriter, r *http.Request, title string, leaf templ.Component, extra ...templ.Component) {
	stack := StackFrom(r)
	if len(stack) == 0 {
		_ = leaf.Render(r.Context(), w)
		return
	}

	mounted := mountedSet(r)
	i := commonPrefixLen(stack, mounted)

	if !isHX(r) || i == 0 {
		renderFull(w, r, title, stack, leaf)
		return
	}
	renderTail(w, r, title, stack, i, leaf, extra)
}

// renderFull renders the whole tree. A fragment with no shared prefix forces
// a full reload (HX-Refresh) rather than rebuilding the body (which would
// destroy #outlet-root).
func renderFull(w http.ResponseWriter, r *http.Request, title string, stack []Segment, leaf templ.Component) {
	ids := stackIDs(stack)
	ctx := withTitle(withPath(withStackIDs(r.Context(), ids), r.URL.Path), title)

	w.Header().Set("HX-Trigger", fmt.Sprintf(`{"segments":%q}`, strings.Join(ids, ",")))

	if isHX(r) {
		w.Header().Set("HX-Refresh", "true")
		return
	}

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

// renderTail renders only the missing tail of the stack plus the leaf.
// Fragment navigations clear an open page-modal (ClearModal OOB); keeping a
// modal open is the job of responses that target #modal-root (Modalable).
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

// partial emits an htmx 4 partial that swaps body into target.
func partial(target, swap, body string) templ.Component {
	return templ.ComponentFunc(func(ctx context.Context, w io.Writer) error {
		_, err := io.WriteString(w, `<template hx type="partial" hx-target="`+target+`" hx-swap="`+swap+`">`+body+`</template>`)
		return err
	})
}

func OOBTitle(title string) templ.Component {
	return partial("title", "outerHTML", `<title>`+templ.EscapeString(title)+`</title>`)
}

func ClearModal() templ.Component {
	return partial("#modal-root", "innerHTML", ``)
}

func NotifBadge(n int) templ.Component {
	if n > 0 {
		return partial("#notif-badge", "outerHTML", fmt.Sprintf(
			`<span id="notif-badge" class="absolute -right-1 -top-1 grid h-4 min-w-4 place-items-center rounded-full bg-foreground px-1 text-[10px] font-semibold text-background">%d</span>`, n))
	}
	return partial("#notif-badge", "outerHTML", `<span id="notif-badge" class="hidden" aria-hidden="true"></span>`)
}

type ModalOpts struct {
	CloseX   bool
	Esc      bool
	Backdrop bool
	Expand   bool
	Size     string // "", "sm", "md", "lg"
}

type ShellFunc func(ctx context.Context, opts ModalOpts, owner, current, parent string, child templ.Component) templ.Component

type LeafFunc func(r *http.Request) (title string, leaf templ.Component)

const (
	ModalPage = "page"
	ModalKeep = "keep"
	ModalNone = "none"
)

// Header helpers for links: "page" opens a page-modal, "keep" stays inside
// the open shell, "none" (expand) renders the full page.
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

// Modalable serves the same URL per the X-Modal header: "" renders through
// the full stack, "page" wraps the leaf in a modal shell at #modal-root,
// "keep" returns just the leaf into the open shell's outlet, "none" renders
// the full page. oob lists out-of-band components appended to responses.
func Modalable(shell ShellFunc, leaf LeafFunc, opts ModalOpts, oob ...templ.Component) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		marker := ModalMarker(r)
		title, comp := leaf(r)

		switch marker {
		case "":
			PageWith(w, r, title, comp, oob...)
			return

		case ModalNone:
			renderExpanded(w, r, title, comp)
			return

		case ModalKeep:
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

		// Close/back should return to the page the modal was opened from
		// (HX-Current-URL); on a direct modal-URL load, fall back to the parent.
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
