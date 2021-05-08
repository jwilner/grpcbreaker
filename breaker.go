package grpcbreaker

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sync/atomic"
	"time"
)

var (
	// ErrBreakerOpen is returned when the breaker is open
	ErrBreakerOpen = errors.New("breaker open")
	// ErrBreakerStopped is returned when a breaker has been disabled
	ErrBreakerStopped = errors.New("breaker stopped")
)

func newBreaker(key Key, deps deps, s settings) *breaker {
	return &breaker{
		Key:         key,
		deps:        deps,
		genState:    uint64(Closed), // gen 0, State closed
		genOutcomes: make(chan genOutcome),
		settings:    s,
	}
}

type deps struct {
	closeCh <-chan struct{}
	events  chan<- Event
}

type breaker struct {
	Key
	settings
	deps

	genState    uint64 // atomic, only written to by `run`
	genOutcomes chan genOutcome
}

func init() {
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)
}

func (b *breaker) call(ctx context.Context, circuit func(ctx context.Context) error) error {
	genState := GenState(atomic.LoadUint64(&b.genState))

	if genState == 0 {
		return circuit(ctx)
	}

	state := genState.State()

	if state == Open {
		select {
		case b.events <- ShedEvent{b.Key, time.Now(), genState}:
		default:
		}
		return ErrBreakerOpen
	}

	err := circuit(ctx)
	switch {
	case err != nil && b.predicate(err):
		select {
		case b.genOutcomes <- genState.asFail():
		case <-b.closeCh:
		case <-ctx.Done():
		}
	case state == HalfOpen:
		select {
		case b.genOutcomes <- genState.asPass():
		case <-b.closeCh:
		case <-ctx.Done():
		}
	}
	return err
}

func (b *breaker) run() {
	var (
		fails, passes         int
		lastFail, resetMoment time.Time
		resetTimer            *time.Timer
	)

	if b.reset > 0 {
		resetTimer = time.NewTimer(0)
	}

	for {
		genState := GenState(atomic.LoadUint64(&b.genState))
		state := genState.State()

		var resetCh <-chan time.Time
		if resetTimer != nil {
			resetCh = resetTimer.C
		}

		select {

		case res := <-b.genOutcomes:
			switch {
			case res.gen() != genState.Gen():
				continue // drop messages not from this gen

			case res.pass():
				passes++
				if passes >= b.resetThreshold {
					// half open -> closed
					fails = 0
					state = Closed
				}

			default: // fail
				fails++
				lastFail = time.Now()

				if fails < b.failThreshold {
					break
				}

				// closed -> open
				// half open -> open
				state = Open

				if resetTimer == nil {
					break
				}

				// start the resetTimer anew
				if !resetTimer.Stop() { // drain if need be
					select {
					case <-resetCh:
					default:
					}
				}

				resetMoment = lastFail.Add(b.reset)
				resetTimer.Reset(time.Until(resetMoment))
			}

		case <-resetCh:
			if state != Open { // if the timer is expiring when we're not in Open, ignore
				continue
			}

			resetMoment = time.Time{}
			// open -> half open
			state = HalfOpen

		case <-b.closeCh:
			atomic.StoreUint64(&b.genState, 0)
			return
		}

		newState := genState
		if genState.State() != state {
			newState = genState.Next(state)
			atomic.StoreUint64(&b.genState, uint64(newState))
		}

		select {
		case b.events <- StateEvent{
			Key:         b.Key,
			Published:   time.Now(),
			Old:         genState,
			New:         newState,
			LastFail:    lastFail,
			ResetMoment: resetMoment,
			Fails:       fails,
			Passes:      passes,
		}:
		default:
		}
	}
}

// State is the current state of the breaker -- closed, half open, or open
type State uint64

//go:generate stringer -type=State
const (
	Unknown State = iota
	Closed
	HalfOpen
	Open
)

const (
	stateBits   = 2
	outcomeBit  = 1
	outcomePass = 0
)

// GenState is the current gen and State of the breaker
type GenState uint64

// State returns the current State for the breaker
func (g GenState) State() State {
	return State(g) & ((1 << stateBits) - 1)
}

// String returns a friendly representation of this GenState
func (g GenState) String() string {
	return fmt.Sprintf("%d|%v", g.Gen(), g.State())
}

func (g GenState) asFail() genOutcome {
	return genOutcome(g) | outcomeBit
}

func (g GenState) asPass() genOutcome {
	return genOutcome(g) &^ outcomeBit
}

// Gen returns the generation of this GenState
func (g GenState) Gen() uint64 {
	return uint64(g) >> 2
}

// Next increments the generation and updates the state; more than 2^62 generations will overflow and wrap
func (g GenState) Next(state State) GenState {
	return GenState(g.Gen()+1)<<2 | GenState(state)
}

type genOutcome uint64

func (g genOutcome) pass() bool {
	return g&outcomeBit == outcomePass
}

func (g genOutcome) gen() uint64 {
	return uint64(g) >> 2
}
