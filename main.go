package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/alecthomas/chroma/quick"
	"github.com/alecthomas/kong"
	kongyaml "github.com/alecthomas/kong-yaml"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	logFmt = `{"time": "%s", "name": "%s", "result": %s}
`
	urlFmt = "%s/api/v1/query?query=%s"
)

type Configuration struct {
	Queries map[string]Query
}

type Query struct {
	Server   string
	PromQL   string
	Interval metav1.Duration
}

func (q *Query) Get() ([]byte, error) {
	response, err := http.Get(fmt.Sprintf(urlFmt, q.Server, url.QueryEscape(q.PromQL)))
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	return io.ReadAll(response.Body)
}

func (q *Query) Log(name string) {
	b, err := q.Get()
	if err != nil {
		fmt.Printf(logFmt, time.Now(), name, err)
	}
	fmt.Printf(logFmt, time.Now(), name, b)
}

func prettyJSON(str string) (string, error) {
	var pj bytes.Buffer
	if err := json.Indent(&pj, []byte(str), "", "  "); err != nil {
		return "", err
	}
	return pj.String(), nil
}

func prettyQuery(name string, query Query, f formatOps) error {
	if f.Plain {
		f.NoColour = true
		f.NoPrettyJSON = true
	}

	if !f.NoColour {
		o, _ := os.Stdout.Stat()
		if (o.Mode() & os.ModeCharDevice) != os.ModeCharDevice {
			// output is not a terminal
			f.NoColour = true
			f.NoPrettyJSON = true
		}
	}

	b, err := query.Get()
	if err != nil {
		return err
	}
	res := fmt.Sprintf(logFmt, time.Now(), name, b)
	if !f.NoPrettyJSON {
		var err error
		res, err = prettyJSON(res)
		if err != nil {
			return err
		}
	}

	if f.NoColour {
		fmt.Print(res)
		return nil
	}

	return quick.Highlight(os.Stdout, res, "json", "terminal", "native")
}

type formatOps struct {
	NoPrettyJSON bool `help:"Disable JSON pretty printing"`
	NoColour     bool `help:"Disable coloured output"`
	Plain        bool `short:"P" help:"Disable JSON pretty printing and colors"`
}

type baseCMD struct {
	Config kong.ConfigFlag `short:"c" type:"path" help:"Path to the config file"`
	Debug  bool            `short:"d" help:" Enable debug output" env:"DEBUG"`
}

type StartCMD baseCMD

func (s *StartCMD) Run(c *Configuration) error {
	ctx, cancel := context.WithCancel(context.Background())
	for name, query := range c.Queries {
		go func(name string, q Query) {
			q.Log(name)
			ticker := time.NewTicker(q.Interval.Duration)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					q.Log(name)
				}
			}
		}(name, query)
	}
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
	<-signals
	cancel()
	return nil
}

type RunCMD struct {
	formatOps
	baseCMD
}

func (r *RunCMD) Run(c *Configuration) error {
	for name, query := range c.Queries {
		if err := prettyQuery(name, query, r.formatOps); err != nil {
			return err
		}
	}
	return nil
}

type QueryCMD struct {
	formatOps
	Name   string
	Server string `arg:""`
	Query  string `arg:""`
}

func (q *QueryCMD) Run() error {
	query := Query{
		Server: q.Server,
		PromQL: q.Query,
	}
	return prettyQuery(q.Name, query, q.formatOps)
}

func main() {
	var cli struct {
		Configuration
		Start StartCMD `cmd:"" help:"Start the server."`
		Run   RunCMD   `cmd:"" help:"run once."`
		Query QueryCMD `cmd:"" help:"run the given query."`
	}

	ctx := kong.Parse(&cli, kong.Configuration(kongyaml.Loader, "./config.yaml"))
	err := ctx.Run(&cli.Configuration)
	ctx.FatalIfErrorf(err)
}
