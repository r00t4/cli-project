package main

import (
	"cliproject/lib"
	"errors"
	"fmt"
	"github.com/urfave/cli"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"syscall"
)

type Config struct {
	Interface string`json:interface`
	Upstreams []Upstream`json:"upstreams"`
}

type Upstream struct {
	Path string`json:"path"`
	Method string`json:"method"`
	Backends []string`json:"backends"`
	ProxyMethod string`json:"proxyMethod"`
}

var (
	app = cli.NewApp()
	name = "config.json"
	isDaemon = false
)

var (
	ErrNotRunning    = errors.New("Process is not running")
	ErrUnableToParse = errors.New("Unable to read and parse process id")
	ErrUnableToKill  = errors.New("Unable to kill process")
)

func main() {
	info()
	commands()

	err := app.Run(os.Args)
	if err != nil {
		log.Fatal(err)
	}
}

func info() {
	app.Name = "balancer"
	app.Version = "1.0.0"
	app.EnableBashCompletion = true
}


func commands() {
	app.Commands = []cli.Command{
		{
			Name:    "run",
			Aliases: []string{"r"},
			Usage:   "To run",
			Action: run,
			Flags: []cli.Flag{
				cli.BoolFlag{
					Name:        "daemon, d",
					Usage:       "daemon flag",
					Destination: &isDaemon,
				},
			},
		},
		{
			Name:    "reload",
			Aliases: []string{"re"},
			Usage:   "Reload program",
			Action: reload,
		},
		{
			Name: "stop",
			Usage: "Stop servers",
			Action: stop,
		},
	}
}

func start(filename string){
	data, err := lib.GetConfig(filename)
	if err != nil {
		log.Println(err)
		return
	}
	//n := len(data)
	var servers []lib.MyServer
	for _, config := range data {
		server := lib.Init(&config)
		servers = append(servers, server)
		server.RunServer()
	}

	sign := make(chan os.Signal, 1)
	signal.Notify(sign, os.Interrupt,os.Kill , syscall.SIGINT, syscall.SIGTERM)
	<-sign

	for _, server := range servers {
		server.StopServer()
	}
}


func run(c *cli.Context) {
	if isDaemon {
		runDaemon(c)
	}
	if c.Args().First() != "" {
		name = c.Args().Get(0)
		start(name)
	} else {
		fmt.Printf("started with default configuration\n")
		start(name)
	}
}

func stop(ctx *cli.Context) error {
	if _, err := os.Stat(getPidFilePath()); err != nil {
		return ErrNotRunning
	}

	data, err := ioutil.ReadFile(getPidFilePath())
	if err != nil {
		return ErrNotRunning
	}
	ProcessID, err := strconv.Atoi(string(data))

	if err != nil {
		return ErrUnableToParse
	}

	process, err := os.FindProcess(ProcessID)
	if err != nil {
		return ErrUnableToParse
	}
	// remove PID file
	os.Remove(getPidFilePath())

	fmt.Printf("Killing process ID [%v] now.\n", ProcessID)
	// kill process and exit immediately
	err = process.Kill()

	if err != nil {
		return ErrUnableToKill
	}

	fmt.Printf("Killed process ID [%v]\n", ProcessID)
	return nil
}

func reload() {
	fmt.Printf("reload with %s configuration\n", name)
	start(name)
}

func runDaemon(c *cli.Context) error {
	cmd := exec.Command(os.Args[0], "run")
	cmd.Start()
	log.Println("Daemon process ID is : ", cmd.Process.Pid)
	savePID(cmd.Process.Pid)
	os.Exit(0)

	return nil
}

func savePID(pid int){
	file, err := os.Create(getPidFilePath())
	if err != nil {
		log.Printf("Unable to create pid file : %v\n", err)
		os.Exit(1)
	}

	defer file.Close()

	_, err = file.WriteString(strconv.Itoa(pid))

	if err != nil {
		log.Printf("Unable to create pid file : %v\n", err)
		os.Exit(1)
	}

	file.Sync()
}

func getPidFilePath() string {
	return os.Getenv("HOME") + "/daemon.pid"
}