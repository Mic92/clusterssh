package main

import (
	"fmt"
	"io/ioutil"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/mic92/clusterssh"
	"golang.org/x/crypto/ssh/terminal"
)

type Options struct {
	cmd   string
	hosts clusterssh.Cluster
}

func parseArgs(args []string) (*Options, error) {
	if len(args) < 2 {
		return nil, fmt.Errorf("USAGE: %s cmd hosts...", args[0])
	}
	cmd := args[1]
	hosts := make(clusterssh.Cluster, len(args[2:]))
	for i, arg := range args[2:] {
		host, err := clusterssh.ParseHost(arg)
		if err != nil {
			return nil, fmt.Errorf("invalid host '%s': %v", arg, err)
		}
		hosts[i] = *host
	}
	return &Options{cmd, hosts}, nil
}

func main() {
	options, err := parseArgs(os.Args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
	clusterssh.SetLogger(&clusterssh.DefaultLogger{})

	runningHosts := len(options.hosts)
	timeout := make(chan bool, 1)

	signalChan := make(chan os.Signal, 1)
	signal.Notify(signalChan, os.Interrupt)
	signal.Notify(signalChan, syscall.SIGTERM)

	var input []byte
	if terminal.IsTerminal(int(os.Stdin.Fd())) {
		input, _ = ioutil.ReadAll(os.Stdin)
	}

	cmd := options.hosts.Run(options.cmd, input)

	for {
		select {
		case res := <-cmd.Results:
			fmt.Print(res)
			runningHosts -= 1
			if runningHosts <= 0 {
				return
			}
		case <-signalChan:
			fmt.Fprintf(os.Stderr, "terminating...\n")
			cmd.SendStopSignal()
			go func() {
				time.Sleep(5 * time.Second)
				timeout <- true
			}()
		case <-timeout:
			return
		}
	}
}
