package grpcbreaker_test

import (
	"context"
	"fmt"
	"log"
	"testing"

	"github.com/jwilner/grpcbreaker"
	"github.com/jwilner/grpcbreaker/pbtest"
	"google.golang.org/grpc"
)

func TestNew(t *testing.T) {
	ctx, cncl := context.WithCancel(context.Background())
	defer cncl()

	br := grpcbreaker.New(
		ctx,
		grpcbreaker.Global(
			grpcbreaker.Predicate(func(err error) bool {
				return err != nil
			}),
			grpcbreaker.FailThreshold(1),
			grpcbreaker.ResetThreshold(1),
		),
	)

	grpcbreaker.LogEvents(ctx, log.Printf, br.Events)

	conn, _ := grpc.Dial("localhost:8080", grpc.WithUnaryInterceptor(br.UnaryInterceptor))

	client := pbtest.NewSvcAClient(conn)

	_, err := client.Get(context.Background(), new(pbtest.GetRequest))
	fmt.Println(err)
}
