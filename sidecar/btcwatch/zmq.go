package btcwatch

import (
	"context"
	"time"

	"github.com/go-zeromq/zmq4"

	"cosmossdk.io/log/v2"
)

// ZMQTrigger subscribes to bitcoind's hashblock notifications at endpoint (e.g.
// tcp://127.0.0.1:28332) and signals the returned channel on each new block, so Run
// polls at once instead of waiting for the next tick. It reconnects after errors;
// polling continues regardless.
func ZMQTrigger(ctx context.Context, endpoint string, logger log.Logger) <-chan struct{} {
	ch := make(chan struct{}, 1)
	go func() {
		for ctx.Err() == nil {
			if err := subscribe(ctx, endpoint, ch); err != nil && ctx.Err() == nil {
				logger.Debug("bitcoind zmq subscription failed; retrying", "endpoint", endpoint, "err", err)
				select {
				case <-ctx.Done():
				case <-time.After(5 * time.Second):
				}
			}
		}
	}()
	return ch
}

func subscribe(ctx context.Context, endpoint string, ch chan<- struct{}) error {
	sub := zmq4.NewSub(ctx)
	defer sub.Close()
	if err := sub.Dial(endpoint); err != nil {
		return err
	}
	if err := sub.SetOption(zmq4.OptionSubscribe, "hashblock"); err != nil {
		return err
	}
	for {
		if _, err := sub.Recv(); err != nil {
			return err
		}
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}
