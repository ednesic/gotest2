package main

import (
	"fmt"
	"net"
	"net/http"
	"time"

	pb "github.com/ednesic/grpctest"
	"github.com/ednesic/grpctest/server/logfactory"
	"github.com/grpc-ecosystem/go-grpc-middleware"
	"github.com/grpc-ecosystem/go-grpc-middleware/logging/zap"
	"github.com/grpc-ecosystem/go-grpc-middleware/tracing/opentracing"
	"github.com/grpc-ecosystem/go-grpc-prometheus"
	stdopentracing "github.com/opentracing/opentracing-go"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/uber/jaeger-client-go/config"
	"github.com/uber/jaeger-client-go/rpcmetrics"
	jprom "github.com/uber/jaeger-lib/metrics/prometheus"

	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
)

type server struct{}

func (s *server) GetStream(obj *pb.Object, stream pb.Hello_GetStreamServer) error {
	array := [5]*pb.Object{
		&pb.Object{Arg1: "oi1", Arg2: "oi3", Arg3: 50},
		&pb.Object{Arg1: "oi1", Arg2: "oi3", Arg3: 50},
		&pb.Object{Arg1: "oi1", Arg2: "oi3", Arg3: 50},
		&pb.Object{Arg1: "oi1", Arg2: "oi3", Arg3: 50},
		&pb.Object{Arg1: "oi1", Arg2: "oi3", Arg3: 50}}

	for _, object := range array {
		if err := stream.Send(object); err != nil {
			return err
		}
	}
	return nil
}

type jaegerLoggerAdapter struct {
	logger logfactory.Logger
}

func (l jaegerLoggerAdapter) Error(msg string) {
	l.logger.Error(msg)
}

func (l jaegerLoggerAdapter) Infof(msg string, args ...interface{}) {
	l.logger.Info(fmt.Sprintf(msg, args...))
}

func main() {

	zapLogger, _ := zap.NewProduction()
	defer zapLogger.Sync()

	logger := logfactory.NewFactory(zapLogger)

	var tracer stdopentracing.Tracer
	{
		cfg := config.Configuration{
			Sampler: &config.SamplerConfig{
				Type:  "const",
				Param: 1,
			},
			Reporter: &config.ReporterConfig{
				LogSpans:            false,
				BufferFlushInterval: 1 * time.Second,
				LocalAgentHostPort:  ":6831",
			},
		}
		time.Sleep(100 * time.Millisecond)
		var metrics = jprom.New()

		trace, _, err := cfg.New(
			"hello",
			config.Logger(jaegerLoggerAdapter{logger.Bg()}),
			config.Observer(rpcmetrics.NewObserver(metrics.Namespace("hello", nil), rpcmetrics.DefaultNameNormalizer)),
		)
		if err != nil {
			logger.Bg().Fatal("cannot initialize Jaeger Tracer", zap.Error(err))
		}
		tracer = trace
	}

	lis, err := net.Listen("tcp", ":8080")
	if err != nil {
		logger.Bg().Error("failed to fetch URL",
			zap.Error(err))
	}

	s := grpc.NewServer(
		grpc.StreamInterceptor(
			grpc_middleware.ChainStreamServer(
				grpc_opentracing.StreamServerInterceptor(grpc_opentracing.WithTracer(tracer)),
				grpc_prometheus.StreamServerInterceptor,
				grpc_zap.StreamServerInterceptor(zapLogger),
			)),
		grpc.UnaryInterceptor(grpc_middleware.ChainUnaryServer(
			grpc_opentracing.UnaryServerInterceptor(grpc_opentracing.WithTracer(tracer)),
			grpc_prometheus.UnaryServerInterceptor,
			grpc_zap.UnaryServerInterceptor(zapLogger),
		)),
	)
	pb.RegisterHelloServer(s, &server{})
	// Register reflection service on gRPC server.
	reflection.Register(s)

	grpc_prometheus.Register(s)
	// Register Prometheus metrics handler.
	http.Handle("/metrics", promhttp.Handler())

	if err := s.Serve(lis); err != nil {
		logger.Bg().Error("failed to fetch URL",
			zap.Error(err))
	}
}
