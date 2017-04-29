#!/bin/bash
mkdir bin &> /dev/null
export GOPATH=$(pwd)/../../
echo "Set GOPATH to $GOPATH"
go build -o bin/ratchet ratchet/ratchet.go
go build -o bin/ratchet-child ratchet-child/ratchet-child.go
