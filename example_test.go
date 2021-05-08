package grpcbreaker_test

import (
	"context"
	"errors"
	"net"
	"os"
	"path"
	"testing"
	"time"

	"github.com/jwilner/grpcbreaker"
	"github.com/jwilner/grpcbreaker/pbtest"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestNew(t *testing.T) {
	dir, err := os.MkdirTemp("", "")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = os.RemoveAll(dir)
	}()

	l, err := net.Listen("unix", path.Join(dir, "foo.sock"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = l.Close()
	}()

	srvr := grpc.NewServer()
	pbtest.RegisterSvcAServer(srvr, new(testServer))
	go srvr.Serve(l)

	ctx, cncl := context.WithCancel(context.Background())
	defer cncl()

	br := grpcbreaker.New(
		ctx,
		grpcbreaker.Global(
			grpcbreaker.Predicate(func(err error) bool { return err != nil }),
			grpcbreaker.FailThreshold(1),
			grpcbreaker.ResetTimeout(100*time.Second),
		),
	)

	conn, err := grpc.Dial(
		"unix://"+l.Addr().String(),
		grpc.WithInsecure(),
		grpc.WithUnaryInterceptor(br.UnaryInterceptor),
	)
	if err != nil {
		t.Fatal(err)
	}

	client := pbtest.NewSvcAClient(conn)

	_, err = client.Get(ctx, &pbtest.GetRequest{})
	if c := status.Code(err); c != codes.Internal {
		t.Fatalf("Expected %v but got %v", codes.Internal, c)
	}
	assertSequence(t, br.Events, grpcbreaker.Closed, grpcbreaker.Open)
	if _, err = client.Get(ctx, &pbtest.GetRequest{}); !errors.Is(err, grpcbreaker.ErrBreakerOpen) {
		t.Fatalf("Expected %v but got %v", grpcbreaker.ErrBreakerOpen, err)
	}
}

func assertSequence(
	t *testing.T,
	events <-chan grpcbreaker.Event,
	start grpcbreaker.State,
	states ...grpcbreaker.State,
) {
	t.Helper()

	timeout := time.After(100 * time.Microsecond)

	current := start
	for len(states) > 0 {
		next := states[0]
		select {
		case ev := <-events:
			e, ok := ev.(grpcbreaker.StateEvent)
			if !ok || !e.Transition() {
				continue
			}
			if e.Old.State() != current || e.New.State() != next {
				t.Fatalf("Expected %v->%v but got %v->%v", current, next, e.Old.State(), e.New.State())
			}
			current = next
			states = states[1:]
		case <-timeout:
			t.Fatalf("Timed out waiting for state event")
		}
	}
}

type testServer struct {
	pbtest.UnimplementedSvcAServer
}

func (t *testServer) Get(context.Context, *pbtest.GetRequest) (*pbtest.GetResponse, error) {
	return nil, status.Error(codes.Internal, "uh huh")
}

var _ pbtest.SvcAServer = (*testServer)(nil)
