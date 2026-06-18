package factory

import (
	"github.com/ThreeDotsLabs/watermill/message"
)

type SubscriberFactory interface {
	BuildSubscriber(name string, config *SubscriberConfig) (message.Subscriber, error)
}

type PublisherFactory interface {
	BuildPublisher(config *PublisherConfig) (message.Publisher, error)
}

type Factory interface {
	SubscriberFactory
	PublisherFactory
}

type ExchangeConfig struct {
	Name    string
	Type    string
	Durable bool
}

type QueueConfig struct {
	Name    string
	Durable bool
}

type SubscriberConfig struct {
	Exchange          ExchangeConfig
	Queue             string
	ExclusiveConsumer bool
	RoutingKey        string
}

type PublisherConfig struct {
	Exchange ExchangeConfig
}
