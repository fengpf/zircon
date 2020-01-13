#!/bin/bash
set -e -u

cd "$(dirname "$0")"

export GOPATH="$(dirname $(pwd))"

# update go packages

echo "downloading packages"
echo "WARNING: INSECURE: CODE SHOULD BE VENDORED BEFORE PRODUCTION"

if [ ! -e protobuf ]
then
	wget --quiet https://github.com/google/protobuf/releases/download/v3.5.1/protoc-3.5.1-linux-x86_64.zip
	unzip -q protoc-3.5.1-linux-x86_64.zip -d protobuf
	rm protoc-3.5.1-linux-x86_64.zip
fi
go get github.com/twitchtv/twirp/protoc-gen-twirp
go get github.com/golang/protobuf/protoc-gen-go
go get github.com/vektra/mockery/.../
go get gopkg.in/yaml.v2
go get github.com/coreos/etcd/clientv3
go get github.com/hanwen/go-fuse/fuse

export PATH="$GOPATH/bin:$(pwd)/protobuf/bin:$PATH"

# generate twirp bindings

echo "Generating twirp bindings"
protoc --twirp_out=. --go_out=. ./zircon/lib/lib/lib/rpc/twirp/*.proto

# generate mockery mocks

for interface in Chunkserver Frontend MetadataCache EtcdInterface SyncServer
do
	mockery -dir zircon/lib/lib/lib/apis/ "-name=${interface}" -output zircon/lib/lib/lib/apis/mocks/
done
mockery -dir zircon/lib/lib/lib/chunkupdate/ "-name=UpdaterMetadata" -output zircon/lib/lib/lib/chunkupdate/mocks/

# build binary

echo "building binary..."

go build zircon/lib/lib/lib/main

# run tests

echo "launching tests..."

TESTDIRS=$((for x in $(find zircon/lib -name '*_test.go'); do dirname $x; done) | sort -u)
FINALTESTS=

for testdir in ${TESTDIRS}
do
	if [ ! -e "${testdir}/no-auto-test" ]
	then
		FINALTESTS+="${testdir} "
	else
		echo "SKIPPED TESTS IN DIRECTORY: ${testdir}"
	fi
done

go test ${FINALTESTS}

echo "built!"
