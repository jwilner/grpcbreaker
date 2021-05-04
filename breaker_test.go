package grpcbreaker

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestBreaker(t *testing.T) {
	errNope := errors.New("nope")
	circuitNope := func(ctx context.Context) error { return errNope }
	ctx := context.Background()

	t.Run("opens", func(t *testing.T) {
		b := newTestBreaker(t, FailThreshold(1))
		if err := b.call(ctx, circuitNope); !errors.Is(err, errNope) {
			t.Fatalf("wanted errNope but got %v", err)
		}
		if err := b.call(ctx, circuitNope); !errors.Is(err, ErrBreakerOpen) {
			t.Fatalf("wanted an ErrBreakerOpen but got %v", err)
		}
	})

	t.Run("halfOpens", func(t *testing.T) {
		b := newTestBreaker(t, FailThreshold(1), ResetTimeout(time.Microsecond*10))
		if err := b.call(ctx, circuitNope); !errors.Is(err, errNope) {
			t.Fatalf("wanted errNope but got %v", err)
		}
	})
}

type harness struct {
	*breaker

	events <-chan Event
}

func newTestBreaker(t *testing.T, opts ...Option) *harness {
	var s settings
	for _, o := range append([]Option{Predicate(func(error) bool { return true })}, opts...) {
		o(&s)
	}

	ch := make(chan struct{})
	evs := make(chan Event, 100)
	b := newBreaker(Key{}, deps{ch, evs}, s)
	go b.run()

	LogEvents(context.Background(), t.Logf, evs)

	t.Cleanup(func() { close(ch) })

	return &harness{b, evs}
}
