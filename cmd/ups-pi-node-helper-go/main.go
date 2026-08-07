package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"ups-pi-node/internal/authpam"
	hardwareruntime "ups-pi-node/internal/hardware/runtime"
	"ups-pi-node/internal/helper"
	"ups-pi-node/internal/networkmanager"
)

func main() {
	if err := run(); err != nil {
		slog.Error("Go helper stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	var socketPath string
	var socketGroup string
	var configPath string
	var hardwareEnabled bool
	var pamProgram string
	var pamService string
	var requestTimeout time.Duration
	flag.StringVar(&socketPath, "socket", "/run/ups-pi-node/go-helper.sock", "Unix socket path")
	flag.StringVar(&socketGroup, "socket-group", "www-data", "Unix socket group")
	flag.StringVar(&configPath, "config", "/etc/ups-pi-node/main.conf", "existing ups-pi-node INI config")
	flag.BoolVar(
		&hardwareEnabled,
		"hardware",
		false,
		"own INA3221, GPIO, and ST7735 devices (never enable beside the Python helper)",
	)
	flag.StringVar(&pamProgram, "pamtester", "/usr/bin/pamtester", "root-owned pamtester executable")
	flag.StringVar(&pamService, "pam-service", "ups-pi-node", "allow-listed PAM service")
	flag.DurationVar(&requestTimeout, "request-timeout", 25*time.Second, "maximum request duration")
	flag.Parse()

	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	slog.SetDefault(logger)
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	networkManager, err := networkmanager.ConnectSystemBus()
	if err != nil {
		return err
	}
	defer networkManager.Close()
	router := networkmanager.Router(networkManager)
	for action, handler := range authpam.Router(authpam.New(pamProgram, pamService)) {
		router[action] = handler
	}

	var hardwareController *hardwareruntime.Controller
	if hardwareEnabled {
		hardwareConfig, err := hardwareruntime.LoadConfig(configPath)
		if err != nil {
			return err
		}
		hardwareController, err = hardwareruntime.NewController(hardwareConfig, logger)
		if err != nil {
			return err
		}
		if err := hardwareController.Start(ctx); err != nil {
			return err
		}
		defer hardwareController.Close()
		logger.Info("Go hardware owner enabled", "config", configPath)
	} else {
		logger.Info("Go hardware owner disabled for shadow validation")
	}
	for action, handler := range hardwareruntime.Router(hardwareController) {
		router[action] = handler
	}

	listener, err := helper.ListenUnix(socketPath, socketGroup)
	if err != nil {
		return err
	}
	defer listener.Close()
	go func() {
		<-ctx.Done()
		listener.Close()
	}()

	server := helper.ConnectionServer{
		Handler: router,
		Logger:  logger,
	}
	semaphore := make(chan struct{}, 16)
	logger.Info("Go helper listening", "socket", socketPath)
	for {
		connection, acceptErr := listener.Accept()
		if acceptErr != nil {
			if ctx.Err() != nil || errors.Is(acceptErr, net.ErrClosed) {
				return nil
			}
			return acceptErr
		}

		select {
		case semaphore <- struct{}{}:
			go func() {
				defer func() { <-semaphore }()
				requestContext, cancel := context.WithTimeout(ctx, requestTimeout)
				defer cancel()
				server.ServeConnection(requestContext, connection)
			}()
		default:
			connection.Close()
			logger.Warn("Go helper connection limit reached")
		}
	}
}
