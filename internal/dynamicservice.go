package internal

import (
	"context"
	"fmt"
	"google.golang.org/protobuf/reflect/protoreflect"

	"google.golang.org/grpc"
)

type unaryMethod struct {
	name     string
	handler  grpc.UnaryHandler
	req, res interface{}
}

type DynamicService struct {
	name     string
	fileName string
	methods  []*unaryMethod
}

type emptyInterface interface{}

func NewDynamicService(name protoreflect.FullName, fileName string) *DynamicService {
	return &DynamicService{
		name:     string(name),
		fileName: fileName,
	}
}

func (s *DynamicService) RegisterUnaryMethod(name string, req, res interface{}, h grpc.UnaryHandler) {
	s.methods = append(s.methods, &unaryMethod{name: name, handler: h, req: req, res: res})
}

func (s *DynamicService) Register(srv *grpc.Server) {
	serviceDesc := s.createServiceDesc()
	srv.RegisterService(serviceDesc, struct{}{})
}

func (s *DynamicService) createServiceDesc() *grpc.ServiceDesc {
	sd := &grpc.ServiceDesc{
		ServiceName: s.name,
		HandlerType: (*emptyInterface)(nil),
		Methods:     make([]grpc.MethodDesc, 0, len(s.methods)),
		Metadata:    s.fileName,
	}
	for _, method := range s.methods {
		sd.Methods = append(sd.Methods, createMethodDesc(fullMethod(s.name, method.name), method))
	}
	return sd
}

func createMethodDesc(fullMethod string, m *unaryMethod) grpc.MethodDesc {
	return grpc.MethodDesc{
		MethodName: m.name,
		Handler: func(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
			if err := dec(m.req); err != nil {
				return nil, err
			}
			if interceptor == nil {
				return m.handler(ctx, m.req)
			}
			info := &grpc.UnaryServerInfo{
				Server:     srv,
				FullMethod: fullMethod,
			}
			return interceptor(ctx, m.req, info, m.handler)
		},
	}
}

func fullMethod(serviceName, methodName string) string {
	return fmt.Sprintf("/%s/%s", serviceName, methodName)
}
