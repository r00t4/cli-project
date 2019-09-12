package lib

import (
	"context"
	"errors"
	"github.com/streadway/amqp"
	"log"
	"strconv"
	"sync"
	"time"
)


// Representation of RabbitMQ.
// Proxy method rabbitmq uses RabbitMQSender.

// RabbitMQSender used for sending RabbitMQ requests.
// Has two queues to receive and send.
type RabbitMQSender struct {
	conn             *amqp.Connection
	ch               *amqp.Channel
	send_q           *amqp.Queue
	receive_q        *amqp.Queue
	received         map[string]string
	receivedMapMutex sync.RWMutex
	stop             chan bool
}

// This method returns new RabbitMQ.
// Initialize connection, channel, send_queue, receive_queue.
func NewRabbitMQSender(url string) RabbitMQSender {
	conn, err := amqp.Dial(url)
	failOnError(err, "Failed to connect to RabbitMQ")

	ch, err := conn.Channel()
	failOnError(err, "Failed to open a channel")

	send_q, err := ch.QueueDeclare(
		"send_queue", // name
		false,       // durable
		false,       // delete when unused
		false,       // exclusive
		false,       // no-wait
		nil,         // arguments
	)
	failOnError(err, "Failed to declare a queue")

	receive_q, err := ch.QueueDeclare(
		"receive_queue", // name
		false,       // durable
		false,       // delete when unused
		false,       // exclusive
		false,       // no-wait
		nil,         // arguments
	)
	failOnError(err, "Failed to declare a queue")

	stop := make(chan bool)

	var received = make(map[string]string)

	var someMapMutex = sync.RWMutex{}
	return RabbitMQSender{conn, ch, &send_q, &receive_q, received, someMapMutex, stop}
}


// CloseRabbitMQ stops RabbitMQSender.
func (r *RabbitMQSender) CloseRabbitMQ() {
	r.stop <- true
	r.conn.Close()
	r.ch.Close()
}


// Starts to Listen from receive_queue.
// Stores all came msgs in received map.
func (r *RabbitMQSender) Listen() {

	defer r.conn.Close()
	defer r.ch.Close()

	err := r.ch.Qos(
		1,     // prefetch count
		0,     // prefetch size
		false, // global
	)
	failOnError(err, "Failed to set QoS")

	msgs, err := r.ch.Consume(
		r.receive_q.Name, // queue
		"",       // consumer
		false,    // auto-ack
		false,    // exclusive
		false,    // no-local
		false,    // no-wait
		nil,      // args
	)
	failOnError(err, "Failed to register a consumer")

	go func() {
		for d := range msgs {
			r.receivedMapMutex.RLock()
			r.received[string(d.CorrelationId)] = string(d.Body)
			//log.Println(r.received[string(d.CorrelationId)])
			r.receivedMapMutex.RUnlock()
			d.Ack(false)
		}
	}()

	log.Printf(" [*] Awaiting RPC requests")
	<-r.stop
}


// Send method publish message using send_queue.
// Here message is just number.
// Also corrId is needed for divide messages.
func (r *RabbitMQSender) Send(n int, corrId *string) error {
	err := r.ch.Publish(
		"",          // exchange
		"send_queue", // routing key
		false,       // mandatory
		false,       // immediate
		amqp.Publishing{
			ContentType:   "text/plain",
			CorrelationId: string(*corrId),
			ReplyTo:       r.send_q.Name,
			Body:          []byte(strconv.Itoa(n)),
		})
	return err
}

// Receive method receives message from receive_queue.
// It needs corrId of send message, and chan string for return.
// Also context for timeout.
func (r *RabbitMQSender) Receive(id string, str chan string, ctx context.Context) (string, error) {
	r.receivedMapMutex.Lock()
	res := r.received[id]
	r.receivedMapMutex.Unlock()
	var err error
	for res == "" {
		select {
		case <-ctx.Done():
			log.Println("receive got done from context")
			err = errors.New("not found, receive done from context")
			return "", err
		default:
			r.receivedMapMutex.Lock()
			res = r.received[id]
			r.receivedMapMutex.Unlock()
			break
		}
		time.Sleep(time.Second)
	}
	str <- res
	return res, err
}