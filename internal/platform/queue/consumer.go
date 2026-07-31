package queue

import (
	"context"
	"log"

	amqp "github.com/rabbitmq/amqp091-go"
)

// Consumer declares its own durable queue bound to meta.events with one
// routing key, then delivers each message to a handler — used by
// cmd/worker-ai (the only consumer in this codebase so far). Queue
// declaration lives here, not in Connection.New, per rabbitmq.go's doc
// comment: "declaring a queue you don't consume from is how you end up
// with orphaned queues nobody remembers creating" — only the thing that
// actually consumes gets to declare its queue.
type Consumer struct {
	ch        *amqp.Channel
	queueName string
}

// NewConsumer declares queueName (durable, survives a broker restart) and
// binds it to ExchangeMetaEvents under routingKey. Safe to call every
// worker startup — queue/binding declaration is idempotent in RabbitMQ.
func NewConsumer(conn *Connection, queueName, routingKey string) (*Consumer, error) {
	ch := conn.Channel()

	q, err := ch.QueueDeclare(queueName, true, false, false, false, nil)
	if err != nil {
		return nil, err
	}
	if err := ch.QueueBind(q.Name, routingKey, ExchangeMetaEvents, false, nil); err != nil {
		return nil, err
	}
	// Process one message at a time per worker instance — this pipeline's
	// bottleneck is the Gemini + Meta Graph API round trips, not local CPU,
	// so a higher prefetch would just let messages pile up in flight
	// without actually finishing any faster. Scale by running more worker
	// instances (all consuming the same queue), not by raising this.
	if err := ch.Qos(1, 0, false); err != nil {
		return nil, err
	}

	return &Consumer{ch: ch, queueName: q.Name}, nil
}

// Handler processes one delivery's body. Returning an error nacks the
// delivery WITH requeue — see Run's doc comment for why that's the right
// default here despite the poison-message risk it carries.
type Handler func(ctx context.Context, body []byte) error

// Run blocks, delivering messages to handler until ctx is cancelled.
// On handler error, the delivery is nacked with requeue=true: this
// pipeline's failure modes (Gemini/Meta API hiccups, a transient DB error)
// are generally transient, and losing a customer's DM silently is worse
// than a redelivery. This has no dead-letter queue or max-retry count yet
// — a genuinely poisoned message (e.g. one that always fails to unmarshal)
// will loop forever. Add a DLQ before this handles production volume; not
// implemented here since correctly tuning retry/backoff needs real failure
// data this codebase doesn't have yet.
func (c *Consumer) Run(ctx context.Context, handler Handler) error {
	deliveries, err := c.ch.ConsumeWithContext(
		ctx,
		c.queueName,
		"",    // consumer tag (auto-generated)
		false, // autoAck — false: we ack/nack explicitly based on handler outcome
		false, // exclusive
		false, // noLocal (unused by RabbitMQ)
		false, // noWait
		nil,
	)
	if err != nil {
		return err
	}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case d, ok := <-deliveries:
			if !ok {
				return nil // channel closed (connection lost) — caller decides whether to reconnect
			}
			if err := handler(ctx, d.Body); err != nil {
				log.Printf("worker: handler error, requeueing: %v", err)
				_ = d.Nack(false, true)
				continue
			}
			_ = d.Ack(false)
		}
	}
}
