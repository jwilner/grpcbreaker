package grpcbreaker

import (
	"context"
	"time"
)

// StateEvent describes the current state of the breaker
type StateEvent struct {
	Key
	Old, New                         GenState
	Published, LastFail, ResetMoment time.Time
	Fails, Passes                    int
}

// Transition is true if this event represents a state transition
func (s StateEvent) Transition() bool { return s.Old != s.New }

func (StateEvent) isEvent() {}

// ShedEvent is published when a request is shed
type ShedEvent struct {
	Key
	Published time.Time
	State     GenState
}

func (ShedEvent) isEvent() {}

// Event is an observability event published by the breaker
type Event interface {
	isEvent()
}

func LogEvents(ctx context.Context, logF func(format string, args ...interface{}), events <-chan Event) {
	go func() {
		for {
			select {
			case ev := <-events:
				switch t := ev.(type) {
				case StateEvent:
					if t.Transition() {
						logF("%v transitioned from %v to %v", t.Key, t.Old.State(), t.New.State())
					}
				case ShedEvent:
					logF("%v shed a request", t.Key)
				}
			case <-ctx.Done():
				return
			}
		}
	}()
}
