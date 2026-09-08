package lifecycle

import "sync"

type Group struct{ wg sync.WaitGroup }

func (g *Group) Wait() { g.wg.Wait() }
