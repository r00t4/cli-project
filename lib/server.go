package lib

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/gorilla/mux"
	"github.com/streadway/amqp"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"os/exec"
	"strconv"
	"time"
)

type MyServer struct {
	Server *http.Server
	rtr *mux.Router
	roundId int
	stopped bool
	gracefulTimeout time.Duration
	data Config
	client *http.Client
	connection ConnectionPool
	isRabbitMQ bool
}

func NewServer(data *Config) MyServer {
	myserver := MyServer{}
	myserver.client = &http.Client{}
	myserver.data = *data
	myserver.newRouter()
	myserver.configureServer()
	myserver.gracefulTimeout = 5 * time.Second
	myserver.stopped = false
	myserver.configureHandlers()
	return myserver
}

func (m *MyServer) newRouter() {
	m.rtr = mux.NewRouter()
}

func (m *MyServer) configureServer() {
	m.Server = &http.Server{
		Addr:"127.0.0.1" + m.data.Interface,
		Handler: m.rtr,
	}
}

func (m *MyServer) configureHandlers() {
	for _, item := range m.data.Upstreams {
		upstream := item
		m.rtr.HandleFunc("/"+upstream.Path, func(writer http.ResponseWriter, request *http.Request) {
			defer func() {
				if r := recover(); r != nil {
					log.Println("Recovered in handle", r)
				}
			}()

			if m.stopped {
				writer.WriteHeader(503)
				return
			}

			select {
			case <-request.Context().Done():
				writer.WriteHeader(503)
			default:
				m.upstreamHandler(writer,request, &upstream)
			}

		})
	}
}

func (m *MyServer) upstreamHandler(writer http.ResponseWriter, request *http.Request, upstream *Upstream) {
	ch := make(chan *http.Response)
	defer close(ch)

	if upstream.ProxyMethod == "round-robin" {
		go m.reliableRRRequest(*upstream, ch)
	} else if upstream.ProxyMethod == "anycast" {
		go m.reliableAnycastRequest(*upstream, ch)
	} else {
		if m.isRabbitMQ == false {
			m.connection = NewConnectionPool(upstream.Backends[0])
			m.isRabbitMQ = true
		}
		params := mux.Vars(request)
		keys := params["id"]

		key, err := strconv.Atoi(keys)
		if err != nil {
			fmt.Println(err)
			return
		}
		go m.rabbitMQRequest(*upstream, ch, key)
	}
	select {
	case d := <-ch:
		defer d.Body.Close()

		for name, values := range d.Header {
			writer.Header()[name] = values
		}

		writer.WriteHeader(d.StatusCode)
		io.Copy(writer, d.Body)
	case <-time.After(time.Second * 30):
		log.Println("Time out: No news in 10 seconds")
	}
}

func (m *MyServer) RunServer() {
	go func() {
		log.Println("Server started with", m.data.Interface, "interface")
		if err := m.Server.ListenAndServe(); err != nil {
			log.Println(err)
		}
	}()
}

func (m *MyServer) StopServer() error {
	m.stopped = true
	ctx, cancel := context.WithTimeout(context.Background(), m.gracefulTimeout)
	defer cancel()

	time.Sleep(m.gracefulTimeout)

	m.connection.ReleaseConnectionPool()

	log.Println("shutting down")
	return m.Server.Shutdown(ctx)
}

func (m *MyServer) anycastRequest(upstream Upstream, ch chan *http.Response) {
	defer func() {
		if r := recover(); r != nil {
			fmt.Println("Recovered in reliable request", r)
		}
	}()
	response := make(chan *http.Response)
	for _, url := range upstream.Backends {
		go m.sendRequest(url, upstream.Method, response)
	}

	select {
	case d := <- response:
		ch <- d
	case <-time.After(time.Second * 10):
		log.Println("Time out: No news in 10 seconds")
	}
}

func (m *MyServer) reliableAnycastRequest(upstream Upstream, ch chan *http.Response) {
	defer func() {
		if r := recover(); r != nil {
			fmt.Println("Recovered in reliable request", r)
		}
	}()
	response := make(chan *http.Response)
	for i := 0; i < 2; i++ {
		go m.anycastRequest(upstream, response)
		select {
		case d := <- response:
			ch <- d
			return
		case <-time.After(time.Second * 10):
			continue
		}
	}
}

func (m *MyServer) roundRobinRequest(upstream Upstream, ch chan *http.Response) {
	defer func() {
		if r := recover(); r != nil {
			fmt.Println("Recovered in reliable request", r)
		}
	}()

	response := make(chan *http.Response)
	go m.sendRequest(upstream.Backends[m.roundId], upstream.Method, response)

	select {
	case d := <-response:
		ch <-d
	case <-time.After(time.Second * 10):
		log.Println("Time out: No news in 10 seconds")
	}
	m.roundId++
	m.roundId %= len(upstream.Backends)
}

func (m *MyServer) reliableRRRequest(upstream Upstream, ch chan *http.Response) {
	response := make(chan *http.Response)
	for range upstream.Backends {
		go m.roundRobinRequest(upstream, response)
		select {
		case d := <- response:
			ch <- d
			return
		case <-time.After(time.Second * 10):
			continue
		}
	}
}

func (m *MyServer) rabbitMQRequest(upstream Upstream, respChannel chan *http.Response, n int) {

	connection, err := m.connection.GetConnection()
	if err != nil {
		log.Println(err)
		return
	}


	msgs, err := connection.Ch.Consume(
		connection.Q.Name, // queue
		"",     // consumer
		true,   // auto-ack
		false,  // exclusive
		false,  // no-local
		false,  // no-wait
		nil,    // args
	)
	failOnError(err, "Failed to register a consumer")

	corrId, err := exec.Command("uuidgen").Output()
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("%s", corrId)

	err = connection.Ch.Publish(
		"",          // exchange
		"rpc_queue", // routing key
		false,       // mandatory
		false,       // immediate
		amqp.Publishing{
			ContentType:   "text/plain",
			CorrelationId: string(corrId),
			ReplyTo:       connection.Q.Name,
			Body:          []byte(strconv.Itoa(n)),
		})
	failOnError(err, "Failed to publish a message")

	for d := range msgs {
		if string(corrId) == d.CorrelationId {
			respChannel <- &http.Response{
				Body:ioutil.NopCloser(bytes.NewBufferString(string(d.Body))),
				Status:        "200 OK",
				StatusCode:    200,
				Proto:         "HTTP/1.1",
				Header:        make(http.Header, 0),
			}
			break
		}
	}

	fmt.Println("Connection Id: ",connection.Id)

	err = m.connection.ReleaseConnection(connection.Id)
	if err != nil {
		log.Println(err)
	}
}

func (m *MyServer) sendRequest(url string, method string, ch chan *http.Response) error {
	defer func() {
		if r := recover(); r != nil {
			fmt.Println("Recovered in serve", r)
		}
	}()

	req, err := http.NewRequest(method, url, nil)
	if err != nil {
		return err
	}

	resp, err := m.client.Do(req)
	if err != nil {
		return err
	}

	headers, err := json.Marshal(resp.Header)
	if err != nil {
		log.Println("Failed to convert headers to json")
	} else {
		log.Printf("%s %s %s", method, url, headers)
	}

	ch <- resp
	return nil
}

func failOnError(err error, msg string) {
	if err != nil {
		log.Fatalf("%s: %s", msg, err)
	}
}