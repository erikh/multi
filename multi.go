package main

import (
	"fmt"
	"io"
	"io/ioutil"
	"net"
	"os"
	"os/exec"
	"strings"

	"github.com/pkg/errors"
	"github.com/urfave/cli"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

// Description is the long-form explanation of how to use the program.
const Description = `
multi is a small tool for making many ssh connections or local executors at once.

It is very fast. It is a much simpler version of GNU parallel with the
additional thread-based (not process-based) ssh functionality.

You can pass two formats to each command:

%t - the thread id (unique id of each thread)
%i - the item if -i was enabled, this will be a unique line from stdin.

No attempt is made to guarantee thread/item uniformity; runs may change thread
ids for items between invocations.

If both count and input are specified, the longer length wins, with the input
being omitted for any items that are are not there when coordinating with the
count.

If count is specified in ssh mode, it will be multiplied by the host list; and
count invocations will be run on each host.

There is currently no concurrency limit; it's gated at the number of items you
pass it.
`

const (
	// Version is the version of the program.
	Version = "1.0.0"
)

var commonFlags = []cli.Flag{
	cli.BoolFlag{
		Name:  "quiet, q",
		Usage: "Do not display output from commands",
	},
	cli.BoolFlag{
		Name:  "input, i",
		Usage: "Use standard input to work with a list of items by line: note the whole file is read in immediately, if -c is supplied, will use the largest value",
	},
	cli.UintFlag{
		Name:  "count, c",
		Usage: "Perform count number of items; if supplied with -i, will use the largest value",
		Value: 1,
	},
}

func main() {
	app := cli.NewApp()

	app.Author = "Erik Hollensbe <erik@hollensbe.org>"
	app.Description = Description
	app.Version = Version

	app.Commands = []cli.Command{
		cli.Command{
			Name:      "ssh",
			ShortName: "s",
			ArgsUsage: "-- [ host list file ] [ command ]",
			Usage:     "Execute a command in parallel over ssh; the host list file is a newline-delimited list of host:port pairs (22 is default)",
			Action:    sshCommand,
			Flags: append([]cli.Flag{
				cli.StringFlag{
					Name:  "username, u",
					Usage: "Username to connect as",
					Value: os.Getenv("USER"),
				},
				cli.StringFlag{
					Name:  "password, p",
					Usage: "password to connect with, if any",
				},
				cli.StringFlag{
					Name:  "identity, d",
					Usage: "identity file to connect with",
				},
				cli.BoolFlag{
					Name:  "no-agent, n",
					Usage: "Do not attempt to use a ssh-agent",
				},
			}, commonFlags...),
		},
		cli.Command{
			Name:      "exec",
			ShortName: "e",
			Usage:     "Execute a local command in parallel",
			ArgsUsage: "-- [ command ]",
			Action:    execCommand,
			Flags:     commonFlags,
		},
	}

	if err := app.Run(os.Args); err != nil {
		fmt.Fprintf(os.Stderr, errors.Wrap(err, "runtime error (try --help)").Error()+"\n")
		os.Exit(1)
	}
}

func runN(items []string, count uint, fun func(tid uint, item string) error) []error {
	if uint(len(items)) > count {
		count = uint(len(items))
	} else {
		newItems := make([]string, count)
		copy(newItems, items)
		items = newItems
	}

	errChan := make(chan error, count)
	for i := uint(0); i < count; i++ {
		go func(tid uint, item string) {
			errChan <- fun(tid, item)
		}(i, items[i])
	}

	var outerErrs []error

	for i := uint(0); i < count; i++ {
		if err := <-errChan; err != nil {
			outerErrs = append(outerErrs, err)
		}
	}

	return outerErrs
}

func processErrors(errs []error) error {
	for _, err := range errs {
		fmt.Fprintf(os.Stderr, err.Error()+"\n")
	}

	if len(errs) > 0 {
		return errors.New("some commands had errors")
	}

	return nil
}

func format(fmtstr string, tid uint, item string) string {
	var retval string

	var lastPercent bool

	for _, c := range fmtstr {
		if c == '%' {
			if lastPercent == true {
				retval = "%"
				lastPercent = false
				continue
			}
			lastPercent = true
		} else {
			if lastPercent == true {
				switch c {
				case 't':
					retval += fmt.Sprintf("%d", tid)
				case 'i':
					retval += item
				}

				lastPercent = false
				continue
			}

			retval = string(append([]rune(retval), c))
		}
	}

	return retval
}

func readLines(f *os.File) ([]string, error) {
	content, err := ioutil.ReadAll(f)
	if err != nil {
		return nil, errors.Wrap(err, "reading i/o")
	}

	input := strings.Split(string(content), "\n")

	// catch trailing newline
	if input[len(input)-1] == "" {
		input = input[:len(input)-1]
	}
	return input, nil
}

func execCommand(ctx *cli.Context) error {
	if len(ctx.Args()) == 0 {
		return errors.New("must supply a command to run")
	}

	var input []string

	if ctx.Bool("input") {
		var err error
		if input, err = readLines(os.Stdin); err != nil {
			return errors.Wrap(err, "could not read from stdin")
		}
	}

	count := ctx.Uint("count")

	errs := runN(input, count, func(tid uint, item string) error {
		args := []string{}
		for _, arg := range ctx.Args() {
			args = append(args, format(arg, tid, item))
		}

		first := args[0]
		if len(args) == 1 {
			args = []string{}
		} else {
			args = args[1:]
		}

		cmd := exec.Command(first, args...)

		if !ctx.Bool("quiet") {
			outPipe, err := cmd.StdoutPipe()
			if err != nil {
				return errors.Wrap(err, "setting up stdout")
			}

			errPipe, err := cmd.StderrPipe()
			if err != nil {
				return errors.Wrap(err, "setting up stderr")
			}

			go io.Copy(os.Stdout, outPipe)
			go io.Copy(os.Stderr, errPipe)
		}

		if err := cmd.Run(); err != nil {
			return errors.Wrapf(err, "while running %v %v", first, args)
		}

		return nil
	})

	return processErrors(errs)
}

func sshCommand(ctx *cli.Context) error {
	if len(ctx.Args()) < 2 {
		return errors.New("must supply a host list file and command to run")
	}

	listFile := ctx.Args()[0]
	args := ctx.Args()[1:]

	f, err := os.Open(listFile)
	if err != nil {
		return errors.Wrap(err, "could not open host list file")
	}

	hosts, err := readLines(f)
	if err != nil {
		errors.Wrap(err, "could not read hosts file")
	}

	var input []string

	if ctx.Bool("input") {
		var err error
		if input, err = readLines(os.Stdin); err != nil {
			return errors.Wrap(err, "while reading from stdin")
		}
	}

	auths := []ssh.AuthMethod{}

	if ctx.String("password") != "" {
		auths = append(auths, ssh.Password(ctx.String("password")))
	}

	if ctx.String("identity") != "" {
		key, err := ioutil.ReadFile(ctx.String("identity"))
		if err != nil {
			return errors.Wrap(err, "unable to read private key")
		}

		signer, err := ssh.ParsePrivateKey(key)
		if err != nil {
			return errors.Wrap(err, "unable to parse private key")
		}

		auths = append(auths, ssh.PublicKeys(signer))
	}

	if len(auths) == 0 || (os.Getenv("SSH_AUTH_SOCK") != "" && !ctx.Bool("no-agent")) {
		conn, err := net.Dial("unix", os.Getenv("SSH_AUTH_SOCK"))
		if err != nil {
			return errors.Wrap(err, "connecting to ssh agent")
		}

		signers, err := agent.NewClient(conn).Signers()
		if err != nil {
			return errors.Wrap(err, "reading agent keys")
		}

		auths = append(auths, ssh.PublicKeys(signers...))
	}

	cc := &ssh.ClientConfig{
		User: ctx.String("username"),
		// FIXME I'm too lazy to fix this and I don't really need it. -erikh
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Auth:            auths,
	}

	count := ctx.Uint("count")

	errs := runN(input, count*uint(len(hosts)), func(tid uint, item string) error {
		// Connect to the remote server and perform the SSH handshake.
		host := hosts[tid/count]
		if !strings.Contains(host, ":") {
			host += ":22"
		}

		client, err := ssh.Dial("tcp", host, cc)
		if err != nil {
			return errors.Wrap(err, "unable to connect")
		}
		defer client.Close()

		s, err := client.NewSession()
		if err != nil {
			return errors.Wrap(err, "establishing session")
		}

		if !ctx.Bool("quiet") {
			outPipe, err := s.StdoutPipe()
			if err != nil {
				return errors.Wrap(err, "connecting to stdout")
			}

			errPipe, err := s.StderrPipe()
			if err != nil {
				return errors.Wrap(err, "connecting to stderr")
			}

			go io.Copy(os.Stdout, outPipe)
			go io.Copy(os.Stdout, errPipe)
		}

		if err := s.Run(format(strings.Join(args, " "), tid, item)); err != nil {
			return errors.Wrapf(err, "executing %v on %v", args, host)
		}

		return nil
	})

	return processErrors(errs)
}
