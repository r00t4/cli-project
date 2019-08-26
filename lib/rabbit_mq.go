package lib

import (
	"errors"
	"fmt"
	"github.com/streadway/amqp"
	"log"
)

type Connection struct {
	Conn *amqp.Connection
	Ch *amqp.Channel
	Q amqp.Queue
	Id int
}

type ConnectionPool struct {
	Connections []*Connection
	UsedConnections []*Connection
	MaxConnSize int
	stopped bool
}

func NewConnectionPool(url string) ConnectionPool {
	var connections []*Connection
	for i:=0; i<3; i++ {
		conn, err := amqp.Dial(url)
		failOnError(err, "Failed to connect to RabbitMQ")

		ch, err := conn.Channel()
		failOnError(err, "Failed to open a channel")

		q, err := ch.QueueDeclare(
			"rpc_queue", // name
			false,       // durable
			false,       // delete when unused
			false,       // exclusive
			false,       // no-wait
			nil,         // arguments
		)
		failOnError(err, "Failed to declare a queue")

		err = ch.Qos(
			1,     // prefetch count
			0,     // prefetch size
			false, // global
		)
		failOnError(err, "Failed to set QoS")
		connections = append(connections, &Connection{Conn: conn, Ch: ch, Q: q, Id: i})
	}


	return ConnectionPool{Connections: connections, UsedConnections: []*Connection{}, MaxConnSize: 3, stopped: false}
}


func (cp *ConnectionPool) GetConnection() (*Connection, error) {
	if len(cp.Connections) == 0 {
		return nil, errors.New("connections doesn't exists")
	}
	if cp.stopped == true {
		return nil, errors.New("server is down")
	}
	conn := cp.Connections[len(cp.Connections)-1]
	cp.Connections = cp.Connections[:len(cp.Connections)-1]
	cp.UsedConnections = append(cp.UsedConnections, conn)
	fmt.Println(cp.Connections)
	fmt.Println(cp.UsedConnections)
	return conn, nil
}

func (cp *ConnectionPool) ReleaseConnection(id int) error {
	for i, used := range cp.UsedConnections {
		if id == used.Id {
			cp.Connections = append(cp.Connections, used)
			log.Println("connection found")
			cp.UsedConnections = append(cp.UsedConnections[:i], cp.UsedConnections[i+1:]...)
			fmt.Println(cp.Connections)
			fmt.Println(cp.UsedConnections)
			return nil
		}
	}
	fmt.Println(cp.Connections)
	fmt.Println(cp.UsedConnections)
	return errors.New("connection not found")
}

func (cp *ConnectionPool) ReleaseConnectionPool() error {
	cp.stopped = true
	for _, conn := range cp.Connections {
		conn.Ch.Close()
		conn.Conn.Close()
	}
	for _, used := range cp.UsedConnections {
		used.Ch.Close()
		used.Conn.Close()
	}
	return nil
}