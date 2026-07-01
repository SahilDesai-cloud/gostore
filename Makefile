.PHONY: all build test race bench vet clean

GOFLAGS := -trimpath

all: build test

build:
	go build $(GOFLAGS) ./...

test:
	go test $(GOFLAGS) ./...

race:
	go test -race $(GOFLAGS) ./...

vet:
	go vet ./...

bench:
	go test -bench=. -benchmem -benchtime=3s $(GOFLAGS) ./...

bench-write:
	go test -bench=BenchmarkPut -benchmem -benchtime=3s $(GOFLAGS) .

bench-read:
	go test -bench=BenchmarkGet -benchmem -benchtime=3s $(GOFLAGS) .

bench-amp:
	go test -bench=BenchmarkReadAmplification -benchmem -benchtime=1s $(GOFLAGS) .
	go test -bench=BenchmarkSpaceAmplification -benchmem -benchtime=1s $(GOFLAGS) .

bench-bloom:
	go test -bench=BenchmarkGetAbsent -benchmem -benchtime=3s $(GOFLAGS) .

crash-test:
	go test -run TestCrashConsistency -v $(GOFLAGS) .

short:
	go test -short $(GOFLAGS) ./...

clean:
	go clean ./...
