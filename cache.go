package grpcbreaker

import (
	"fmt"
	"sort"
	"strings"
	"sync"

	"google.golang.org/grpc"
)

// BreakerType enumerates the different sorts of breakers
//go:generate stringer -type=BreakerType
type BreakerType int

const (
	BreakerGlobal BreakerType = iota
	BreakerService
	BreakerMethod
	BreakerCallSite
)

type cache struct {
	m      sync.Map
	global *breaker
}

func newCache(deps deps, defaults []Option, g *GlobalOptionSet, optionSets ...*OptionSet) *cache {
	sort.Slice(optionSets, func(i, j int) bool {
		return optionSets[i].key.Name < optionSets[j].key.Name
	})

	var bc cache

	var stack []*breaker
	{
		var s settings
		for _, o := range append(defaults, g.options...) {
			o(&s)
		}
		bc.global = newBreaker(Key{}, deps, s)

		stack = append(stack, bc.global)
	}

	seen := make(map[Key]struct{}, len(optionSets))
	for _, os := range optionSets {
		if _, ok := seen[os.key]; ok {
			continue
		}
		seen[os.key] = struct{}{}

		last := stack[len(stack)-1] // should never asFail b/c global will prefix of all
		for !strings.HasPrefix(os.key.Name, last.Key.Name) {
			last.run()
			bc.m.Store(os.key, last)
			stack = stack[:len(stack)-1]
		}

		// last has the closest prefix of our node -- i.e. is the direct parent
		cp := last.settings
		for _, o := range os.options {
			o(&cp)
		}

		stack = append(stack, newBreaker(os.key, deps, cp))
	}

	for len(stack) > 0 {
		idx := len(stack) - 1

		go stack[idx].run()
		bc.m.Store(stack[idx].Key, stack[idx])
		stack = stack[:idx]
	}

	return &bc
}

func (bc *cache) resolve(method string, opts []grpc.CallOption) *breaker {
	var c *CallOption
	for _, co := range opts {
		if t, ok := co.(*CallOption); ok {
			c = t
		}
	}

	if c != nil {
		if loaded, ok := bc.m.Load(c.optionSet.key); ok {
			return loaded.(*breaker)
		}
		// init call site by loading method
		parent := bc.resolve(method, nil)

		cp := parent.settings
		for _, o := range c.optionSet.options {
			o(&cp)
		}

		return bc.loadOrStore(c.optionSet.key, newBreaker(c.optionSet.key, parent.deps, cp))
	}

	methodKey := Key{Type: BreakerMethod, Name: method}
	if m, ok := bc.load(methodKey); ok {
		return m
	}

	svcKey := Key{Type: BreakerService, Name: method[:strings.Index(method, "/")]}
	if s, ok := bc.load(svcKey); ok {
		// forward to method for next time
		return bc.loadOrStore(methodKey, s)
	}

	return bc.loadOrStore(methodKey, bc.loadOrStore(svcKey, bc.global))
}

func (bc *cache) load(key Key) (*breaker, bool) {
	v, ok := bc.m.Load(key)
	br, _ := v.(*breaker)
	return br, ok
}

func (bc *cache) loadOrStore(key Key, br *breaker) *breaker {
	stored, loaded := bc.m.LoadOrStore(key, br)
	br = stored.(*breaker)
	if !loaded {
		go br.run() // start since not already started
	}
	return br
}

// Key is the unique identifier of a breaker in grpcbreaker
type Key struct {
	Type BreakerType
	Name string
}

// String returns a friendly representation of the identifier
func (k Key) String() string {
	if k.Name == "" {
		return k.Type.String()
	}
	return fmt.Sprintf("%v|%v", k.Type, k.Name)
}
