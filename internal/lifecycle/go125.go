//go:build go1.25

package lifecycle

func (g *Group) Go(f func()) { g.wg.Go(f) }
