//go:build !go1.25

package lifecycle

// Used only by the documented offline bootstrap checks. Published builds have
// a Go 1.27 baseline and use sync.WaitGroup.Go in go125.go.
func (g *Group) Go(f func()) { g.wg.Add(1); go func() { defer g.wg.Done(); f() }() }
