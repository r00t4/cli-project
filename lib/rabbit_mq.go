package lib

import (
	"errors"
	"fmt"
	"github.com/streadway/amqp"
	"log"
	"strconv"
	"sync"
)


// Representation of Connection
// used in Connection Pooling using RabbitMQ
type Connection struct {
	Sender RabbitMQSender
	Id   int
}

type RabbitMQSender struct {
	conn             *amqp.Connection
	ch               *amqp.Channel
	q                amqp.Queue
	Msgs             <-chan amqp.Delivery
	received         map[string]string
	receivedMapMutex sync.RWMutex
	stop             chan bool
}

type ConnectionPool struct {
	Connections []*Connection
	currentId int
	UsedConnections []*Connection
	MaxConnSize int
	stopped bool
}

func NewRabbitMQSender(url string) (RabbitMQSender,error) {
	conn, err := amqp.Dial(url)
	if err != nil {
		return RabbitMQSender{}, err
	}

	ch, err := conn.Channel()
	if err != nil {
		return RabbitMQSender{}, err
	}

	q, err := ch.QueueDeclare(
		"rpc_queue", // name
		false,       // durable
		false,       // delete when unused
		false,       // exclusive
		false,       // no-wait
		nil,         // arguments
	)
	if err != nil {
		return RabbitMQSender{}, err
	}

	err = ch.Qos(
		1,     // prefetch count
		0,     // prefetch size
		false, // global
	)
	if err != nil {
		return RabbitMQSender{}, err
	}

	stop := make(chan bool)

	msgs, err := ch.Consume(
		q.Name, // queue
		"",     // consumer
		false,  // auto-ack
		false,  // exclusive
		false,  // no-local
		false,  // no-wait
		nil,    // args
	)
	failOnError(err, "Failed to register a consumer")

	var received = make(map[string]string)

	var someMapMutex = sync.RWMutex{}
	return RabbitMQSender{conn, ch, q, msgs, received, someMapMutex, stop}, nil
}

func (r *RabbitMQSender) CloseRabbitMQ() {
	r.stop <- true
	r.conn.Close()
	r.ch.Close()
}

func (r *RabbitMQSender) Listen() {

	go func() {
		for d := range r.Msgs {
			r.receivedMapMutex.RLock()
			r.received[string(d.CorrelationId)] = string(d.Body)
			r.receivedMapMutex.RUnlock()
			d.Ack(false)
		}
	}()

	log.Printf(" [*] Awaiting RPC requests")
}

func (r *RabbitMQSender) Send(n int, corrId string) {
	err := r.ch.Publish(
		"",          // exchange
		"rpc_queue", // routing key
		false,       // mandatory
		false,       // immediate
		amqp.Publishing{
			ContentType:   "text/plain",
			CorrelationId: string(corrId),
			ReplyTo:       r.q.Name,
			Body:          []byte(strconv.Itoa(n)),
		})
	if err != nil {
		fmt.Println("failed to publish message",err)
	}
}

func (r *RabbitMQSender) Receive(id string, str chan string) {
	r.receivedMapMutex.Lock()
	res := r.received[id]
	r.receivedMapMutex.Unlock()
	for res == "" {
		r.receivedMapMutex.Lock()
		res = r.received[id]
		r.receivedMapMutex.Unlock()
	}
	str <- res
	r.receivedMapMutex.Lock()
	delete(r.received, id)
	r.receivedMapMutex.Unlock()
	//}(k)
	//select {
	//case p := <-k:
	//	str <- p
	//case <-time.After(10 * time.Second):
	//	log.Println("timeout after 10 seconds")
	//}
}

func NewConnectionPool(url string) ConnectionPool {
	var connections []*Connection
	for i := 0; i < 3; i++ {
		sender, err := NewRabbitMQSender(url)
		if err != nil {
			panic(err)
		}
		go sender.Listen()
		connections = append(connections, &Connection{Sender: sender, Id: i})
	}

	return ConnectionPool{Connections: connections, UsedConnections: []*Connection{}, MaxConnSize: 3, stopped: false, currentId: 0}
}

func (cp *ConnectionPool) GetConnection() (*Connection, error) {
	if len(cp.Connections) == 0 {
		return nil, errors.New("connections doesn't exists")
	}
	if cp.stopped == true {
		return nil, errors.New("server is down")
	}
	cp.currentId = 0
	conn := cp.Connections[cp.currentId % len(cp.Connections)]
	cp.currentId++
	return conn, nil
}

func (cp *ConnectionPool) ReleaseConnectionPool() error {
	cp.stopped = true
	for _, conn := range cp.Connections {
		conn.Sender.CloseRabbitMQ()
	}
	for _, used := range cp.UsedConnections {
		used.Sender.CloseRabbitMQ()
	}
	return nil
}