package zk

import (
	"context"
	"fmt"
	"github.com/go-kit/kit/transport"
	"github.com/pkg/errors"
	"github.com/samuel/go-zookeeper/zk"
	"github.com/unbxd/go-base/base/drivers"
	"github.com/unbxd/go-base/base/drivers/zook"
	"github.com/unbxd/go-base/base/endpoint"
	"github.com/unbxd/go-base/base/log"
	"time"
)

const (
	node watchType = iota
	children
)

type (
	watchType int

	ConsumerOption func(*Consumer)

	Consumer struct {
		logger    log.Logger
		path      string
		watchType watchType
		zk        drivers.Driver

		reconnectFunc ReconnectOnErr
		delayFunc     DelayOnErr

		end        endpoint.Endpoint
		errHandler ErrorHandler
	}
)

func WithEndpointConsumerOption(end endpoint.Endpoint) ConsumerOption {
	return func(c *Consumer) { c.end = end }
}

func WithReconnectOnErrConsumerOption(r ReconnectOnErr) ConsumerOption {
	return func(c *Consumer) { c.reconnectFunc = r }
}

func WithDelayOnErrConsumerOption(d DelayOnErr) ConsumerOption {
	return func(c *Consumer) { c.delayFunc = d }
}

func (c *Consumer) Open() error {

	ctx := context.Background()

	for {

		state := c.zk.(*zook.ZookDriver).State()
		if state != zk.StateConnected {
			c.logger.Error("zook is not connected", log.String("state", state.String()))
			//we need to write a connection state manager for zookeeper to reconnect on disconnects
			time.Sleep(time.Duration(2000) * time.Millisecond)
			continue
		}

		data, eventCh, err := c.watch()
		if err == zk.ErrSessionExpired ||
			err == zk.ErrAuthFailed ||
			err == zk.ErrClosing ||
			err == zk.ErrConnectionClosed {
			time.Sleep(time.Duration(2000) * time.Millisecond)
			continue
		}

		ent := &drivers.Event{
			Type: 0,
			P:    c.path,
			D:    data,
			Err:  err,
		}

		c.ep(ctx, ent)

		if err != nil {
			if !c.reconnectFunc(err) {
				return err
			}

			delay := c.delayFunc(err)
			if delay > 0 {
				time.Sleep(delay)
				continue
			}
		}

		for ent := range eventCh {
			c.ep(ctx, ent)
		}

		c.logger.Debug("received close on event chan", log.String("path", c.path))
	}
}

func (c *Consumer) ep(ctx context.Context, ent *drivers.Event) {
	_, epErr := c.end(ctx, ent)
	if epErr != nil {
		c.errHandler.Handle(ctx, epErr)
	}
}

func NewConsumer(
	logger log.Logger,
	path string,
	options ...ConsumerOption,
) (*Consumer, error) {

	cs := &Consumer{
		logger:    logger,
		watchType: node,
		path:      path,
	}

	return newConsumer(logger, options, cs)
}

func NewChildConsumer(
	logger log.Logger,
	path string,
	options ...ConsumerOption,
) (*Consumer, error) {

	cs := &Consumer{
		logger:    logger,
		watchType: children,
		path:      path,
	}

	return newConsumer(logger, options, cs)
}

func newConsumer(logger log.Logger, options []ConsumerOption, cs *Consumer) (*Consumer, error) {
	for _, o := range options {
		o(cs)
	}

	if cs.end == nil {
		return nil, errors.Wrap(
			ErrCreatingConsumer, "missing endpoint",
		)
	}

	if cs.errHandler == nil {
		cs.errHandler = transport.NewLogErrorHandler(logger)
	}
	return cs, nil
}

func (c *Consumer) watch() (interface{}, <-chan *drivers.Event, error) {
	switch c.watchType {
	case node:
		return c.zk.Watch(c.path)
	case children:
		return c.zk.WatchChildren(c.path)
	default:
		return nil, nil, errors.New(fmt.Sprintf("unknown watchtype %s", c.watchType))
	}
}
