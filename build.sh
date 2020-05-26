#!/bin/sh -e

gofmt -s -w ./goben
go tool fix ./goben
go vet ./goben

#which gosimple >/dev/null && gosimple ./goben
which golint >/dev/null && golint ./goben
#which staticcheck >/dev/null && staticcheck ./goben

go test ./goben
#CGO_ENABLED=0 go install -v ./goben
mkdir -p ./bin/
rm -f ./bin/*

CGO_ENABLED=0 go build -o ./bin/goben -v ./goben

docker build -f Dockerfile -t 'hanxueluo/goben-server:latest' .
