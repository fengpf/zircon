To generate twirp bindings:

 $ cd zircon/lib/src/
 $ protoc --twirp_out=. --go_out=. ./zircon/lib/rpc/twirp/*.proto

To generate mockery mocks:

 $ cd zircon/lib/lib/lib/src/
 $ mockery -dir zircon/lib/apis/ -name=Chunkserver -output zircon/lib/apis/mocks/
 $ mockery -dir zircon/lib/apis/ -name=Frontend -output zircon/lib/apis/mocks/
 $ mockery -dir zircon/lib/apis/ -name=MetadataCache -output zircon/lib/apis/mocks/

To build binary:

 $ go build zircon/lib/main/
