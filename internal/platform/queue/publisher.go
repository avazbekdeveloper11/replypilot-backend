package queue

import (
	"context"
	"encoding/json"
	"fmt"

	amqp "github.com/rabbitmq/amqp091-go"
)

// Publisher publishes JSON-encoded events onto the meta.events exchange.
// Consumers are expected to be idempotent (dedup by the event's own id
// field) since RabbitMQ delivers at-least-once, not exactly-once.
type Publisher struct {
	ch *amqp.Channel
}

func NewPublisher(conn *Connection) *Publisher {
	return &Publisher{ch: conn.Channel()}
}

func (p *Publisher) Publish(ctx context.Context, routingKey string, payload any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal payload for routing key %s: %w", routingKey, err)
	}

	err = p.ch.PublishWithContext(
		ctx,
		ExchangeMetaEvents,
		routingKey,
		false, // mandatory
		false, // immediate
		amqp.Publishing{
			ContentType:  "application/json",
			DeliveryMode: amqp.Persistent,
			Body:         body,
		},
	)
	if err != nil {
		return fmt.Errorf("publish to %s: %w", routingKey, err)
	}
	return nil
}
