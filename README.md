# grpcmock

Mock gRPC server. Inspired by [Prism](https://github.com/stoplightio/prism).

Add example responses to your proto services:

```protobuf
package demo;

import "google/rpc/error_details.proto";
import "grpcmock/grpcmock.proto";

message HelloRequest { }

message HelloResponse {
  string status = 1;
}

service Demo {
  rpc SayHello(HelloRequest) returns (HelloResponse) {
    option (grpcmock.example) = {
      name: "example1";
      body {
       [type.googleapis.com/demo.HelloResponse] {
         status: "Hello World",
       }
      }
    };
    option (grpcmock.example) = {
      name: "example2";
      status: {
        code: NOT_FOUND
        message: "not found";
        details [
          {
            [type.googleapis.com/google.rpc.LocalizedMessage] {
              message: "Hello world not found";
            }
          }
        ];
      };
    };
  }
}
```

Usage:

```shell
go generate ./...
go install ./...

grpcmock demo/demo.desc

grpcurl -plaintext localhost:9999 list
grpcurl -plaintext localhost:9999 describe demo.Demo
grpcurl -plaintext localhost:9999 demo.Demo.SayHello
grpcurl -plaintext -rpc-header 'x-grpcmock-example: example2' localhost:9999 demo.Demo.SayHello

# To use your own protos:
protoc -o protos.desc --include_imports proto1.proto proto2.proto # etc...
grpcmock protos.desc
```
