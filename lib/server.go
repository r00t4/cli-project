package lib

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/gorilla/mux"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"os/exec"
	"strconv"
	"time"
)


// MyServer describes our server
type MyServer struct {
	Server          *http.Server // main server that will listen and serve
	rtr             *mux.Router // Router to add some handlers
	roundId         int // round-robin id that will be used in round-robin request
	stopped         bool // describes is our server is stopped
	gracefulTimeout time.Duration // time to wait before server shutdown
	data            Config // server configuration data
	client          *http.Client // sending requests to backends uses Client
	connection      ConnectionPool // connection pooling
	isRabbitMQ      bool // describes is there rabbitmq request in config
}

// Initializer for MyServer
// configures MyServer by using Config
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

// creates new router for MyServer
func (m *MyServer) newRouter() {
	m.rtr = mux.NewRouter()
}

// configures server interface and router
func (m *MyServer) configureServer() {
	m.Server = &http.Server{
		Addr:    "127.0.0.1" + m.data.Interface,
		Handler: m.rtr,
	}
}

// configures handlers by using data in config
// adds new path
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
				m.upstreamHandler(writer, request, &upstream)
			}

		})
	}
}

// depending on proxy method sends http.Response chan to goroutine, when chan will get value
// function upstreamHandler will write on http.ResponseWriter
func (m *MyServer) upstreamHandler(writer http.ResponseWriter, request *http.Request, upstream *Upstream) {
	ch := make(chan *http.Response)
	defer close(ch)

	if upstream.ProxyMethod == "round-robin" {
		go m.rRoundRobinRequest(*upstream, ch)
	} else if upstream.ProxyMethod == "anycast" {
		go m.rAnycastRequest(*upstream, ch)
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
		io.Copy(writer, d.Body) // write to http.ResponseWriter
	case <-time.After(time.Second * 30):
		log.Println("Time out: No news in 10 seconds")
	}
}

// start listenAndServe method of server
func (m *MyServer) RunServer() {
	go func() {
		log.Println("Server started with", m.data.Interface, "interface")
		if err := m.Server.ListenAndServe(); err != nil {
			log.Println(err)
		}
	}()
}

// start graceful shutdown, firstly we mark that MyServer is going to stop
// create context with timer, that equals to gracefulTimeout
// sleeps for gracefulTimer
// finally shuts down server
func (m *MyServer) StopServer() error {
	m.stopped = true
	ctx, cancel := context.WithTimeout(context.Background(), m.gracefulTimeout)
	defer cancel()

	time.Sleep(m.gracefulTimeout)

	if m.isRabbitMQ {
		err := m.connection.ReleaseConnectionPool()
		if err != nil {
			log.Println(err)
		}
		log.Println("connection pool closed")
	}

	log.Println("shutting down")
	return m.Server.Shutdown(ctx)
}

// anycastRequest sends requests to all backends and waits for fastest response
// there we use http.Response chan
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
	case d := <-response:
		ch <- d
	case <-time.After(time.Second * 10):
		log.Println("Time out: No news in 10 seconds")
	}
}

// reliable anycast request sends anycast request,
// but when it doesn't response in 10 seconds it will again
// send anycast request
func (m *MyServer) rAnycastRequest(upstream Upstream, ch chan *http.Response) {
	defer func() {
		if r := recover(); r != nil {
			fmt.Println("Recovered in reliable request", r)
		}
	}()
	response := make(chan *http.Response)
	for i := 0; i < 2; i++ {
		go m.anycastRequest(upstream, response)
		select {
		case d := <-response:
			ch <- d
			return
		case <-time.After(time.Second * 10):
			continue
		}
	}
}

// round-robin request sends request to one backend using round-robin id
// round-robin id is always increases to one after request
// when round-robin id reaches backends length, it will reset to zero
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
		ch <- d
	case <-time.After(time.Second * 10):
		log.Println("Time out: No news in 10 seconds")
	}
	m.roundId++
	m.roundId %= len(upstream.Backends)
}

// reliable round-robin request sends round-robin request
// if one round-robin request will not give response
// it will try another round-robin request using increased round-robin id
func (m *MyServer) rRoundRobinRequest(upstream Upstream, ch chan *http.Response) {
	response := make(chan *http.Response)
	for range upstream.Backends {
		go m.roundRobinRequest(upstream, response)
		select {
		case d := <-response:
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

	corrId, err := exec.Command("uuidgen").Output()
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("%s", corrId)

	connection.Sender.Send(n, string(corrId))
	k := make(chan string)

	go connection.Sender.Receive(string(corrId), k)
	select {
	case p := <-k:
		respChannel <- &http.Response{
			Body:       ioutil.NopCloser(bytes.NewBufferString(p)),
			Status:     "200 OK",
			StatusCode: 200,
			Proto:      "HTTP/1.1",
			Header:     make(http.Header, 0),
		}
	case <-time.After(10 * time.Second):
		fmt.Println("timeout after 10 seconds")
	}
	fmt.Println("Connection Id: ", connection.Id)
}

// sending request using Client
// response from url writes to http.Response chan
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
