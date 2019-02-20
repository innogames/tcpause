package tcpause

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"github.com/pkg/errors"
	"io"
	"io/ioutil"
	"math"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

type handler func(l net.Listener)

// TLSConfig holds all config values related to TLS
type TLSConfig struct {
	CaCert string `mapstructure:"ca-cert"`
	Cert   string
	Key    string
}

// ProxyConfig holds all config values related to the proxy server itself
type ProxyConfig struct {
	Addr               string
	RejectClients      bool          `mapstructure:"reject-clients"`
	RetryAfterInterval time.Duration `mapstructure:"retry-after-interval"`
	BlockPollInterval  time.Duration `mapstructure:"block-poll-interval"`
	ClosePollInterval  time.Duration `mapstructure:"close-poll-interval"`
	TLS                TLSConfig
}

// ControlConfig holds all config values related to the control server
type ControlConfig struct {
	Addr string
}

// UpstreamConfig holds all config values related to the upstream server
type UpstreamConfig struct {
	Addr string
}

// Config holds all config values
type Config struct {
	GracePeriod time.Duration `mapstructure:"grace-period"`
	Proxy       ProxyConfig
	Control     ControlConfig
	Upstream    UpstreamConfig
}

// server types
type server struct {
	cfg     Config
	logger  Logger
	done    chan struct{}
	srv     *http.Server
	paused  bool
	tlsCfg  *tls.Config
	wg      *sync.WaitGroup
	errChan chan error
}

// Server represents a server instance
type Server interface {
	Start() error
	Stop() error
	Errors() <-chan error
}

// New creates a new server instance
func New(cfg Config, logger Logger) (Server, error) {
	var tlsCfg *tls.Config
	var err error

	if cfg.Proxy.TLS.CaCert != "" && cfg.Proxy.TLS.Cert != "" && cfg.Proxy.TLS.Key != "" {
		tlsCfg, err = createTLSConfig(cfg.Proxy.TLS)
		if err != nil {
			return nil, err
		}
	}

	return &server{
		cfg:     cfg,
		logger:  logger,
		done:    make(chan struct{}, 1),
		paused:  false,
		tlsCfg:  tlsCfg,
		wg:      new(sync.WaitGroup),
		errChan: make(chan error, 100),
	}, nil
}

// Start starts up the server
func (s *server) Start() error {
	s.wg.Add(2)

	err := s.setup("proxy", s.cfg.Proxy.Addr, s.handleProxy)
	if err != nil {
		return err
	}

	return s.setup("control", s.cfg.Control.Addr, s.handleControl)
}

// Stop gracefully shuts down the server
func (s *server) Stop() error {
	s.done <- struct{}{}

	ctx, cancel := context.WithTimeout(context.Background(), s.cfg.GracePeriod)
	err := s.srv.Shutdown(ctx)
	cancel()

	s.wg.Wait()

	return err
}

// Errors returns the error channel
func (s *server) Errors() <-chan error {
	return s.errChan
}

// createTLSConfig loads the ca certificate and creates a server TLS config out of it
func createTLSConfig(tlsCfg TLSConfig) (cfg *tls.Config, err error) {
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

// setup starts up a server with its own listener and handler function
func (s *server) setup(name, addr string, handler handler) error {
	scheme, addr, err := parseAddr(addr)
	if err != nil {
		return err
	}

	name = strings.ToLower(name)
	l, err := net.Listen(scheme, addr)
	if err != nil {
		return errors.Wrapf(err, "%s failed to listen", name)
	}

	s.logger.Info(fmt.Sprintf("%s listening on %s", strings.Title(name), l.Addr().String()))
	go s.handleListener(l, addr, handler)

	return nil
}

// handleListener handles a listener using the specified handler function
func (s *server) handleListener(l net.Listener, addr string, handler handler) {
	defer s.wg.Done()
	defer s.logger.Info(fmt.Sprintf("Listener %s shutdown", l.Addr().String()))

	handler(l)
}

// handleControl handles all incoming connections to the proxy server
func (s *server) handleProxy(l net.Listener) {
	var err error

	for {
		// do not accept any new connections when server is about to close
		select {
		case <-s.done:
			if err = l.Close(); err != nil {
				s.errChan <- errors.Wrap(err, "could not close proxy listener")
			}

			return

		default:
		}

		// force next iteration of the loop after some time so that we can poll shutdown requests
		switch l.(type) {
		case *net.TCPListener:
			err = l.(*net.TCPListener).SetDeadline(time.Now().Add(s.cfg.Proxy.ClosePollInterval))
		case *net.UnixListener:
			err = l.(*net.UnixListener).SetDeadline(time.Now().Add(s.cfg.Proxy.ClosePollInterval))
		default:
			err = nil
		}
		if err != nil {
			s.errChan <- errors.Wrap(err, "could not set deadline on listener")
		}

		conn, err := l.Accept()
		if opErr, ok := err.(*net.OpError); ok && opErr.Timeout() {
			continue
		}
		if err != nil {
			s.errChan <- errors.Wrap(err, "could not accept connection")

			continue
		}

		// handle client
		s.wg.Add(1)
		go s.handleConn(conn)
	}
}

// handleControl handles all incoming connections to the control server
func (s *server) handleControl(l net.Listener) {
	mux := http.NewServeMux()
	mux.HandleFunc("/paused", s.handlePaused)

	s.srv = &http.Server{
		Handler: mux,
	}
	err := s.srv.Serve(l)

	// server closed abnormally
	if err != nil && err != http.ErrServerClosed {
		err = errors.Wrap(err, "control failed")
		s.errChan <- err
	}
}

// handleConn handles one proxy connection
func (s *server) handleConn(conn net.Conn) {
	defer func() {
		s.wg.Done()
		s.logger.Debug("Closing proxy connection")

		err := conn.Close()
		if err != nil {
			s.errChan <- errors.Wrap(err, "could not close proxy connection")
		}
	}()

	// handle the client accordingly when the upstream is paused
	if s.paused {
		if s.tlsCfg != nil && s.cfg.Proxy.RejectClients {
			conn = s.rejectConn(conn)

			return
		}

		s.blockConn()
	}

	// make a new connection to the upstream
	scheme, addr, err := parseAddr(s.cfg.Upstream.Addr)
	if err != nil {
		s.errChan <- errors.Wrapf(err, "failed to connect to upstream %s", s.cfg.Upstream.Addr)

		return
	}
	upstream, err := net.Dial(scheme, addr)
	if err != nil {
		s.errChan <- errors.Wrapf(err, "failed to connect to upstream %s", s.cfg.Upstream.Addr)

		return
	}

	s.logger.Debug(fmt.Sprintf("Connected to upstream %s", s.cfg.Upstream.Addr))

	// proxy the connection bidirectionally
	errChan := make(chan error, 1)
	go s.proxyConn(errChan, upstream, conn)
	go s.proxyConn(errChan, conn, upstream)
	<-errChan

	err = upstream.Close()
	if err != nil {
		s.errChan <- errors.Wrap(err, "could not close upstream connection")
	}
}

// proxyConn proxies a connection to another
func (s *server) proxyConn(errChan chan<- error, dst, src net.Conn) {
	_, err := io.Copy(dst, src)

	errChan <- err
}

// rejectConn actually accepts the TLS connection and sends proper http codes and headers
// so that the client knows when it can give it another try
func (s *server) rejectConn(conn net.Conn) *tls.Conn {
	resp := fmt.Sprintf(
		"HTTP/1.1 %d %s",
		http.StatusServiceUnavailable,
		http.StatusText(http.StatusServiceUnavailable),
	)

	retryAfter := s.cfg.Proxy.RetryAfterInterval
	if retryAfter.Seconds() > 0 {
		resp += fmt.Sprintf("\r\nRetry-After: %d", int(math.Ceil(s.cfg.Proxy.RetryAfterInterval.Seconds())))
	}

	resp += "\r\n\r\n"

	tlsConn := tls.Server(conn, s.tlsCfg)
	_, err := tlsConn.Write([]byte(resp))
	if err != nil {
		s.errChan <- errors.Wrap(err, "could not send response")
	}

	return tlsConn
}

// blockConn blocks the request as long as the server is paused
func (s *server) blockConn() {
	for s.paused {
		time.Sleep(s.cfg.Proxy.BlockPollInterval)
	}
}

// handlePaused handles un/pausing requests to upstream
func (s *server) handlePaused(w http.ResponseWriter, req *http.Request) {
	switch req.Method {
	case http.MethodGet:
		s.getPausedStatus(w)
	case http.MethodPut:
		s.setPausedStatus(w, true)
	case http.MethodDelete:
		s.setPausedStatus(w, false)
	default:
		w.WriteHeader(405)
	}
}

// getPausedStatus sends the current state to the client
func (s *server) getPausedStatus(w http.ResponseWriter) {
	w.WriteHeader(200)

	var err error
	if s.paused {
		_, err = w.Write([]byte("{\"paused\": true}"))
	} else {
		_, err = w.Write([]byte("{\"paused\": false}"))
	}

	if err != nil {
		s.errChan <- errors.Wrap(err, "control failed to write response")
	}
}

// setPausedStatus sets the desired state
func (s *server) setPausedStatus(w http.ResponseWriter, paused bool) {
	s.paused = paused
	w.WriteHeader(200)
}

// parseAddr parses schema and host/path from a URI for use with net.Dial/Listen
func parseAddr(addr string) (s string, a string, err error) {
	u, err := url.Parse(addr)
	if err != nil {
		return
	}

	s = u.Scheme
	switch s {
	case "unix":
		a = u.Path
	case "tcp":
		a = u.Host
	default:
		err = fmt.Errorf("scheme %s not supported", s)

		return
	}

	return
}
