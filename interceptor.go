package grpcbreaker

import (
	"context"

	"google.golang.org/grpc"
)

type Breaker struct {
	UnaryInterceptor grpc.UnaryClientInterceptor
	Events           <-chan Event
}

func New(ctx context.Context, g *GlobalOptionSet, optionSets ...*OptionSet) *Breaker {
	defaults := []Option{
		Predicate(func(error) bool { return true }),
	}

	monitorCh := make(chan Event, 100)

	bc := newCache(deps{ctx.Done(), monitorCh}, defaults, g, optionSets...)

	interceptor := func(
		ctx context.Context,
		method string,
		req, reply interface{},
		cc *grpc.ClientConn,
		invoker grpc.UnaryInvoker,
		opts ...grpc.CallOption,
	) error {
		return bc.
			resolve(method, opts).
			call(ctx, func(ctx context.Context) error {
				return invoker(ctx, method, req, reply, cc, opts...)
			})
	}

	return &Breaker{interceptor, monitorCh}
}
