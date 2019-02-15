package main

import (
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"gitlab.innogames.de/sysadmins/gozero"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"
)

var (
	cfg gozero.Config

	v      = viper.New()
	logger = logrus.New()
	cmd    = &cobra.Command{
		Use:   "gozero",
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

func init() {
	cobra.OnInitialize(loadConfig)
	cmd.PersistentFlags().DurationP("grace-period", "g", 10*time.Second, "grace period for stopping the server")
	cmd.PersistentFlags().String("proxy-addr", "localhost:3000", "proxy listen address")
	cmd.PersistentFlags().Duration("proxy-retry-after-interval", 3*time.Second, "time after which the client should retry when paused")
	cmd.PersistentFlags().Duration("proxy-block-poll-interval", 100*time.Millisecond, "interval at which the state should be polled to continue blocked connections")
	cmd.PersistentFlags().Duration("proxy-close-poll-interval", 100*time.Millisecond, "interval at which the proxy should poll whether to shutdown")
	cmd.PersistentFlags().String("control-addr", "localhost:3001", "control listen address")
	_ = viper.BindPFlag("grace-period", cmd.PersistentFlags().Lookup("grace-period"))
	_ = viper.BindPFlag("proxy.addr", cmd.PersistentFlags().Lookup("proxy-addr"))
	_ = viper.BindPFlag("proxy.retry-after-interval", cmd.PersistentFlags().Lookup("proxy-retry-after-interval"))
	_ = viper.BindPFlag("proxy.block-poll-interval", cmd.PersistentFlags().Lookup("proxy-block-poll-interval"))
	_ = viper.BindPFlag("proxy.close-poll-interval", cmd.PersistentFlags().Lookup("proxy-close-poll-interval"))
	_ = viper.BindPFlag("control.addr", cmd.PersistentFlags().Lookup("control-addr"))
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
	srv, err := gozero.NewServer(cfg, logger)
	if err != nil {
		logger.WithError(err).Fatal("Could not create server")
	}

	// run it
	logger.Info("Starting server")
	srv.Start()
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
	// configure viper
	v.SetConfigName("config")
	v.AddConfigPath("/etc/puppetlabs/puppet-proxy")
	v.AddConfigPath(".")

	// read config
	err := v.ReadInConfig()
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
func handleInterrupt(srv gozero.Server, wg *sync.WaitGroup) {
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

func stop(srv gozero.Server, wg *sync.WaitGroup) {
	if err := srv.Stop(); err != nil {
		logger.WithError(err).Fatalf("Failed to properly stop the server")
	}

	wg.Done()
}
