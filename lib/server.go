package lib

import (
	"bytes"
	"context"
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


// Representation of Server.

// MyServer describes our server.
type MyServer struct {
	Server          *http.Server // main server that will listen and serve
	rtr             *mux.Router // Router to add some handlers
	roundId         int // round-robin id that will be used in round-robin request
	stopped         bool // describes is our server is stopped
	gracefulTimeout time.Duration // time to wait before server shutdown
	data            Config // server configuration data
	client          *http.Client // sending requests to backends uses Client
	connection      *RabbitMQSender // rabbitmq
	isRabbitMQ      bool // describes is there rabbitmq request in config
}

// Initializer for MyServer.
// Configures MyServer by using Config.
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

// Creates new router for MyServer.
func (m *MyServer) newRouter() {
	m.rtr = mux.NewRouter()
}

// Configures server interface and router.
func (m *MyServer) configureServer() {
	m.Server = &http.Server{
		Addr:    "127.0.0.1" + m.data.Interface,
		Handler: m.rtr,
	}
}

// Configures handlers by using data in Config.
// Doesn't work if server is stopped.
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
				break
			default:
				m.upstreamHandler(writer, request, &upstream)
				break
			}

		})
	}
}

// Depending on proxy method sends http.Response chan to goroutine, when http.Response chan will take value.
// Function upstreamHandler writes on http.ResponseWriter.
func (m *MyServer) upstreamHandler(writer http.ResponseWriter, request *http.Request, upstream *Upstream) {
	ch := make(chan *http.Response)
	defer close(ch)

	if upstream.ProxyMethod == "round-robin" {
		go m.rRoundRobinRequest(*upstream, ch)
	} else if upstream.ProxyMethod == "anycast" {
		go m.rAnycastRequest(*upstream, ch)
	} else {
		if m.isRabbitMQ == false {
			conn := NewRabbitMQSender(upstream.Backends[0])
			go conn.Listen()
			m.connection = &conn
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

		//headers, err := json.Marshal(writer.Header()[])
		//if err != nil {
		//	log.Println("Failed to convert headers to json")
		//} else {
		//	log.Printf("%s %s %s", d.Request.Method, d.Request.URL.Host, headers)
		//}

		writer.WriteHeader(d.StatusCode)
		io.Copy(writer, d.Body) // write to http.ResponseWriter
		break
	case <-time.After(time.Second * 20):
		log.Println("Time out: No news in 20 seconds")
		break
	}
}

// Starts listenAndServe method of server.
func (m *MyServer) RunServer() {
	go func() {
		log.Println("Server started with", m.data.Interface, "interface")
		if err := m.Server.ListenAndServe(); err != nil {
			log.Println(err)
		}
	}()
}

// Start graceful shutdown, firstly we mark that MyServer is going to stop.
// Create context with timer, that equals to gracefulTimeout.
// Sleeps for gracefulTimer.
// Finally shuts down server.
func (m *MyServer) StopServer() error {
	m.stopped = true
	ctx, cancel := context.WithTimeout(context.Background(), m.gracefulTimeout)
	defer cancel()

	time.Sleep(m.gracefulTimeout)

	if m.isRabbitMQ {
		m.connection.CloseRabbitMQ()
	}

	log.Println("shutting down")
	return m.Server.Shutdown(ctx)
}

// AnycastRequest sends requests to all backends and waits for fastest response.
// There we also use http.Response chan.
func (m *MyServer) anycastRequest(upstream Upstream, ch chan *http.Response) {
	defer func() {
		if r := recover(); r != nil {
			fmt.Println("Recovered in reliable request", r)
		}
	}()
	response := make(chan *http.Response)
	for _, url := range upstream.Backends {
		go m.SendRequest(url, upstream.Method, response)
	}

	select {
	case d := <-response:
		ch <- d
	case <-time.After(time.Second * 10):
		log.Println("Time out: No news in 10 seconds")
	}
}

// Reliable anycast request sends anycast request,
// but when it doesn't response in 10 seconds it will again
// send anycast request.
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

// Round-robin request sends request to one backend using round-robin id.
// Round-robin id is always increases to one after request.
// When round-robin id reaches backends length, it will reset to zero.
func (m *MyServer) roundRobinRequest(upstream Upstream, ch chan *http.Response) {
	defer func() {
		if r := recover(); r != nil {
			fmt.Println("Recovered in reliable request", r)
		}
	}()

	response := make(chan *http.Response)
	go m.SendRequest(upstream.Backends[m.roundId], upstream.Method, response)

	select {
	case d := <-response:
		ch <- d
	case <-time.After(time.Second * 10):
		log.Println("Time out: No news in 10 seconds")
	}
	m.roundId++
	m.roundId %= len(upstream.Backends)
}

// Reliable round-robin request sends round-robin request.
// If one round-robin request will not give response.
// It will try another round-robin request using increased round-robin id.
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

	corrId := getCorrId()
	//fmt.Printf("%s", corrId)

	err := m.connection.Send(n, &corrId)
	if err != nil {
		panic("sending error")
	}
	k := make(chan string)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*6)

	go m.connection.Receive(string(corrId), k, ctx)
	select {
	case <-ctx.Done():
		log.Println("Got a cancel request")
		break
	case p := <-k:
		respChannel <- &http.Response{
			Body:       ioutil.NopCloser(bytes.NewBufferString(p)),
			Status:     "200 OK",
			StatusCode: 200,
			Proto:      "HTTP/1.1",
			Header:     make(http.Header, 0),
		}
		break
	case <-time.After(5 * time.Second):
		cancel()
		log.Println("timeout after 5 seconds")
		break
	}

	return
}

// Sending request using Client.
// Response from url writes to http.Response chan.
func (m *MyServer) SendRequest(url string, method string, ch chan *http.Response) error {
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

	ch <- resp
	return nil
}


// Generates unique Correlation Id
func getCorrId() string {
	corrId, err := exec.Command("uuidgen").Output()
	if err != nil {
		log.Fatal(err)
	}
	return string(corrId)
}

// Checking for error
func failOnError(err error, msg string) {
	if err != nil {
		log.Fatalf("%s: %s", msg, err)
	}
}
