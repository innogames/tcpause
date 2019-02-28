package main

import (
	"github.com/innogames/tcpause"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"
)

var (
	cfg tcpause.Config

	splash = `______________________________                             
\__    ___/\_   ___ \______   \_____   __ __  ______ ____  
  |    |   /    \  \/|     ___/\__  \ |  |  \/  ___// __ \ 
  |    |   \     \___|    |     / __ \|  |  /\___ \\  ___/ 
  |____|    \______  /____|    (____  /____//____  >\___  >
                   \/               \/           \/     \/`
	v          = viper.New()
	logger     = logrus.New()
	tcpauseCmd = &cobra.Command{
		Use:   "tcpause",
		Long:  splash,
		Short: "Zero-downtime proxy for any TCP backend",
		Run:   run,
	}
	completionCmd = &cobra.Command{
		Use: "completion",
		Run: func(cmd *cobra.Command, args []string) {
			_ = cmd.GenBashCompletionFile("completion.sh")
		},
	}
)

// logrusLogger wraps a logrus logger for compatibility with the tcpause library
type logrusLogger struct {
	tcpause.Logger
	l *logrus.Logger
}

// Debug logs debug messages
func (l *logrusLogger) Debug(msg string) {
	l.l.Debug(msg)
}

// Info logs debug messages
func (l *logrusLogger) Info(msg string) {
	l.l.Info(msg)
}

// init initializes the CLI
func init() {
	cobra.OnInitialize(loadConfig)
	tcpauseCmd.PersistentFlags().StringP("config", "c", "", "path to config file if any")
	tcpauseCmd.PersistentFlags().DurationP("grace-period", "g", 10*time.Second, "grace period for stopping the server")
	tcpauseCmd.PersistentFlags().String("proxy-addr", "localhost:3000", "proxy listen address")
	tcpauseCmd.PersistentFlags().Bool("proxy-reject-clients", false, "whether to accept the tls connection and reject with a 503 statuc code")
	tcpauseCmd.PersistentFlags().Duration("proxy-retry-after-interval", 3*time.Second, "time after which the client should retry when paused and rejected")
	tcpauseCmd.PersistentFlags().Duration("proxy-block-poll-interval", 100*time.Millisecond, "interval at which the state should be polled to continue blocked connections")
	tcpauseCmd.PersistentFlags().Duration("proxy-close-poll-interval", 100*time.Millisecond, "interval at which the proxy should poll whether to shutdown")
	tcpauseCmd.PersistentFlags().String("proxy-tls-ca-cert", "", "client ca cert if available")
	tcpauseCmd.PersistentFlags().String("proxy-tls-cert", "", "server cert if available")
	tcpauseCmd.PersistentFlags().String("proxy-tls-key", "", "server key if available")
	tcpauseCmd.PersistentFlags().String("control-addr", "localhost:3001", "control listen address")
	tcpauseCmd.PersistentFlags().String("upstream-addr", "localhost:3002", "upstream address")

	_ = v.BindPFlag("grace-period", tcpauseCmd.PersistentFlags().Lookup("grace-period"))
	_ = v.BindPFlag("proxy.addr", tcpauseCmd.PersistentFlags().Lookup("proxy-addr"))
	_ = v.BindPFlag("proxy.reject-clients", tcpauseCmd.PersistentFlags().Lookup("proxy-reject-clients"))
	_ = v.BindPFlag("proxy.retry-after-interval", tcpauseCmd.PersistentFlags().Lookup("proxy-retry-after-interval"))
	_ = v.BindPFlag("proxy.block-poll-interval", tcpauseCmd.PersistentFlags().Lookup("proxy-block-poll-interval"))
	_ = v.BindPFlag("proxy.close-poll-interval", tcpauseCmd.PersistentFlags().Lookup("proxy-close-poll-interval"))
	_ = v.BindPFlag("proxy.tls.ca-cert", tcpauseCmd.PersistentFlags().Lookup("proxy-tls-ca-cert"))
	_ = v.BindPFlag("proxy.tls.cert", tcpauseCmd.PersistentFlags().Lookup("proxy-tls-cert"))
	_ = v.BindPFlag("proxy.tls.key", tcpauseCmd.PersistentFlags().Lookup("proxy-tls-key"))
	_ = v.BindPFlag("control.addr", tcpauseCmd.PersistentFlags().Lookup("control-addr"))
	_ = v.BindPFlag("upstream.addr", tcpauseCmd.PersistentFlags().Lookup("upstream-addr"))
}

func main() {
	tcpauseCmd.AddCommand(completionCmd)

	if err := tcpauseCmd.Execute(); err != nil {
		logger.WithError(err).Fatal("Failed to run the command")
	}
}

// run runs the command
func run(cmd *cobra.Command, args []string) {
	l := &logrusLogger{l: logger}
	srv, err := tcpause.New(cfg, l)
	if err != nil {
		logger.WithError(err).Fatal("Could not create server")
	}

	// run it
	logger.Info("Starting server")
	err = srv.Start()
	if err != nil {
		logger.WithError(err).Fatal("Failed starting the server")
	}

	go func() {
		for err := range srv.Errors() {
			logger.WithError(err).Error("Encountered an unexpected error")
		}
	}()

	// wait for graceful shutdown
	wg := new(sync.WaitGroup)
	wg.Add(1)
	go handleInterrupt(srv, wg)
	wg.Wait()
	os.Exit(0)
}

// loadConfig loads and parses the config file
func loadConfig() {
	path, err := tcpauseCmd.PersistentFlags().GetString("config")
	if err != nil {
		logger.WithError(err).Fatal("Could not get config path")
	}

	// configure viper
	if path != "" {
		v.SetConfigFile(path)
	} else {
		v.SetConfigName("config")
		v.AddConfigPath("/etc/puppetlabs/puppet-proxy")
		v.AddConfigPath(".")
	}

	// read config
	err = v.ReadInConfig()
	if err != nil {
		logger.WithError(err).Fatal("Could not read config")
	}

	// parse config
	err = v.Unmarshal(&cfg)
	if err != nil {
		logger.WithError(err).Fatal("Could not parse config")
	}
}

// handleInterrupt takes care of signals and graceful shutdowns
func handleInterrupt(srv tcpause.Server, wg *sync.WaitGroup) {
	c := make(chan os.Signal, 1)
	defer close(c)

	signal.Notify(c, os.Interrupt, os.Kill, syscall.SIGTERM)

	<-c
	logger.Info("Shutting down. Kill again to force")
	go stop(srv, wg)

	<-c
	logger.Warn("Forced shutdown")
	os.Exit(1)
}

func stop(srv tcpause.Server, wg *sync.WaitGroup) {
	if err := srv.Stop(); err != nil {
		logger.WithError(err).Fatalf("Failed to properly stop the server")
	}

	wg.Done()
}
