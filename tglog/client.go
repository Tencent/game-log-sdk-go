package tglog

import (
	"context"
	"errors"
	"time"

	"git.woa.com/tglog/v3/sdk-go/discoverer"
	"git.woa.com/tglog/v3/sdk-go/logger"
)

// variables
var (
	ErrInvalidNetwork = errors.New("invalid network")
	ErrInvalidCodec   = errors.New("invalid codec")
	ErrInvalidHost    = errors.New("invalid host")
	ErrInvalidPort    = errors.New("invalid port")
	ErrDomainLookup   = errors.New("domain lookup failed")
	ErrNoConns        = errors.New("no available connections")
	ErrNoReady        = errors.New("producer is not ready")
)

// Callback is the callback func that will be called when Client finish sending the logger
type Callback func(log string, err error)

// Client is the interface of a TGLog netClient
type Client interface {
	// Send sends the logger synchronously
	Send(ctx context.Context, log string) error
	// SendAsync sends the logger asynchronously
	SendAsync(ctx context.Context, log string, cb Callback)
	// Close closes the netClient
	Close()
}

// NewClient news a TGLog client
func NewClient(opts ...Option) (Client, error) {
	// default options
	options := &Options{
		Network:                 "udp",
		Codec:                   CodecV1,
		BatchingMaxMessages:     10,
		BatchingMaxPublishDelay: 10 * time.Millisecond,
		BatchingMaxSize:         4096,
		MaxPendingMessages:      40960,
		WorkerNum:               1,
		Logger:                  logger.Std(),
	}

	for _, o := range opts {
		o(options)
	}

	if options.Host == "" {
		return nil, ErrInvalidHost
	}
	if options.Port == 0 {
		return nil, ErrInvalidPort
	}

	// create discoverer
	discoverer, err := discoverer.NewDNS(options.Host, options.Port, 30*time.Second, options.Logger)
	if err != nil {
		return nil, err
	}

	cli := &client{
		options:    options,
		discoverer: discoverer,
		log:        options.Logger,
	}

	// create connection manager
	conMgr, err := newConnMgr(options.Network, discoverer, options.Logger)
	if err != nil {
		discoverer.Close()
		return nil, err
	}

	cli.connMgr = conMgr
	return cli, nil
}

type client struct {
	options    *Options
	discoverer discoverer.Discoverer
	connMgr    *connMgr
	log        logger.Logger
}

func (c *client) Send(ctx context.Context, log string) error {
	if c.connMgr == nil {
		return ErrNoReady
	}
	return c.connMgr.send(ctx, []byte(log))
}

func (c *client) SendAsync(ctx context.Context, log string, cb Callback) {

}

func (c *client) Close() {
	if c.discoverer != nil {
		c.discoverer.Close()
	}

	if c.connMgr != nil {
		c.connMgr.stop()
	}
}
