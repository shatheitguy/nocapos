package imageupdates

import (
	"context"
	"errors"
	"math/rand/v2"
	"sort"
	"strings"
	"sync"
	"time"
)

// Image is one container's image: the app it belongs to, the reference it
// was created from (repo:tag) and the local image id.
type Image struct {
	App     string
	Ref     string
	ImageID string
}

// Digester reads a tag's digest from its registry.
type Digester interface {
	Digest(ctx context.Context, r Ref) (string, error)
}

// Update is a newer image for one of an app's references.
type Update struct {
	Ref    string `json:"ref"`
	Digest string `json:"digest"`
}

// Checker keeps which apps have newer images.
type Checker struct {
	// Images lists the apps' containers' images.
	Images func(ctx context.Context) ([]Image, error)
	// RepoDigests lists the digests a local image was pulled with ("repo@sha256:…").
	RepoDigests func(ctx context.Context, imageID string) ([]string, error)
	Registry    Digester
	// Gap spaces out requests to the registries.
	Gap time.Duration
	// OnFound is told about apps with a newer image found in this check
	// (not ones already known).
	OnFound func(app string, updates []Update)

	now   func() time.Time
	sleep func(ctx context.Context, d time.Duration) error

	run     sync.Mutex // one check at a time
	mu      sync.Mutex
	updates map[string][]Update // app → newer images
	waitTil map[string]time.Time
	fails   map[string]int
	checked time.Time
}

// New is a checker with the defaults: one request every 2 seconds at most.
func New(images func(context.Context) ([]Image, error), repoDigests func(context.Context, string) ([]string, error), reg Digester) *Checker {
	return &Checker{Images: images, RepoDigests: repoDigests, Registry: reg, Gap: 2 * time.Second}
}

func (c *Checker) clock() time.Time {
	if c.now != nil {
		return c.now()
	}
	return time.Now()
}

func (c *Checker) pause(ctx context.Context, d time.Duration) error {
	if c.sleep != nil {
		return c.sleep(ctx, d)
	}
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// Updates returns an app's newer images (none when it's up to date).
func (c *Checker) Updates(app string) []Update {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]Update(nil), c.updates[app]...)
}

// Has reports whether an app has a newer image.
func (c *Checker) Has(app string) bool { return len(c.Updates(app)) > 0 }

// Forget drops an app's updates (it was just updated or removed).
func (c *Checker) Forget(app string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.updates, app)
}

// Checked is when the last check finished.
func (c *Checker) Checked() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.checked
}

// Backoff after errors: a rate limit waits at least 30 minutes (doubling,
// up to a day, or longer if the registry says so); other errors 15 minutes
// (doubling, up to 6 hours).
func backoff(err error, fails int) time.Duration {
	if fails > 6 {
		fails = 6
	}
	var rl *RateLimitError
	if errors.As(err, &rl) {
		d := 30 * time.Minute << fails
		if d > 24*time.Hour {
			d = 24 * time.Hour
		}
		if rl.RetryAfter > d {
			d = rl.RetryAfter
		}
		return d
	}
	d := 15 * time.Minute << fails
	if d > 6*time.Hour {
		d = 6 * time.Hour
	}
	return d
}

// Check asks the registries about every app image once. A registry that is
// backing off is skipped (its apps keep what was known). It fails only when
// the images can't be listed.
func (c *Checker) Check(ctx context.Context) error {
	c.run.Lock()
	defer c.run.Unlock()
	imgs, err := c.Images(ctx)
	if err != nil {
		return err
	}
	// Each reference once, whichever apps use it.
	type use struct {
		ref    Ref
		apps   map[string]bool
		images map[string]bool
	}
	uses := map[string]*use{}
	var order []string
	apps := map[string]bool{}
	for _, im := range imgs {
		apps[im.App] = true
		r, ok := ParseRef(im.Ref)
		if !ok {
			continue
		}
		u := uses[im.Ref]
		if u == nil {
			u = &use{ref: r, apps: map[string]bool{}, images: map[string]bool{}}
			uses[im.Ref] = u
			order = append(order, im.Ref)
		}
		u.apps[im.App] = true
		if im.ImageID != "" {
			u.images[im.ImageID] = true
		}
	}
	sort.Strings(order)

	found := map[string][]Update{} // app → newer images seen now
	skipped := map[string]bool{}   // apps with a reference not checked this time
	first := true
	for _, key := range order {
		u := uses[key]
		host := u.ref.Registry
		c.mu.Lock()
		wait := c.waitTil[host]
		c.mu.Unlock()
		if c.clock().Before(wait) {
			for a := range u.apps {
				skipped[a] = true
			}
			continue
		}
		local := map[string]bool{}
		for id := range u.images {
			ds, err := c.RepoDigests(ctx, id)
			if err != nil {
				continue
			}
			for _, d := range ds {
				if i := strings.LastIndex(d, "@"); i >= 0 {
					local[d[i+1:]] = true
				}
			}
		}
		if len(local) == 0 {
			// Built here or loaded from a file: nothing to compare with.
			continue
		}
		if !first {
			if err := c.pause(ctx, c.Gap); err != nil {
				return err
			}
		}
		first = false
		remote, err := c.Registry.Digest(ctx, u.ref)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			c.mu.Lock()
			if c.waitTil == nil {
				c.waitTil, c.fails = map[string]time.Time{}, map[string]int{}
			}
			if !errors.Is(err, ErrNotFound) {
				c.waitTil[host] = c.clock().Add(backoff(err, c.fails[host]))
				c.fails[host]++
			}
			c.mu.Unlock()
			for a := range u.apps {
				skipped[a] = true
			}
			continue
		}
		c.mu.Lock()
		delete(c.fails, host)
		c.mu.Unlock()
		if !local[remote] {
			for a := range u.apps {
				found[a] = append(found[a], Update{Ref: key, Digest: remote})
			}
		}
	}

	c.mu.Lock()
	if c.updates == nil {
		c.updates = map[string][]Update{}
	}
	news := map[string][]Update{}
	for a := range c.updates {
		if !apps[a] {
			delete(c.updates, a) // removed
		}
	}
	for a := range apps {
		now := found[a]
		if skipped[a] {
			// Keep what was known about the references not checked now.
			for _, old := range c.updates[a] {
				if !hasRef(now, old.Ref) {
					now = append(now, old)
				}
			}
		}
		for _, u := range found[a] {
			if !hasUpdate(c.updates[a], u) {
				news[a] = append(news[a], u)
			}
		}
		if len(now) > 0 {
			c.updates[a] = now
		} else {
			delete(c.updates, a)
		}
	}
	c.checked = c.clock()
	onFound := c.OnFound
	c.mu.Unlock()
	if onFound != nil {
		names := make([]string, 0, len(news))
		for a := range news {
			names = append(names, a)
		}
		sort.Strings(names)
		for _, a := range names {
			onFound(a, news[a])
		}
	}
	return nil
}

func hasRef(list []Update, ref string) bool {
	for _, u := range list {
		if u.Ref == ref {
			return true
		}
	}
	return false
}

func hasUpdate(list []Update, u Update) bool {
	for _, x := range list {
		if x == u {
			return true
		}
	}
	return false
}

// Jitter spreads d by up to ±frac of it, so servers don't all ask at once.
func Jitter(d time.Duration, frac float64) time.Duration {
	span := time.Duration(float64(d) * frac)
	if span <= 0 {
		return d
	}
	return d - span + time.Duration(rand.Int64N(int64(2*span)))
}

// Run checks a few minutes after start, then about every `every`; after a
// failed check (Docker down, say) it tries again in half an hour.
func (c *Checker) Run(ctx context.Context, first, every time.Duration) {
	wait := first + time.Duration(rand.Int64N(int64(3*time.Minute)))
	for {
		if err := c.pause(ctx, wait); err != nil {
			return
		}
		if err := c.Check(ctx); err != nil {
			if ctx.Err() != nil {
				return
			}
			wait = Jitter(30*time.Minute, 0.2)
			continue
		}
		wait = Jitter(every, 0.1)
	}
}
