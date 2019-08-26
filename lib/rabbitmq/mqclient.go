package rabbitmq

import (
	"github.com/streadway/amqp"
	"log"
)



func failOnError(err error, msg string) {
	if err != nil {
		log.Fatalf("%s: %s", msg, err)
	}
}

func Main() {
	mq := Init()
	mq.send()
}

func Init() RabbitMQ {
	mq := RabbitMQ{}
	mq.conn, mq.err = amqp.Dial("amqp://guest:guest@localhost:5672/")
	failOnError(mq.err, "Failed to connect to RabbitMQ")
	defer mq.conn.Close()
	mq.ch, mq.err = mq.conn.Channel()
	failOnError(mq.err, "Failed to open a channel")
	defer mq.ch.Close()
	return mq
}

func (mq *RabbitMQ) send(){
	q, err := mq.ch.QueueDeclare(
		"hello", // name
		false,   // durable
		false,   // delete when unused
		false,   // exclusive
		false,   // no-wait
		nil,     // arguments
	)
	failOnError(err, "Failed to declare a queue")

	body := "Hello World!"
	err = mq.ch.Publish(
		"",     // exchange
		q.Name, // routing key
		false,  // mandatory
		false,  // immediate
		amqp.Publishing {
			ContentType: "text/plain",
			Body:        []byte(body),
		})
	failOnError(err, "Failed to publish a message")
}