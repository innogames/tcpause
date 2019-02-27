# TCPause

TCPause is a zero-downtime proxy for TCP and UNIX sockets written in Go.

[![Build Status](https://travis-ci.org/innogames/tcpause.svg)](https://travis-ci.org/innogames/tcpause)
[![GoDoc](https://godoc.org/github.com/innogames/tcpause?status.svg)](https://godoc.org/github.com/innogames/tcpause)
[![Go Report Card](https://goreportcard.com/badge/github.com/innogames/tcpause)](https://goreportcard.com/report/github.com/innogames/tcpause)
[![Release](https://img.shields.io/github/release/innogames/tcpause.svg?style=flat-square)](https://github.com/innogames/tcpause/releases)

## Contents

- [General](#general)
  - [Features](#features)
- [Installation](#installation)
- [Usage](#usage)
  - [Example Config](#example-config)
  - [CLI](#cli)

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
$ go install github.com/innogames/tcpause/cmd/tcpause
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
