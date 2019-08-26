package main

import (
	"cliproject/lib"
	"encoding/json"
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

var (
	app = cli.NewApp()
	name = "config.json"
	isDaemon = false
)

var (
	ErrNotRunning    = errors.New("Process is not running")
	ErrUnableToParse = errors.New("Unable to read and parse process id")
	ErrUnableToKill  = errors.New("Unable to kill process")
	ErrNotSaved      = errors.New("No default config")
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
			Usage:   "Reload running servers",
			Action: reload,
			Flags: []cli.Flag{
				cli.BoolFlag{
					Name:        "daemon, d",
					Usage:       "daemon flag",
					Destination: &isDaemon,
				},
			},
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
	saveConfig(data)
	var servers []lib.MyServer
	for _, config := range data {
		server := lib.NewServer(&config)
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
		fmt.Printf("started with %s configuration\n", c.Args().First())
		start(c.Args().First())
	} else {
		fmt.Printf("started with default configuration\n")
		start(getDefaultConfigFilePath())
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

func reload(ctx *cli.Context) error {
	err := stop(ctx)
	if err != nil {
		return err
	}

	run(ctx)

	fmt.Printf("reload with %s configuration\n", getLastConfigFilePath())
	run(ctx)
	return nil
}

func runDaemon(c *cli.Context) error {
	cmd := exec.Command(os.Args[0], "run")
	cmd.Start()
	log.Println("Daemon process ID is : ", cmd.Process.Pid)
	savePID(cmd.Process.Pid)
	os.Exit(0)

	return nil
}

func saveConfig(config []lib.Config) {
	file, err := os.Create(getLastConfigFilePath())
	if err != nil {
		log.Printf("Unable to create config file : %v\n", err)
		os.Exit(1)
	}

	defer file.Close()
	// TODO marshal
	json, err := json.Marshal(config)
	if err != nil {
		return
	}
	_, err = file.Write(json)

	if err != nil {
		log.Printf("Unable to create config file : %v\n", err)
		os.Exit(1)
	}

	file.Sync()
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

func getLastConfigFilePath() string {
	return os.Getenv("HOME") + "/lastConfig.json"
}

func getDefaultConfigFilePath() string {
	return os.Getenv("HOME") + "/default.json"
}