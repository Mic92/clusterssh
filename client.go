package clusterssh

import (
	"bytes"
	"fmt"
	"io"
	"io/ioutil"
	"net"
	"net/url"
	"os"

	"golang.org/x/crypto/ssh"
)

const CTRL_C_CODE = '\x03'
const EOF_CODE = '\x04'

type DefaultLogger struct{}

func makeSigner(keyname string) (signer ssh.Signer, err error) {
	fp, err := os.Open(keyname)
	if err != nil {
		return
	}
	defer fp.Close()

	buf, _ := ioutil.ReadAll(fp)
	signer, _ = ssh.ParsePrivateKey(buf)
	return
}

func createKeyring() ssh.AuthMethod {
	signers := []ssh.Signer{}
	keys := []string{
		os.Getenv("HOME") + "/.ssh/id_ecdsa",
		os.Getenv("HOME") + "/.ssh/id_rsa",
		os.Getenv("HOME") + "/.ssh/id_dsa",
	}

	for _, keyname := range keys {
		signer, err := makeSigner(keyname)
		if err == nil {
			signers = append(signers, signer)
		}
	}

	return ssh.PublicKeys(signers...)
}

type Cluster struct {
	Hosts []Host
}

type Result struct {
	Output string
	Error  error
	Host   *Host
}

type Command struct {
	Results chan Result
	stdins  []io.WriteCloser
}

func (c *Command) SendStopSignal() {
	for _, stdin := range c.stdins {
		go stdin.Write([]byte{CTRL_C_CODE})
	}
}

func (c Cluster) Run(cmd string, cmdStdin []byte) Command {
	auth := []ssh.AuthMethod{createKeyring()}
	results := make(chan Result, 100)

	stdinChan := make(chan *io.WriteCloser)

	for _, host := range c.Hosts {
		go func(host Host) {
			if host.Password != nil {
				auth = append(auth, ssh.Password(*host.Password))
			}
			config := &ssh.ClientConfig{
				User: host.User,
				Auth: auth,
			}
			results <- host.run(cmd, cmdStdin, config, stdinChan)
		}(host)
	}

	var stdins []io.WriteCloser
	for range c.Hosts {
		select {
		case stdin := <-stdinChan:
			if stdin != nil {
				stdins = append(stdins, *stdin)
			}
		}
	}

	return Command{results, stdins}
}

type Host struct {
	Name     string
	Port     string
	User     string
	Password *string
}

func (host *Host) run(cmd string, cmdStdin []byte, config *ssh.ClientConfig, stdins chan *io.WriteCloser) Result {
	var result Result
	result.Host = host
	conn, err := ssh.Dial("tcp", net.JoinHostPort(host.Name, host.Port), config)

	if err != nil {
		stdins <- nil
		result.Error = fmt.Errorf("Unable to connect: %v", err)
		return result
	}

	session, err := conn.NewSession()
	defer session.Close()
	if err != nil {
		stdins <- nil
		result.Error = fmt.Errorf("Unable to get ssh session: %v", err)
		return result
	}

	pipe, err := session.StdinPipe()
	if err != nil {
		stdins <- nil
		result.Error = fmt.Errorf("Unable to get stdin: %v", err)
		return result
	}
	go func() {
		pipe.Write(cmdStdin)
		go pipe.Write([]byte{EOF_CODE})
	}()

	modes := ssh.TerminalModes{
		ssh.ECHO:          0,     // disable echoing
		ssh.TTY_OP_ISPEED: 14400, // input speed = 14.4kbaud
		ssh.TTY_OP_OSPEED: 14400, // output speed = 14.4kbaud
	}

	if err := session.RequestPty("xterm", 80, 40, modes); err != nil {
		result.Error = fmt.Errorf("failed to request pty: %v", err)
		return result
	}

	stdins <- &pipe

	var stdoutBuf bytes.Buffer
	session.Stdout = &stdoutBuf
	result.Error = session.Run(cmd)
	result.Output = stdoutBuf.String()

	return result
}

func ParseHost(input string) (*Host, error) {
	url, err := url.Parse("ssh://" + input)
	if err != nil {
		return nil, err
	}
	host := Host{}
	if url.User != nil {
		host.User = url.User.Username()
		password, isSet := url.User.Password()
		if isSet {
			host.Password = &password
		}
	} else {
		host.User = os.Getenv("USER")
		if host.User == "" {
			host.User = "root"
		}
	}
	name, port, err := net.SplitHostPort(url.Host)
	if err == nil || name == "" {
		host.Name = url.Host
		host.Port = "22"
	} else {
		host.Name = name
		host.Port = port
	}
	return &host, nil
}
