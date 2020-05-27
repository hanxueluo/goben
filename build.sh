#!/bin/sh -e


mkdir -p ./bin/
rm -f ./bin/*

build_local() {
    gofmt -s -w ./goben
    go tool fix ./goben
    go vet ./goben

    #which gosimple >/dev/null && gosimple ./goben
    which golint >/dev/null && golint ./goben
    #which staticcheck >/dev/null && staticcheck ./goben

    #go test ./goben
    #CGO_ENABLED=0 go install -v ./goben

    CGO_ENABLED=0 go build -o ./bin/goben -v ./goben
}

build_image() {
    docker build -f Dockerfile -t 'hanxueluo/goben-server:latest' .
}

build_linux() {
    export GOOS=linux
    build_local
}

target=$1

if [ "$target" = "run" ];then
    build_local
    ./bin/goben
    exit 0
fi

if [ "$target" = "linux" ];then
    build_linux
    exit 0
fi

if [ "$target" = "" ];then
    build_linux
    build_image
    exit 0
fi
