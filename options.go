package grpcbreaker

import (
	"time"

	"google.golang.org/grpc"
)

type GlobalOptionSet struct {
	options []Option
}

type OptionSet struct {
	key     Key
	options []Option
}

type settings struct {
	predicate                     func(error) bool
	reset                         time.Duration
	failThreshold, resetThreshold int
}

type Option func(*settings)

func Predicate(predicate func(error) bool) Option {
	return func(s *settings) {
		s.predicate = predicate
	}
}

func ResetTimeout(dur time.Duration) Option {
	return func(s *settings) {
		s.reset = dur
	}
}

func FailThreshold(threshold int) Option {
	return func(s *settings) {
		s.failThreshold = threshold
	}
}

func ResetThreshold(threshold int) Option {
	return func(s *settings) {
		s.resetThreshold = threshold
	}
}

type CallOption struct {
	optionSet OptionSet
	grpc.EmptyCallOption
}

func CallSite(name string, opts ...Option) *CallOption {
	return &CallOption{optionSet: OptionSet{Key{BreakerCallSite, name}, opts}}
}

func Method(name string, opts ...Option) *OptionSet {
	return &OptionSet{Key{BreakerMethod, name}, opts}
}

func Service(name string, opts ...Option) *OptionSet {
	return &OptionSet{Key{BreakerService, name}, opts}
}

func Global(opts ...Option) *GlobalOptionSet {
	return &GlobalOptionSet{opts}
}
