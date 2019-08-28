package lib

import (
	"errors"
	"github.com/streadway/amqp"
	"fmt"
	"log"
)

type Connection struct {
	Conn *amqp.Connection
	Id   int
}

type ConnectionPool struct {
	Connections []*Connection
	UsedConnections []*Connection
	MaxConnSize int
	stopped bool
}

func NewConnectionPool(url string) ConnectionPool {
	var connections []*Connection
	for i := 0; i < 3; i++ {
		conn, err := amqp.Dial(url)
		failOnError(err, "Failed to connect to RabbitMQ")

		connections = append(connections, &Connection{Conn: conn, Id: i})
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
	return errors.New("connection not found")
}

func (cp *ConnectionPool) ReleaseConnectionPool() error {
	cp.stopped = true
	for _, conn := range cp.Connections {
		err := conn.Conn.Close()
		if err != nil {
			return err
		}
	}
	for _, used := range cp.UsedConnections {
		err := used.Conn.Close()
		if err != nil {
			return err
		}
	}
	return nil
}
