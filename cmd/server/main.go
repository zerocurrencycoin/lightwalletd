package main

import (
	"context"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/sirupsen/logrus"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/reflection"

	"github.com/adityapk00/lightwalletd/common"
	"github.com/adityapk00/lightwalletd/frontend"
	"github.com/adityapk00/lightwalletd/walletrpc"
)

var log *logrus.Entry
var logger = logrus.New()

var (
	promRegistry = prometheus.NewRegistry()
)

var metrics = common.GetPrometheusMetrics()

func init() {
	logger.SetFormatter(&logrus.TextFormatter{
		//DisableColors:          true,
		FullTimestamp:          true,
		DisableLevelTruncation: true,
	})

	log = logger.WithFields(logrus.Fields{
		"app": "frontend-grpc",
	})

	promRegistry.MustRegister(metrics.LatestBlockCounter)
	promRegistry.MustRegister(metrics.TotalErrors)
	promRegistry.MustRegister(metrics.TotalBlocksServedConter)
	promRegistry.MustRegister(metrics.SendTransactionsCounter)
	promRegistry.MustRegister(metrics.TotalSaplingParamsCounter)
	promRegistry.MustRegister(metrics.TotalSproutParamsCounter)
}

// TODO stream logging

func LoggingInterceptor() grpc.ServerOption {
	return grpc.UnaryInterceptor(logInterceptor)
}

func logInterceptor(
	ctx context.Context,
	req interface{},
	info *grpc.UnaryServerInfo,
	handler grpc.UnaryHandler,
) (interface{}, error) {
	reqLog := loggerFromContext(ctx)
	start := time.Now()

	resp, err := handler(ctx, req)

	entry := reqLog.WithFields(logrus.Fields{
		"method":   info.FullMethod,
		"duration": time.Since(start),
		"error":    err,
	})

	if err != nil {
		entry.Error("call failed")
	} else {
		entry.Info("method called")
	}

	return resp, err
}

func loggerFromContext(ctx context.Context) *logrus.Entry {
	if xRealIP, ok := metadata.FromIncomingContext(ctx); ok {
		realIP := xRealIP.Get("x-real-ip")
		if len(realIP) > 0 {
			return log.WithFields(logrus.Fields{"peer_addr": realIP[0]})
		}
	}

	if peerInfo, ok := peer.FromContext(ctx); ok {
		return log.WithFields(logrus.Fields{"peer_addr": peerInfo.Addr})
	}

	return log.WithFields(logrus.Fields{"peer_addr": "unknown"})
}

type Options struct {
	bindAddr      string
	tlsCertPath   string
	tlsKeyPath    string
	noTLS         bool
	logLevel      uint64
	logPath       string
	zcashConfPath string
	cacheSize     int
	metricsPort   uint
	paramsPort    uint
}

func main() {
	opts := &Options{}
	flag.StringVar(&opts.bindAddr, "bind-addr", "127.0.0.1:9067", "the address to listen on")
	flag.StringVar(&opts.tlsCertPath, "tls-cert", "", "the path to a TLS certificate (optional)")
	flag.StringVar(&opts.tlsKeyPath, "tls-key", "", "the path to a TLS key file (optional)")
	flag.BoolVar(&opts.noTLS, "no-tls", false, "Disable TLS, serve un-encrypted traffic.")
	flag.Uint64Var(&opts.logLevel, "log-level", uint64(logrus.InfoLevel), "log level (logrus 1-7)")
	flag.StringVar(&opts.logPath, "log-file", "", "log file to write to")
	flag.StringVar(&opts.zcashConfPath, "conf-file", "", "conf file to pull RPC creds from")
	flag.IntVar(&opts.cacheSize, "cache-size", 40000, "number of blocks to hold in the cache")
	flag.UintVar(&opts.paramsPort, "params-port", 8090, "the port on which the params server listens")
	flag.UintVar(&opts.metricsPort, "metrics-port", 2234, "the port on which to run the prometheus metrics exported")

	// TODO prod metrics
	// TODO support config from file and env vars
	flag.Parse()

	if opts.zcashConfPath == "" {
		flag.Usage()
		os.Exit(1)
	}

	if !opts.noTLS && (opts.tlsCertPath == "" || opts.tlsKeyPath == "") {
		println("Please specify a TLS certificate/key to use. You can use a self-signed certificate.")
		println("See 'https://github.com/adityapk00/lightwalletd/blob/master/README.md#running-your-own-zeclite-lightwalletd'")
		os.Exit(1)
	}

	if opts.logPath != "" {
		// instead write parsable logs for logstash/splunk/etc
		output, err := os.OpenFile(opts.logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			log.WithFields(logrus.Fields{
				"error": err,
				"path":  opts.logPath,
			}).Fatal("couldn't open log file")
		}
		defer output.Close()
		logger.SetOutput(output)
		logger.SetFormatter(&logrus.JSONFormatter{})
	}

	logger.SetLevel(logrus.Level(opts.logLevel))

	// gRPC initialization
	var server *grpc.Server

	if !opts.noTLS && (opts.tlsCertPath != "" && opts.tlsKeyPath != "") {
		transportCreds, err := credentials.NewServerTLSFromFile(opts.tlsCertPath, opts.tlsKeyPath)
		if err != nil {
			log.WithFields(logrus.Fields{
				"cert_file": opts.tlsCertPath,
				"key_path":  opts.tlsKeyPath,
				"error":     err,
			}).Fatal("couldn't load TLS credentials")
		}
		server = grpc.NewServer(grpc.Creds(transportCreds), LoggingInterceptor())
	} else {
		server = grpc.NewServer(LoggingInterceptor())
	}

	// Enable reflection for debugging
	if opts.logLevel >= uint64(logrus.WarnLevel) {
		reflection.Register(server)
	}

	// Initialize Zcash RPC client. Right now (Jan 2018) this is only for
	// sending transactions, but in the future it could back a different type
	// of block streamer.

	rpcClient, err := frontend.NewZRPCFromConf(opts.zcashConfPath)
	if err != nil {
		log.WithFields(logrus.Fields{
			"error": err,
		}).Warn("zcash.conf failed, will try empty credentials for rpc")

		rpcClient, err = frontend.NewZRPCFromCreds("127.0.0.1:23811", "", "")

		if err != nil {
			log.WithFields(logrus.Fields{
				"error": err,
			}).Warn("couldn't start rpc conn. won't be able to send transactions")
		}
	}

	// Get the sapling activation height from the RPC
	saplingHeight, blockHeight, chainName, branchID, err := common.GetSaplingInfo(rpcClient)
	if err != nil {
		log.WithFields(logrus.Fields{
			"error": err,
		}).Warn("Unable to get sapling activation height")
	}

	log.Info("Got sapling height ", saplingHeight, " chain ", chainName, " branchID ", branchID)

	// Initialize the cache
	cache := common.NewBlockCache(opts.cacheSize, log)

	stopChan := make(chan bool, 1)

	// Start the block cache importer at 100 blocks, so that the server is ready immediately.
	// The remaining blocks are added historically
	cacheStart := blockHeight - 100
	if cacheStart < saplingHeight {
		cacheStart = saplingHeight
	}

	// Start the ingestor
	go common.BlockIngestor(rpcClient, cache, log, stopChan, cacheStart)

	// Add historical blocks also
	go common.HistoricalBlockIngestor(rpcClient, cache, log, cacheStart-1, opts.cacheSize, saplingHeight)

	// Signal handler for graceful stops
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		s := <-signals
		log.WithFields(logrus.Fields{
			"signal": s.String(),
		}).Info("caught signal, stopping gRPC server")
		// Stop the server
		server.GracefulStop()
		// Stop the block ingestor
		stopChan <- true
	}()

	// Start the metrics server
	go func() {
		http.Handle("/metrics", promhttp.HandlerFor(
			promRegistry,
			promhttp.HandlerOpts{},
		))
		metricsport := fmt.Sprintf(":%d", opts.metricsPort)
		log.Fatal(http.ListenAndServe(metricsport, nil))
	}()

	// Start the download params handler
	log.Infof("Starting params handler")
	paramsport := fmt.Sprintf(":%d", opts.paramsPort)
	go common.ParamsDownloadHandler(metrics, log, paramsport)

	// Start the GRPC server
	log.Infof("Starting gRPC server on %s", opts.bindAddr)

	// Compact transaction service initialization
	service, err := frontend.NewSQLiteStreamer(rpcClient, cache, log, metrics)
	if err != nil {
		log.WithFields(logrus.Fields{
			"error": err,
		}).Fatal("couldn't create SQL backend")
	}
	defer service.(*frontend.SqlStreamer).GracefulStop()

	// Register service
	walletrpc.RegisterCompactTxStreamerServer(server, service)

	// Start listening
	listener, err := net.Listen("tcp", opts.bindAddr)
	if err != nil {
		log.WithFields(logrus.Fields{
			"bind_addr": opts.bindAddr,
			"error":     err,
		}).Fatal("couldn't create listener")
	}

	err = server.Serve(listener)
	if err != nil {
		log.WithFields(logrus.Fields{
			"error": err,
		}).Fatal("gRPC server exited")
	}
}
