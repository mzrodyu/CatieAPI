package main

import (
	"context"
	stdtls "crypto/tls"
	"net"
	"net/http"
	"net/http/cookiejar"
	"strings"
	"time"

	utls "github.com/refraction-networking/utls"
	"golang.org/x/net/http2"
)

type chatGPTWebTransport struct {
	plain http.RoundTripper
	h2    *http2.Transport
}

func (transport *chatGPTWebTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if !strings.EqualFold(request.URL.Scheme, "https") {
		return transport.plain.RoundTrip(request)
	}
	return transport.h2.RoundTrip(request)
}

func newChatGPTWebHTTPClient(timeout time.Duration) *http.Client {
	jar, _ := cookiejar.New(nil)
	h2Transport := &http2.Transport{
		DialTLSContext: func(ctx context.Context, network, address string, _ *stdtls.Config) (net.Conn, error) {
			rawConn, err := (&net.Dialer{Timeout: 20 * time.Second, KeepAlive: 30 * time.Second}).DialContext(ctx, network, address)
			if err != nil {
				return nil, err
			}
			serverName, _, err := net.SplitHostPort(address)
			if err != nil {
				serverName = address
			}
			tlsConn := utls.UClient(rawConn, &utls.Config{
				ServerName: serverName,
				NextProtos: []string{"h2"},
				MinVersion: utls.VersionTLS12,
			}, utls.HelloChrome_Auto)
			if err := tlsConn.HandshakeContext(ctx); err != nil {
				_ = rawConn.Close()
				return nil, err
			}
			return tlsConn, nil
		},
	}
	return &http.Client{
		Timeout: timeout,
		Jar:     jar,
		Transport: &chatGPTWebTransport{
			plain: http.DefaultTransport,
			h2:    h2Transport,
		},
	}
}
