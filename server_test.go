package tcpause

import (
	"fmt"
	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
	"net"
	"net/url"
	"runtime"
	"testing"
	"time"
)

var (
	srv          Server
	proxyAddr    = "unix:///tmp/proxy.sock"
	upstreamAddr = "unix:///tmp/upstream.sock"
	errChan      = make(chan error, 100)
	ping         = []byte("ping")
	pong         = []byte("pong")
)

func mockUpstream() {
	u, err := url.Parse(upstreamAddr)
	if err != nil {
		panic(err)
	}

	var addr string
	switch u.Scheme {
	case "unix":
		addr = u.Path
	case "tcp":
		addr = u.Host
	default:
		panic(fmt.Errorf("scheme %s not supported", u.Scheme))
	}

	l, err := net.Listen(u.Scheme, addr)
	if err != nil {
		panic(err)
	}

	for {
		conn, err := l.Accept()
		if err != nil {
			panic(err)
		}

		go func() {
			defer func() {
				err := conn.Close()
				if err != nil {
					panic(err)
				}
			}()

			buf := make([]byte, 4)
			_, err := conn.Read(buf)
			if err != nil {
				panic(err)
			}

			for i := 0; i < len(buf); i++ {
				if buf[i] != ping[i] {
					panic("expected ping")
				}
			}

			_, err = conn.Write(pong)
			if err != nil {
				panic(err)
			}
		}()
	}
}

func init() {
	cfg := Config{
		GracePeriod: 10 * time.Second,
		Proxy: ProxyConfig{
			Addr:              proxyAddr,
			RejectClients:     false,
			BlockPollInterval: 100 * time.Millisecond,
			ClosePollInterval: 100 * time.Millisecond,
		},
		Control: ControlConfig{
			Addr: "unix:///tmp/control.sock",
		},
		Upstream: UpstreamConfig{
			Addr: upstreamAddr,
		},
	}
	srv, err := New(cfg, NewNullLogger())
	if err != nil {
		panic(fmt.Sprintf("could not create server: %s", err.Error()))
	}

	err = srv.Start()
	if err != nil {
		panic(fmt.Sprintf("could not start server: %s", err.Error()))
	}

	go mockUpstream()
}

func config() Config {
	return Config{

	}
}

func TestNullLogger(t *testing.T) {
	t.Run("New server without TLS config should return no error", func(t *testing.T) {
		cfg := Config{}
		l := NewNullLogger()
		_, err := New(cfg, l)

		assert.NoError(t, err, "creating a server should not return an error")
	})

	t.Run("New server with wrong TLS config should return an error", func(t *testing.T) {
		cfg := Config{
			Proxy: ProxyConfig{
				TLS: TLSConfig{
					CaCert: "/wrong/path/to/ca-cert.pem",
					Cert:   "/wrong/path/to/cert.pem",
					Key:    "/wrong/path/to/key.pem",
				},
			},
		}
		l := mockLogger()
		_, err := New(cfg, l)

		assert.Error(t, err, "paths to tls files should be valid")
	})
}

type testingLogger struct {
	b *testing.B
}

func (l testingLogger) Debug(msg string) {
	fmt.Println(msg)
}

func (l testingLogger) Info(msg string) {
	fmt.Println(msg)
}

func mockLogger() Logger {
	return testingLogger{}
}

func BenchmarkServer(b *testing.B) {
	n := runtime.NumCPU()
	slots := make(chan struct{}, n)
	for i := 0; i < n; i++ {
		slots <- struct{}{}
	}

	go func() {
		for err := range errChan {
			fmt.Println(err)
		}
	}()

	for i := 0; i < b.N; i++ {
		<-slots
		go sendPing(errChan, slots)
	}
}

func sendPing(errChan chan error, slots chan struct{}) {
	defer func() {
		slots <- struct{}{}
	}()

	u, err := url.Parse(upstreamAddr)
	if err != nil {
		panic(err)
	}

	var addr string
	switch u.Scheme {
	case "unix":
		addr = u.Path
	case "tcp":
		addr = u.Host
	default:
		panic(fmt.Errorf("scheme %s not supported", u.Scheme))
	}

	conn, err := net.Dial(u.Scheme, addr)
	if err != nil {
		errChan <- errors.Wrap(err, "could not connect to mock upstream")

		return
	}

	defer func() {
		err := conn.Close()
		if err != nil {
			errChan <- errors.Wrap(err, "could not close connection")
		}
	}()

	_, err = conn.Write(ping)
	if err != nil {
		errChan <- errors.Wrap(err, "could not write to connection")

		return
	}

	buf := make([]byte, 4)
	_, err = conn.Read(buf)
	if err != nil {
		errChan <- errors.Wrap(err, "client: could not read from connection")

		return
	}

	for i := 0; i < len(buf); i++ {
		if buf[i] != pong[i] {
			errChan <- errors.Wrap(err, "did not receive pong")

			return
		}
	}
}
