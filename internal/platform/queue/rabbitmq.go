// Package queue owns the RabbitMQ connection/channel and defines the
// exchange + routing-key contract shared by every publisher and consumer.
// See docs/ARCHITECTURE.md §6 for the full event flow this implements:
// webhook ingest -> dm.received -> message processing -> dm.process.ai ->
// AI worker -> either dm.send.reply or dm.handoff.requested.
package queue

import (
	"fmt"

	amqp "github.com/rabbitmq/amqp091-go"

	"github.com/replypilot/backend/internal/config"
)

const (
	ExchangeMetaEvents = "meta.events"

	RoutingKeyDMReceived        = "dm.received"
	RoutingKeyDMProcessAI       = "dm.process.ai"
	RoutingKeyDMSendReply       = "dm.send.reply"
	RoutingKeyDMHandoffRequest  = "dm.handoff.requested"
)

type Connection struct {
	conn *amqp.Connection
	ch   *amqp.Channel
}

// New dials RabbitMQ, opens a channel, and declares the topic exchange this
// service publishes to. Queue bindings belong to whichever worker consumes
// them (see cmd/worker-* in the full system, not part of this API service),
// so they are not declared here — declaring a queue you don't consume from
// is how you end up with orphaned queues nobody remembers creating.
func New(cfg config.RabbitMQConfig) (*Connection, error) {
	conn, err := amqp.Dial(cfg.URL)
	if err != nil {
		return nil, fmt.Errorf("dial rabbitmq: %w", err)
	}

	ch, err := conn.Channel()
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("open channel: %w", err)
	}

	if err := ch.ExchangeDeclare(
		ExchangeMetaEvents,
		"topic",
		true,  // durable
		false, // autoDelete
		false, // internal
		false, // noWait
		nil,
	); err != nil {
		_ = ch.Close()
		_ = conn.Close()
		return nil, fmt.Errorf("declare exchange %s: %w", ExchangeMetaEvents, err)
	}

	return &Connection{conn: conn, ch: ch}, nil
}

func (c *Connection) Channel() *amqp.Channel { return c.ch }

func (c *Connection) Close() error {
	if err := c.ch.Close(); err != nil {
		return err
	}
	return c.conn.Close()
}
