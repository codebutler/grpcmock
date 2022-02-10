package grpcmock

import (
	"context"
	"github.com/golang/protobuf/proto"
	protov1 "github.com/golang/protobuf/proto"
	"github.com/rs/zerolog"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"
	"gopkg.in/errgo.v2/errors"
	"grpcmock/internal"
	pbgrpcmock "grpcmock/proto/grpcmock"
	"io/ioutil"
	"path"
	"strings"
)

//go:generate protoc -I ./proto --go_out=./proto proto/grpcmock/grpcmock.proto --go_opt paths=source_relative

type GrpcMock struct {
	services []*internal.DynamicService
}

func NewGrpcMock() *GrpcMock {
	gm := &GrpcMock{}
	return gm
}

func (gm *GrpcMock) LoadProtoDescriptorFile(ctx context.Context, filename string) error {
	log := zerolog.Ctx(ctx).With().Str("descfile", path.Base(filename)).Logger()
	log.Debug().Msg("loading descriptors")

	in, err := ioutil.ReadFile(filename)
	if err != nil {
		return errors.Wrap(err)
	}

	d := &descriptorpb.FileDescriptorSet{}
	if err := proto.Unmarshal(in, d); err != nil {
		return errors.Wrap(err)
	}

	for _, f := range d.GetFile() {
		if err := gm.loadFile(log.WithContext(ctx), f); err != nil {
			return errors.Wrap(err)
		}
	}

	return nil
}

func (gm *GrpcMock) Register(s *grpc.Server) {
	for _, service := range gm.services {
		service.Register(s)
	}
}

func (gm *GrpcMock) loadFile(ctx context.Context, f *descriptorpb.FileDescriptorProto) error {
	log := zerolog.Ctx(ctx).With().Str("file", f.GetName()).Logger()
	log.Debug().Msg("load file")

	df, err := protodesc.NewFile(f, protoregistry.GlobalFiles)
	if err != nil {
		return errors.Wrap(err)
	}

	_, err = protoregistry.GlobalFiles.FindFileByPath(f.GetName())
	if err == protoregistry.NotFound {
		if err := protoregistry.GlobalFiles.RegisterFile(df); err != nil {
			return errors.Wrap(err)
		}
	} else if err != nil {
		return errors.Wrap(err)
	}

	newCtx := log.WithContext(ctx)

	for i, mdp := range f.GetMessageType() {
		if err := gm.registerMessage(newCtx, f, mdp, df.Messages().Get(i)); err != nil {
			return errors.Wrap(err)
		}
	}

	for i, sdp := range f.GetService() {
		if err := gm.registerService(newCtx, f, sdp, df.Services().Get(i)); err != nil {
			return errors.Wrap(err)
		}
	}

	return nil
}

func (gm *GrpcMock) registerMessage(
	ctx context.Context,
	_ *descriptorpb.FileDescriptorProto,
	_ *descriptorpb.DescriptorProto,
	m protoreflect.MessageDescriptor,
) error {
	log := zerolog.Ctx(ctx).With().Str("msg", string(m.FullName())).Logger()
	log.Debug().Msg("register message")
	{
		_, err := protoregistry.GlobalTypes.FindMessageByName(m.FullName())
		if err == nil {
			// already registered, skip
			return nil
		} else if err != protoregistry.NotFound {
			// unexpected error
			return errors.Wrap(err)
		}
	}
	if err := protoregistry.GlobalTypes.RegisterMessage(dynamicpb.NewMessageType(m)); err != nil {
		return errors.Wrap(err)
	}
	return nil
}

func (gm *GrpcMock) registerService(
	ctx context.Context,
	f *descriptorpb.FileDescriptorProto,
	sdp *descriptorpb.ServiceDescriptorProto,
	sd protoreflect.ServiceDescriptor,
) error {
	log := zerolog.Ctx(ctx).With().Str("svc", string(sd.FullName())).Logger()
	log.Debug().Msg("register service")

	s := internal.NewDynamicService(sd.FullName(), f.GetName())
	gm.services = append(gm.services, s)

	for i, mdp := range sdp.GetMethod() {
		if err := gm.registerServiceMethod(log.WithContext(ctx), s, mdp, sd.Methods().Get(i)); err != nil {
			return errors.Wrap(err)
		}
	}

	return nil
}

func (gm *GrpcMock) registerServiceMethod(
	ctx context.Context,
	s *internal.DynamicService,
	mdp *descriptorpb.MethodDescriptorProto,
	md protoreflect.MethodDescriptor,
) error {
	log := zerolog.Ctx(ctx).With().Str("method", string(md.FullName())).Logger()
	log.Debug().
		Str("input", string(md.Input().FullName())).
		Str("output", string(md.Output().FullName())).
		Msg("register method")

	req, err := newMessageFromTypeName(md.Input().FullName())
	if err != nil {
		return errors.Wrap(err)
	}

	res, err := newMessageFromTypeName(md.Output().FullName())
	if err != nil {
		return errors.Wrap(err)
	}

	examples, err := examplesForMethod(ctx, mdp)
	if err != nil {
		return errors.Wrap(err)
	}

	s.RegisterUnaryMethod(mdp.GetName(), req, res, func(ctx context.Context, req interface{}) (interface{}, error) {
		if examples == nil {
			return nil, status.Error(codes.Unimplemented, "no examples found")
		}

		example, err := findExample(ctx, examples)
		if err != nil {
			return nil, errors.Wrap(err)
		}

		if exampleStatus := example.GetStatus(); exampleStatus != nil {
			var details []protov1.Message
			for _, d := range exampleStatus.GetDetails() {
				desc, err := protoregistry.GlobalFiles.FindDescriptorByName(
					protoreflect.FullName(strings.TrimPrefix(d.GetTypeUrl(), "type.googleapis.com/")))
				if err != nil {
					return nil, errors.Wrap(err)
				}
				m := dynamicpb.NewMessage(desc.(protoreflect.MessageDescriptor))
				if err := d.UnmarshalTo(m); err != nil {
					return nil, errors.Wrap(err)
				}
				details = append(details, protov1.MessageV1(m))
			}
			st, err := status.New(codes.Code(exampleStatus.GetCode()), exampleStatus.GetMessage()).WithDetails(details...)
			if err != nil {
				return nil, errors.Wrap(err)
			}
			return nil, st.Err()
		}

		if b := example.GetBody(); b != nil {
			if err := b.UnmarshalTo(res); err != nil {
				return nil, errors.Wrap(err)
			}
			return res, nil
		}

		return nil, status.New(codes.Internal, "invalid example").Err()
	})

	return nil
}

func findExample(ctx context.Context, examples []*pbgrpcmock.ExampleRule) (*pbgrpcmock.ExampleRule, error) {
	if len(examples) == 0 {
		return nil, status.New(codes.Unimplemented, "no examples found").Err()
	}
	md, ok := metadata.FromIncomingContext(ctx)
	if ok {
		exampleNames := md.Get("x-grpcmock-example")
		if len(exampleNames) > 0 {
			exampleName := exampleNames[0]
			for _, example := range examples {
				if example.Name == exampleName {
					return example, nil
				}
			}
			return nil, errors.New("unknown example")
		}
	}
	return examples[0], nil
}

func newMessageFromTypeName(inputType protoreflect.FullName) (*dynamicpb.Message, error) {
	desc, err := protoregistry.GlobalFiles.FindDescriptorByName(inputType)
	if err != nil {
		return nil, errors.Wrap(err)
	}
	return dynamicpb.NewMessage(desc.(protoreflect.MessageDescriptor)), nil
}

func examplesForMethod(ctx context.Context, mdp *descriptorpb.MethodDescriptorProto) ([]*pbgrpcmock.ExampleRule, error) {
	log := zerolog.Ctx(ctx)

	exampleExtensions, err := proto.GetExtension(mdp.GetOptions(), pbgrpcmock.E_Example)
	if err == proto.ErrMissingExtension {
		log.Warn().Msgf("no examples found for %s", mdp.GetName())
		return nil, nil
	} else if err != nil {
		return nil, errors.Wrap(err)
	}

	examples, ok := exampleExtensions.([]*pbgrpcmock.ExampleRule)
	if !ok {
		return nil, errors.New("extensions type mismatch")
	}

	return examples, nil
}
