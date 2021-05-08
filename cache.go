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
	// init the tree of Global, Service, and Method option sets
	// Options copy from Global -> Service -> Method; each level's name is always a prefix of the prior's, therefore
	// if we sort lexicographically, we'll always visit parent nodes first.
	var (
		bc    cache
		stack []*breaker
		last  *breaker
	)

	push := func(b *breaker) {
		stack = append(stack, b)
		last = b
	}

	popAndInit := func() {
		bc.m.Store(last.Key, last)
		go last.run()

		stack = stack[:len(stack)-1]

		if len(stack) > 0 {
			last = stack[len(stack)-1]
		}
	}

	// add in the global option set which is the defaults + explicit globals
	optionSets = append(optionSets, &OptionSet{options: append(defaults, g.options...)})

	// we'll traverse the tree according to the names -- global will be first, etc.
	sort.Slice(optionSets, func(i, j int) bool {
		return optionSets[i].key.Name < optionSets[j].key.Name
	})

	seen := make(map[Key]struct{}, len(optionSets))
	for _, os := range optionSets {
		if _, ok := seen[os.key]; ok {
			continue
		}
		seen[os.key] = struct{}{}

		var base settings
		if last != nil {
			for !strings.HasPrefix(os.key.Name, last.Key.Name) { // ascend tree
				popAndInit()
			}
			// last has the closest prefix of our node -- i.e. is the direct parent
			base = last.settings
		}
		for _, o := range os.options {
			o(&base)
		}
		push(newBreaker(os.key, deps, base))
	}

	for len(stack) > 0 {
		popAndInit()
	}

	bc.global = last // global is always last

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
		if b, ok := bc.m.Load(c.optionSet.key); ok {
			return b.(*breaker)
		}
		// init call site by loading method
		parent := bc.resolve(method, nil)

		cp := parent.settings
		for _, o := range c.optionSet.options {
			o(&cp)
		}

		b, loaded := bc.loadOrStore(c.optionSet.key, newBreaker(c.optionSet.key, parent.deps, cp))
		if !loaded {
			go b.run()
		}
		return b
	}

	methodKey := Key{Type: BreakerMethod, Name: method}
	if b, ok := bc.load(methodKey); ok {
		return b
	}

	idx := strings.Index(method[1:], "/") + 1
	if idx < 1 {
		b, _ := bc.loadOrStore(methodKey, bc.global)
		return b
	}

	svcKey := Key{Type: BreakerService, Name: method[:idx]}
	if s, ok := bc.load(svcKey); ok {
		// forward to method for next time
		b, _ := bc.loadOrStore(methodKey, s)
		return b
	}

	b, _ := bc.loadOrStore(svcKey, bc.global)
	b, _ = bc.loadOrStore(methodKey, b)
	return b
}

func (bc *cache) load(key Key) (*breaker, bool) {
	v, ok := bc.m.Load(key)
	br, _ := v.(*breaker)
	return br, ok
}

func (bc *cache) loadOrStore(key Key, br *breaker) (*breaker, bool) {
	actual, loaded := bc.m.LoadOrStore(key, br)
	return actual.(*breaker), loaded
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
