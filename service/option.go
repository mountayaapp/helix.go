package service

import (
	"os"
	"time"
)

/*
Option configures Service creation.
*/
type Option func(*serviceConfig)

/*
serviceConfig holds the configuration collected from Options before Service
creation.
*/
type serviceConfig struct {
	cloud           *cloud
	shutdownTimeout time.Duration
	signals         []os.Signal
}

/*
WithShutdownTimeout sets the maximum duration for graceful shutdown. Defaults
to 30 seconds.
*/
func WithShutdownTimeout(d time.Duration) Option {
	return func(cfg *serviceConfig) {
		cfg.shutdownTimeout = d
	}
}

/*
WithSignals overrides the default signals (SIGINT, SIGTERM) that trigger graceful
shutdown.
*/
func WithSignals(signals ...os.Signal) Option {
	return func(cfg *serviceConfig) {
		cfg.signals = signals
	}
}
