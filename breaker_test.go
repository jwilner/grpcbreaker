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
	circuitOK := func(ctx context.Context) error { return nil }
	ctx := context.Background()

	requireErr := func(t *testing.T, expected, err error) {
		t.Helper()
		if !errors.Is(err, expected) {
			t.Fatalf("wanted %v but got %v", expected, err)
		}
	}

	requireNoErr := func(t *testing.T, err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("wanted no err but got %v", err)
		}
	}

	t.Run("opens", func(t *testing.T) {
		b := newTestBreaker(t, FailThreshold(1))
		requireErr(t, errNope, b.call(ctx, circuitNope))
		b.assertSequence(Closed, Open)
		requireErr(t, ErrBreakerOpen, b.call(ctx, circuitOK))
	})

	t.Run("half open success", func(t *testing.T) {
		b := newTestBreaker(t, FailThreshold(1), ResetTimeout(time.Microsecond*10))
		requireErr(t, errNope, b.call(ctx, circuitNope))
		b.assertSequence(Closed, Open, HalfOpen)
		requireNoErr(t, b.call(ctx, circuitOK))
		b.assertSequence(HalfOpen, Closed)
	})

	t.Run("half open fail", func(t *testing.T) {
		b := newTestBreaker(t, FailThreshold(1), ResetTimeout(time.Microsecond*10))
		requireErr(t, errNope, b.call(ctx, circuitNope))
		b.assertSequence(Closed, Open, HalfOpen)
		requireErr(t, errNope, b.call(ctx, circuitNope))
		b.assertSequence(HalfOpen, Open, HalfOpen) // final half open is because of short reset timeout
	})

	t.Run("consecutive failures below threshold", func(t *testing.T) {
		b := newTestBreaker(t, FailThreshold(3))
		requireErr(t, errNope, b.call(ctx, circuitNope))
		requireErr(t, errNope, b.call(ctx, circuitNope))
		b.assertNoChange()
		requireErr(t, errNope, b.call(ctx, circuitNope))
		b.assertSequence(Closed, Open)
	})
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

	t.Cleanup(func() { close(ch) })

	return &harness{b, t, evs}
}

type harness struct {
	*breaker

	t *testing.T

	events <-chan Event
}

func (h *harness) assertNoChange() {
	h.t.Helper()

	delay := time.After(100 * time.Microsecond)

	for {
		select {
		case ev := <-h.events:
			t, ok := ev.(StateEvent)
			if !ok || !t.Transition() {
				continue
			}
			h.t.Fatalf("Wanted no change but got transition %v->%v", t.Old.State(), t.New.State())
		case <-delay:
			return
		}
	}
}

func (h *harness) assertSequence(start State, states ...State) {
	h.t.Helper()

	timeout := time.After(100 * time.Microsecond)

	current := start
	for len(states) > 0 {
		next := states[0]
		select {
		case ev := <-h.events:
			t, ok := ev.(StateEvent)
			if !ok || !t.Transition() {
				continue
			}
			if t.Old.State() != current || t.New.State() != next {
				h.t.Fatalf("Expected %v->%v but got %v->%v", current, next, t.Old.State(), t.New.State())
			}
			current = next
			states = states[1:]
		case <-timeout:
			h.t.Fatalf("Timed out waiting for state event")
		}
	}
}
