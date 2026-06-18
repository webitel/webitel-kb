package amqp

import (
	"fmt"

	"github.com/ThreeDotsLabs/watermill"
	"github.com/ThreeDotsLabs/watermill-amqp/v3/pkg/amqp"
	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/webitel/webitel-kb/infra/pubsub/factory"
)

const (
	TopicExchangeType  = "topic"
	FanoutExchangeType = "fanout"
	DirectExchangeType = "direct"
)

type Factory struct {
	url    string
	logger watermill.LoggerAdapter
}

func NewFactory(url string, logger watermill.LoggerAdapter) (*Factory, error) {
	return &Factory{
		url:    url,
		logger: logger,
	}, nil
}

func (f *Factory) BuildSubscriber(name string, subConfig *factory.SubscriberConfig) (message.Subscriber, error) {
	if subConfig == nil {
		return nil, fmt.Errorf("no subscriber configured")
	}
	conf := amqp.Config{
		Connection: amqp.ConnectionConfig{
			AmqpURI: f.url,
		},
		Marshaler: amqp.DefaultMarshaler{},
		Exchange: amqp.ExchangeConfig{
			GenerateName: func(s string) string {
				return subConfig.Exchange.Name
			},
			Type:    subConfig.Exchange.Type,
			Durable: subConfig.Exchange.Durable,
		},
		Queue: amqp.QueueConfig{
			GenerateName: func(s string) string {
				return subConfig.Queue
			},
			Durable: true,
		},
		QueueBind: amqp.QueueBindConfig{
			GenerateRoutingKey: func(s string) string {
				return subConfig.RoutingKey
			},
		},
		Consume: amqp.ConsumeConfig{
			Consumer:  name,
			Exclusive: subConfig.ExclusiveConsumer,
		},
		TopologyBuilder: &amqp.DefaultTopologyBuilder{},
	}
	return amqp.NewSubscriber(conf, f.logger)
}

func (f *Factory) BuildPublisher(pubConfig *factory.PublisherConfig) (message.Publisher, error) {
	conf := amqp.Config{
		Connection: amqp.ConnectionConfig{
			AmqpURI: f.url,
		},
		Marshaler: amqp.DefaultMarshaler{},
		Exchange: amqp.ExchangeConfig{
			GenerateName: func(s string) string {
				return pubConfig.Exchange.Name
			},
			Type:    pubConfig.Exchange.Type,
			Durable: pubConfig.Exchange.Durable,
		},
		Publish: amqp.PublishConfig{
			GenerateRoutingKey: func(s string) string {
				return s
			},
		},
		TopologyBuilder: &amqp.DefaultTopologyBuilder{},
	}
	return amqp.NewPublisher(conf, f.logger)
}
