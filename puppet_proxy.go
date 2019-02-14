package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"io/ioutil"
	"math"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/spf13/viper"
)

var (
	logger = logrus.New()
)

// config
type tlsConfig struct {
	CaCert string `mapstructure:"ca-cert"`
	Cert   string
	Key    string
}

type proxyConfig struct {
	Addr          string
	RetryInterval time.Duration `mapstructure:"retry-interval"`
	TLS           tlsConfig
}

type controlConfig struct {
	Addr string
}

type upstreamConfig struct {
	Addr string
}

type config struct {
	GracePeriod time.Duration `mapstructure:"grace-period"`
	Proxy       proxyConfig
	Control     controlConfig
	Upstream    upstreamConfig
}

type server struct {
	cfg    config
	done   chan struct{}
	srv    *http.Server
	paused bool
	tlsCfg *tls.Config
}

// main entry point
func main() {
	// load config
	cfg := loadConfig()

	var tlsCfg *tls.Config
	var err error
	if cfg.Proxy.TLS.CaCert != "" {
		tlsCfg, err = createTLSConfig(cfg.Proxy.TLS)
		if err != nil {
			logger.WithError(err).Error("Failed to create TLS config")
		}
	}

	// start server
	s := &server{
		cfg:    cfg,
		done:   make(chan struct{}, 1),
		paused: false,
		tlsCfg: tlsCfg,
	}

	wg := new(sync.WaitGroup)
	wg.Add(1)
	go s.setupProxy(wg)
	wg.Add(1)
	go s.setupControl(wg)

	// wait for graceful shutdown
	go handleInterrupt(s)
	wg.Wait()
	os.Exit(0)
}

// loadConfig loads and parses the config file
func loadConfig() config {
	// configure viper
	v := viper.New()
	v.SetConfigName("config")
	v.AddConfigPath("/etc/puppetlabs/puppet-proxy")
	v.AddConfigPath(".")

	// read config
	err := v.ReadInConfig()
	if err != nil {
		logger.WithError(err).Fatal("Couldn't read config")
	}

	// parse config
	var cfg config
	err = v.Unmarshal(&cfg)
	if err != nil {
		logger.WithError(err).Fatal("Couldn't parse config")
	}

	return cfg
}

// createTLSConfig loads the ca certificate and creates a server TLS config out of it
func createTLSConfig(tlsCfg tlsConfig) (cfg *tls.Config, err error) {
	// load CA cert
	caCertPEM, err := ioutil.ReadFile(tlsCfg.CaCert)
	if err != nil {
		return
	}
	cas := x509.NewCertPool()
	ok := cas.AppendCertsFromPEM(caCertPEM)
	if !ok {
		err = fmt.Errorf("could not add CA certificate %s", tlsCfg.CaCert)

		return
	}

	// load server key pair
	certPEM, err := ioutil.ReadFile(tlsCfg.Cert)
	if err != nil {
		return
	}
	keyPEM, err := ioutil.ReadFile(tlsCfg.Key)
	if err != nil {
		return
	}
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return
	}

	return &tls.Config{
		RootCAs:      cas,
		ClientCAs:    cas,
		Certificates: []tls.Certificate{cert},
		ClientAuth:   tls.RequireAndVerifyClientCert,
	}, nil
}

// handleInterrupt takes care of signals and graceful shutdowns
func handleInterrupt(server *server) {
	c := make(chan os.Signal, 1)
	defer close(c)

	signal.Notify(c, os.Interrupt, os.Kill, syscall.SIGTERM)

	<-c
	logger.Info("Shutting down. Kill again to force")
	go server.stop()

	<-c
	logger.Warn("Forcing shutdown")
	os.Exit(1)
}

// setupProxy starts the proxy server
func (s *server) setupProxy(wg *sync.WaitGroup) {
	defer wg.Done()
	defer logger.Info("Proxy shutdown")

	l, err := net.Listen("tcp", s.cfg.Proxy.Addr)
	if err != nil {
		logger.WithError(err).Fatalf("Proxy failed to listen on %s", s.cfg.Proxy.Addr)
	}

	logger.Infof("Proxy listening on %s", s.cfg.Proxy.Addr)
	s.serveListener(l, wg)

	err = l.Close()
	if err != nil {
		logger.WithError(err).Error("Couldn't close proxy listener")
	}
}

// setupControl starts the control server
func (s *server) setupControl(wg *sync.WaitGroup) {
	defer wg.Done()
	defer logger.Info("Control shutdown")

	mux := http.NewServeMux()
	mux.HandleFunc("/paused", s.handlePaused)

	s.srv = &http.Server{
		Addr:    s.cfg.Control.Addr,
		Handler: mux,
	}

	logger.Infof("Control listening on %s", s.cfg.Control.Addr)
	if err := s.srv.ListenAndServe(); err != nil {
		// server closed normally
		if err != http.ErrServerClosed {
			logger.WithError(err).Fatalf("Control failed to listen on %s", s.cfg.Control.Addr)
		}
	}
}

// stop initiates graceful shutdowns
func (s *server) stop() {
	s.done <- struct{}{}

	ctx, cancel := context.WithTimeout(context.Background(), s.cfg.GracePeriod)
	err := s.srv.Shutdown(ctx)
	if err != nil {
		logger.WithError(err).Error("Error shutting down control")
	}
	cancel()
}

// serveListener handles incoming client connections
func (s *server) serveListener(l net.Listener, wg *sync.WaitGroup) {
	for {
		// do not accept any new connections when server is about to close
		select {
		case <-s.done:
			return

		default:
		}

		// force next iteration of the loop after some time so that we can catch shutdown requests
		err := l.(*net.TCPListener).SetDeadline(time.Now().Add(100 * time.Millisecond))
		if err != nil {
			logger.WithError(err).Error("Could not set deadline on listener")
		}

		conn, err := l.Accept()
		if opErr, ok := err.(*net.OpError); ok && opErr.Timeout() {
			continue
		}
		if err != nil {
			logger.WithError(err).Error("Could not accept connection")

			continue
		}

		// handle client
		wg.Add(1)
		go s.handleConn(conn, wg)
	}
}

// handleConn handles one client connection
func (s *server) handleConn(conn net.Conn, wg *sync.WaitGroup) {
	defer func() {
		wg.Done()
		logger.Debug("Closing proxy connection")

		err := conn.Close()
		if err != nil {
			logger.WithError(err).Error("Couldn't close proxy connection")
		}
	}()

	// handle the client accordingly when the upstream is paused
	if s.paused {
		if s.tlsCfg != nil && s.cfg.Proxy.RetryInterval != 0 {
			s.rejectConn(conn)

			return
		}

		s.blockConn()
	}

	// make a new connection to the upstream
	addr, err := net.ResolveTCPAddr("tcp", s.cfg.Upstream.Addr)
	if err != nil {
		logger.WithError(err).Errorf("Failed to resolve upstream %s", s.cfg.Upstream)

		return
	}

	upstream, err := net.DialTCP("tcp", nil, addr)
	if err != nil {
		logger.WithError(err).Errorf("Failed to connect to upstream %s", s.cfg.Upstream)

		return
	}

	logger.Debugf("Connected to upstream %s", s.cfg.Upstream)

	// proxy the connection bidirectionally
	errChan := make(chan error, 1)
	go s.proxyConn(errChan, upstream, conn)
	go s.proxyConn(errChan, conn, upstream)
	<-errChan

	err = upstream.Close()
	if err != nil {
		logger.WithError(err).Error("Couldn't close upstream connection")
	}
}

// proxyConn proxies a connection to another
func (s *server) proxyConn(errChan chan<- error, dst, src net.Conn) {
	_, err := io.Copy(dst, src)

	errChan <- err
}

// rejectConn actually accepts the TLS connection and sends proper http codes and headers
// so that the client knows when it can give it another try
func (s *server) rejectConn(conn net.Conn) {
	tlsConn := tls.Server(conn, s.tlsCfg)
	resp := fmt.Sprintf(
		"HTTP/1.1 %d %s\r\nRetry-After: %d\r\n\r\n",
		http.StatusServiceUnavailable,
		http.StatusText(http.StatusServiceUnavailable),
		int(math.Ceil(s.cfg.Proxy.RetryInterval.Seconds())),
	)

	_, err := tlsConn.Write([]byte(resp))
	if err != nil {
		logger.WithError(err).Error("Could not send response")
	}

	err = tlsConn.Close()
	if err != nil {
		logger.WithError(err).Error("Couldn't close TLS connection")
	}
}

// blockConn blocks the request as long as the server is paused
func (s *server) blockConn() {
	for s.paused {
		time.Sleep(100 * time.Millisecond)
	}
}

// handlePaused handles un/pausing requests to upstream
func (s *server) handlePaused(w http.ResponseWriter, req *http.Request) {
	if req.Method == http.MethodGet {
		w.WriteHeader(200)

		var err error
		if s.paused {
			_, err = w.Write([]byte("{\"paused\": true}"))
		} else {
			_, err = w.Write([]byte("{\"paused\": false}"))
		}

		if err != nil {
			logger.WithError(err).Error("Control failed to write response")
		}

		return
	}

	if req.Method == http.MethodPut {
		s.paused = true
		w.WriteHeader(200)

		return
	}

	if req.Method == http.MethodDelete {
		s.paused = false
		w.WriteHeader(200)

		return
	}

	w.WriteHeader(405)
}
