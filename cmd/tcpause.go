package main

import (
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"gitlab.innogames.de/sysadmins/tcpause"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"
)

var (
	cfg tcpause.Config

	v      = viper.New()
	logger = logrus.New()
	cmd    = &cobra.Command{
		Use:   "tcpause",
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
	cmd.PersistentFlags().StringP("config", "c", "", "path to config file if any")
	cmd.PersistentFlags().DurationP("grace-period", "g", 10*time.Second, "grace period for stopping the server")
	cmd.PersistentFlags().String("proxy-addr", "localhost:3000", "proxy listen address")
	cmd.PersistentFlags().Bool("proxy-reject-clients", false, "whether to accept the tls connection and reject with a 503 statuc code")
	cmd.PersistentFlags().Duration("proxy-retry-after-interval", 3*time.Second, "time after which the client should retry when paused and rejected")
	cmd.PersistentFlags().Duration("proxy-block-poll-interval", 100*time.Millisecond, "interval at which the state should be polled to continue blocked connections")
	cmd.PersistentFlags().Duration("proxy-close-poll-interval", 100*time.Millisecond, "interval at which the proxy should poll whether to shutdown")
	cmd.PersistentFlags().String("proxy-tls-ca-cert", "", "client ca cert if available")
	cmd.PersistentFlags().String("proxy-tls-cert", "", "server cert if available")
	cmd.PersistentFlags().String("proxy-tls-key", "", "server key if available")
	cmd.PersistentFlags().String("control-addr", "localhost:3001", "control listen address")
	cmd.PersistentFlags().String("upstream-addr", "localhost:3002", "upstream address")

	_ = v.BindPFlag("grace-period", cmd.PersistentFlags().Lookup("grace-period"))
	_ = v.BindPFlag("proxy.addr", cmd.PersistentFlags().Lookup("proxy-addr"))
	_ = v.BindPFlag("proxy.reject-clients", cmd.PersistentFlags().Lookup("proxy-reject-clients"))
	_ = v.BindPFlag("proxy.retry-after-interval", cmd.PersistentFlags().Lookup("proxy-retry-after-interval"))
	_ = v.BindPFlag("proxy.block-poll-interval", cmd.PersistentFlags().Lookup("proxy-block-poll-interval"))
	_ = v.BindPFlag("proxy.close-poll-interval", cmd.PersistentFlags().Lookup("proxy-close-poll-interval"))
	_ = v.BindPFlag("proxy.tls.ca-cert", cmd.PersistentFlags().Lookup("proxy-tls-ca-cert"))
	_ = v.BindPFlag("proxy.tls.cert", cmd.PersistentFlags().Lookup("proxy-tls-cert"))
	_ = v.BindPFlag("proxy.tls.key", cmd.PersistentFlags().Lookup("proxy-tls-key"))
	_ = v.BindPFlag("control.addr", cmd.PersistentFlags().Lookup("control-addr"))
	_ = v.BindPFlag("upstream.addr", cmd.PersistentFlags().Lookup("upstream-addr"))
}

// main entry point
func main() {
	cmd.AddCommand(completionCmd)

	if err := cmd.Execute(); err != nil {
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
	path, err := cmd.PersistentFlags().GetString("config")
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
