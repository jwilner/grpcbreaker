package grpcbreaker

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"google.golang.org/grpc"
)

func Test_cache_resolve(t *testing.T) {
	ch := make(chan struct{})
	defer func() { close(ch) }()

	type invok struct {
		method string
		opts   []grpc.CallOption

		// note that we can't actually compare the 'predicate' function here, so instead we use the sentinel error
		// mechanism -- if not null, both functions should match the sentinel error
		expectedKey      Key
		expectedSettings settings
		sentinelError    error
	}

	sentinelErr := errors.New("foo")
	pred := func(err error) bool { return err == sentinelErr }

	testCache := func(g *GlobalOptionSet, optionSets ...*OptionSet) *cache {
		return newCache(deps{closeCh: ch}, nil, g, optionSets...)
	}

	for _, tt := range []struct {
		name        string
		cache       *cache
		invocations []invok
	}{
		{
			name:  "just global",
			cache: testCache(Global(ResetTimeout(10*time.Minute), Predicate(pred))),
			invocations: []invok{
				{
					method:           "foo/Get",
					expectedKey:      Key{Type: BreakerGlobal},
					expectedSettings: settings{reset: 10 * time.Minute, predicate: pred},
					sentinelError:    sentinelErr,
				},
			},
		},
		{
			name: "service level",
			cache: testCache(
				Global(ResetTimeout(10*time.Minute), Predicate(pred)),
				Service("foo", FailThreshold(10)),
			),
			invocations: []invok{
				{
					method:           "foo/Get",
					expectedKey:      Key{Type: BreakerService, Name: "foo"},
					expectedSettings: settings{reset: 10 * time.Minute, failThreshold: 10, predicate: pred},
				},
				{
					method:           "foo/Create",
					expectedKey:      Key{Type: BreakerService, Name: "foo"},
					expectedSettings: settings{reset: 10 * time.Minute, failThreshold: 10, predicate: pred},
				},
				{
					method:           "notfoo/blah",
					expectedKey:      Key{Type: BreakerGlobal},
					expectedSettings: settings{reset: 10 * time.Minute, predicate: pred},
				},
			},
		},
		{
			name: "method level",
			cache: testCache(
				Global(ResetTimeout(10*time.Minute)),
				Service("foo", FailThreshold(10)),
				Method("foo/Get", ResetThreshold(22)),
			),
			invocations: []invok{
				{
					method:           "foo/Get",
					expectedKey:      Key{Type: BreakerMethod, Name: "foo/Get"},
					expectedSettings: settings{reset: 10 * time.Minute, failThreshold: 10, resetThreshold: 22},
				},
				{
					method:           "foo/Create",
					expectedKey:      Key{Type: BreakerService, Name: "foo"},
					expectedSettings: settings{reset: 10 * time.Minute, failThreshold: 10},
				},
				{
					method:           "notfoo/blah",
					expectedKey:      Key{Type: BreakerGlobal},
					expectedSettings: settings{reset: 10 * time.Minute},
				},
			},
		},
		{
			name: "callsite override",
			cache: testCache(
				Global(ResetTimeout(10*time.Minute)),
				Service("foo", FailThreshold(10)),
				Method("foo/Get", ResetThreshold(22), Predicate(pred)),
			),
			invocations: []invok{
				{
					method:      "foo/Get",
					expectedKey: Key{Type: BreakerMethod, Name: "foo/Get"},
					expectedSettings: settings{
						reset:          10 * time.Minute,
						failThreshold:  10,
						resetThreshold: 22,
						predicate:      pred,
					},
					sentinelError: sentinelErr,
				},
				{
					method:      "foo/Get",
					opts:        []grpc.CallOption{CallSite("bizbaz", FailThreshold(11))},
					expectedKey: Key{Type: BreakerCallSite, Name: "bizbaz"},
					expectedSettings: settings{
						reset:          10 * time.Minute,
						failThreshold:  11,
						resetThreshold: 22,
						predicate:      pred,
					},
					sentinelError: sentinelErr,
				},
				{
					method:           "foo/Create",
					expectedKey:      Key{Type: BreakerService, Name: "foo"},
					expectedSettings: settings{reset: 10 * time.Minute, failThreshold: 10},
				},
				{
					method:           "notfoo/blah",
					expectedKey:      Key{Type: BreakerGlobal},
					expectedSettings: settings{reset: 10 * time.Minute},
				},
			},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			for _, i := range tt.invocations {
				t.Run(i.method, func(t *testing.T) {
					b := tt.cache.resolve(i.method, i.opts)
					if b.Key != i.expectedKey {
						t.Errorf("expected key %+v but got %+v", i.expectedKey, b.Key)
					}

					// copy out predicates and then null out refs before comparing settings
					p, expP := b.settings.predicate, i.expectedSettings.predicate
					b.settings.predicate, i.expectedSettings.predicate = nil, nil
					if !reflect.DeepEqual(b.settings, i.expectedSettings) {
						t.Errorf("expected settings %+v but got %+v", i.expectedSettings, b.settings)
					}
					b.settings.predicate, i.expectedSettings.predicate = p, expP

					if (p == nil) != (expP == nil) {
						t.Errorf("expected predicate nullity to be %v but got %v", expP != nil, p != nil)
					} else if p != nil && (p(i.sentinelError) != expP(i.sentinelError)) {
						t.Errorf("expected both predicates to match %v but they did not", i.sentinelError)
					}
				})
			}
		})
	}
}
