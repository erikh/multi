package main

import (
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"os/exec"
	"strings"

	"github.com/pkg/errors"
	"github.com/urfave/cli"
)

// Description is the long-form explanation of how to use the program.
const Description = `
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

func runN(items []string, count uint, fun func(tid uint, item string) error) error {
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

	var outerErr error

	for i := uint(0); i < count; i++ {
		if err := <-errChan; err != nil {
			outerErr = err
		}
	}

	return outerErr
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

func execCommand(ctx *cli.Context) error {
	if len(ctx.Args()) == 0 {
		return errors.New("must supply a command to run")
	}

	var input []string

	if ctx.Bool("input") {
		content, err := ioutil.ReadAll(os.Stdin)
		if err != nil {
			return err
		}

		input = strings.Split(string(content), "\n")

		// catch trailing newline
		if input[len(input)-1] == "" {
			input = input[:len(input)-1]
		}
	}

	count := ctx.Uint("count")

	return runN(input, count, func(tid uint, item string) error {
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
}

func sshCommand(ctx *cli.Context) error {
	if len(ctx.Args()) == 0 {
		return errors.New("must supply a command to run")
	}

	return nil
}
