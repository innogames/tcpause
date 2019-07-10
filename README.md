# TCPause

TCPause is a zero-downtime proxy for TCP and UNIX sockets written in Go.

[![Build Status](https://travis-ci.org/innogames/tcpause.svg)](https://travis-ci.org/innogames/tcpause)
[![GoDoc](https://godoc.org/github.com/innogames/tcpause?status.svg)](https://godoc.org/github.com/innogames/tcpause)
[![Go Report Card](https://goreportcard.com/badge/github.com/innogames/tcpause)](https://goreportcard.com/report/github.com/innogames/tcpause)
[![Release](https://img.shields.io/github/release/innogames/tcpause.svg)](https://github.com/innogames/tcpause/releases)

## Contents

- [General](#general)
  - [Features](#features)
- [Installation](#installation)
- [Usage](#usage)
  - [Example Config](#example-config)
  - [CLI](#cli)
  - [HTTP](#http)
    - [Pause Status](#pause-status)
    - [Pause Proxy](#pause-proxy)
    - [Resume Proxy](#resume-proxy)
    - [Block Until Idle](#block-until-idle)

## General

TCPause focuses mainly on proxying TCP connections.
That means it can accept any kind of TCP connection and forwards the traffic to a configured upstream service.
It also supports UNIX sockets and can act as a proxy between a client and a UNIX socket.

Both can be mixed so you can let's say forward traffic that is supposed to go on a UNIX socket to a TCP backend.
That is particularly helpful if your client or backend doesn't support one of those.

Through another built-in HTTP server you can control whether the proxy should actually forward the traffic or block connections until it is resumed.
This essentially enables zero-downtime deployments of any TCP/UNIX socket-based backend.

### Features

- TCP/UNIX socket proxy
- Pause/Reject/Resume connections
- Graceful shutdowns

## Installation

If you want to run it as a standalone application you can go install it:
```
$ go install github.com/innogames/tcpause/tcpause
```

To use the library for your own project simply go get it: 
```
$ go get -u github.com/innogames/tcpause
```

## Usage

### Example Config

```
grace-period: 60s

proxy:
  addr: tcp://localhost:3000
  reject-clients: true
  retry-after-interval: 3s
  block-poll-interval: 100ms
  close-poll-interval: 100ms

  tls:
    ca-cert: ca.pem
    cert: cert.pem
    key: key.pem

control:
  addr: tcp://localhost:3001

upstream:
  addr: tcp://localhost:3002

```

### CLI

```
______________________________
\__    ___/\_   ___ \______   \_____   __ __  ______ ____
  |    |   /    \  \/|     ___/\__  \ |  |  \/  ___// __ \
  |    |   \     \___|    |     / __ \|  |  /\___ \\  ___/
  |____|    \______  /____|    (____  /____//____  >\___  >
                   \/               \/           \/     \/

Usage:
  tcpause [flags]
  tcpause [command]

Available Commands:
  completion
  help        Help about any command

Flags:
  -c, --config string                         path to config file if any
      --control-addr string                   control listen address (default "localhost:3001")
  -g, --grace-period duration                 grace period for stopping the server (default 10s)
  -h, --help                                  help for tcpause
      --proxy-addr string                     proxy listen address (default "localhost:3000")
      --proxy-block-poll-interval duration    interval at which the state should be polled to continue blocked connections (default 100ms)
      --proxy-close-poll-interval duration    interval at which the proxy should poll whether to shutdown (default 100ms)
      --proxy-reject-clients                  whether to accept the tls connection and reject with a 503 statuc code
      --proxy-retry-after-interval duration   time after which the client should retry when paused and rejected (default 3s)
      --proxy-tls-ca-cert string              client ca cert if available
      --proxy-tls-cert string                 server cert if available
      --proxy-tls-key string                  server key if available
      --upstream-addr string                  upstream address (default "localhost:3002")
```

### HTTP

TCPause also starts a control server which can take normal HTTP requests to block and resume connections to the proxy server.
Also it is possible to wait until all proxied connections are closed/done.

This makes it possible to block any new connections to the upstream, then wait for all running connections to finish, do whatever kind of magic you want to do on your upstream and then allow new connections again.
It basically allows for zero-downtime maintenance work on your servers.

#### Pause Status

With a GET request you can retrieve the current pause status:
```
$ curl -i http://localhost:3001/paused
HTTP/1.1 200 OK
Content-Type: application/json
Date: Tue, 09 Jul 2019 14:59:16 GMT
Content-Length: 17

{"paused": false}
```

#### Pause Proxy

With a PUT request you can "put" the proxy server into the paused state:
```
$ curl -iXPUT http://localhost:3001/paused
HTTP/1.1 201 Created
Date: Tue, 09 Jul 2019 15:01:00 GMT
Content-Length: 0

```
This will not pause any existing connections though.
See [Block Until Idle](#block-until-idle) on how to let the existing connections finish.

#### Resume Proxy

With a DELETE request you can resume the proxy server:
```
$ curl -iXDELETE http://localhost:3001/paused
HTTP/1.1 204 No Content
Date: Tue, 09 Jul 2019 15:02:21 GMT
Content-Length: 0

```
Any new connection to the proxy server will be handled normally from now on.

#### Block Until Idle

With another GET request you can block for as long as there are active connections left on the proxy:
```
$ curl -i http://localhost:3001/paused/block-until-idle
HTTP/1.1 204 No Content
Date: Tue, 09 Jul 2019 15:03:15 GMT
Content-Length: 0

```
This call will immediately return as soon as all proxy connections have been finished.
Beware that you should [Pause the Proxy](#pause-proxy) before invoking this call as otherwise it might block forever depending on your workload.
