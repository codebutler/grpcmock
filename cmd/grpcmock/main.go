package main

import (
	"context"
	"github.com/alecthomas/kong"
	grpczerolog "github.com/grpc-ecosystem/go-grpc-middleware/providers/zerolog/v2"
	middleware "github.com/grpc-ecosystem/go-grpc-middleware/v2"
	"github.com/grpc-ecosystem/go-grpc-middleware/v2/interceptors/logging"
	"github.com/grpc-ecosystem/go-grpc-middleware/v2/interceptors/tags"
	"github.com/improbable-eng/grpc-web/go/grpcweb"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/hlog"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
	"gopkg.in/errgo.v2/errors"
	"grpcmock"
	"net"
	"net/http"
	"time"
)

type CLI struct {
	ListenAddress    string `name:"listen" help:"Address to listen on." default:"localhost:9999"`
	WebListenAddress string `name:"web-listen" default:"localhost:8888"`
	Verbose          bool   `help:"Enable verbose output."`
	FileName         string `arg:"" help:"Proto descriptor file" type:"existingfile"`
}

func (cli *CLI) Run() error {
	if cli.Verbose {
		zerolog.SetGlobalLevel(zerolog.DebugLevel)
	}

	log := zerolog.New(zerolog.NewConsoleWriter())
	ctx := log.WithContext(context.Background())

	gm := grpcmock.NewGrpcMock()

	if err := gm.LoadProtoDescriptorFile(ctx, cli.FileName); err != nil {
		return errors.Wrap(err)
	}

	s := grpc.NewServer(
		middleware.WithUnaryServerChain(
			tags.UnaryServerInterceptor(),
			zerologContextUnaryServerInterceptor(log),
			logging.UnaryServerInterceptor(grpczerolog.InterceptorLogger(log)),
		),
		middleware.WithStreamServerChain(
			tags.StreamServerInterceptor(),
			logging.StreamServerInterceptor(grpczerolog.InterceptorLogger(log)),
		),
	)

	gm.Register(s)
	reflection.Register(s)

	g := new(errgroup.Group)

	g.Go(func() error {
		lis, err := net.Listen("tcp", cli.ListenAddress)
		if err != nil {
			return errors.Wrap(err)
		}
		log.Info().Msgf("grpc listening on %s", cli.ListenAddress)
		return s.Serve(lis)
	})

	g.Go(func() error {
		wrappedGrpc := grpcweb.WrapServer(s, grpcweb.WithAllowNonRootResource(true))
		handler := func(resp http.ResponseWriter, req *http.Request) {
			wrappedGrpc.ServeHTTP(resp, req)
		}
		logHandler := hlog.AccessHandler(func(r *http.Request, status, size int, duration time.Duration) {
			hlog.FromRequest(r).Info().
				Str("method", r.Method).
				Stringer("url", r.URL).
				Int("status", status).
				Int("size", size).
				Dur("duration", duration).
				Msg("")
		})
		httpServer := http.Server{
			Addr:    cli.WebListenAddress,
			Handler: hlog.NewHandler(log)(logHandler(http.HandlerFunc(handler))),
		}
		log.Info().Msgf("grpc-web listening on %s", cli.WebListenAddress)
		return httpServer.ListenAndServe()
	})

	if err := g.Wait(); err != nil {
		return errors.Wrap(err)
	}

	return nil
}

func zerologContextUnaryServerInterceptor(log zerolog.Logger) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (resp interface{}, err error) {
		ctxLog := log.With().Str("method", info.FullMethod).Logger()
		newCtx := ctxLog.WithContext(ctx)
		return handler(newCtx, req)
	}
}

func main() {
	var cli CLI
	ctx := kong.Parse(&cli,
		kong.Name("grpcmock"),
		kong.Description("Mock GRPC server"))
	err := ctx.Run()
	ctx.FatalIfErrorf(err)
}
