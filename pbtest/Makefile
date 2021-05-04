%.pb.go %_grpc.pb.go: %.proto
	protoc --go_out=paths=source_relative:. --go-grpc_out=paths=source_relative:. $<
