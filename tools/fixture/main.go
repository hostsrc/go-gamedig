// Command fixture records node-gamedig queries as replayable fixtures.
//
//	go run ./tools/fixture synth -gamedig-version 5.3.3 -out testdata
//	    Serves every synthetic scenario (scenarios.go) to node-gamedig and
//	    writes one fixture per scenario. These are committed.
//
//	go run ./tools/fixture record -gamedig-version 5.3.3 -type counterstrike16 -target 1.2.3.4:27015 -out x.json
//	    Proxies a real server while node-gamedig queries it. Recordings of
//	    third-party servers contain their names and players: keep them out of
//	    the repository.
//
// fixture_test.go replays every fixture against this module and requires
// the same JSON.
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"

	"github.com/hostsrc/go-gamedig/internal/fixture"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: fixture synth|record [flags]")
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "synth":
		err = synth(os.Args[2:])
	case "record":
		err = record(os.Args[2:])
	default:
		err = fmt.Errorf("unknown command %q", os.Args[1])
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "fixture:", err)
		os.Exit(1)
	}
}

func synth(args []string) error {
	fs := flag.NewFlagSet("synth", flag.ExitOnError)
	out := fs.String("out", "testdata", "output directory")
	bin := fs.String("gamedig", "gamedig", "node-gamedig command")
	ver := fs.String("gamedig-version", "", "node-gamedig version to record (required)")
	only := fs.String("only", "", "record only this scenario")
	_ = fs.Parse(args)
	if *ver == "" {
		return errors.New("-gamedig-version is required")
	}
	for _, sc := range scenarios() {
		if *only != "" && sc.Name != *only {
			continue
		}
		srv, err := fixture.Serve(sc.Exchanges)
		if err != nil {
			return err
		}
		expected, err := runGamedig(*bin, sc.Args, srv.Port())
		srv.Close()
		if err != nil {
			return fmt.Errorf("%s: %w", sc.Name, err)
		}
		if u := srv.Unmatched(); len(u) > 0 {
			return fmt.Errorf("%s: node-gamedig sent %d unexpected request(s), first %x", sc.Name, len(u), u[0])
		}
		if u := srv.Unused(); len(u) > 0 {
			return fmt.Errorf("%s: node-gamedig never sent the request of exchange(s) %v", sc.Name, u)
		}
		f := fixture.Fixture{
			Description:    sc.Description,
			Args:           sc.Args,
			Exchanges:      sc.Exchanges,
			GamedigVersion: *ver,
			Expected:       expected,
		}
		path := filepath.Join(*out, sc.Name+".json")
		if err := f.Save(path); err != nil {
			return err
		}
		fmt.Println("wrote", path)
	}
	return nil
}

func record(args []string) error {
	fs := flag.NewFlagSet("record", flag.ExitOnError)
	target := fs.String("target", "", "server query address, host:port")
	typ := fs.String("type", "", "game type")
	out := fs.String("out", "", "output file")
	bin := fs.String("gamedig", "gamedig", "node-gamedig command")
	ver := fs.String("gamedig-version", "", "node-gamedig version (required)")
	desc := fs.String("description", "recorded from a live server", "fixture description")
	_ = fs.Parse(args)
	if *target == "" || *typ == "" || *out == "" || *ver == "" {
		return errors.New("-target, -type, -out and -gamedig-version are required")
	}
	gdArgs := append([]string{"--type=" + *typ}, fs.Args()...)

	p, err := fixture.NewProxy(*target)
	if err != nil {
		return err
	}
	expected, err := runGamedig(*bin, gdArgs, p.Port())
	ex := p.Close()
	if err != nil {
		return err
	}
	f := fixture.Fixture{Description: *desc, Args: gdArgs, Exchanges: ex, GamedigVersion: *ver, Expected: expected}
	return f.Save(*out)
}

// runGamedig queries 127.0.0.1:port with node-gamedig and returns its
// normalized output.
func runGamedig(bin string, args []string, port int) (json.RawMessage, error) {
	full := append(append([]string{}, args...), "--givenPortOnly", "--port="+strconv.Itoa(port), "127.0.0.1")
	var stdout, stderr bytes.Buffer
	cmd := exec.Command(bin, full...)
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("%s: %w: %s", bin, err, stderr.String())
	}
	var v map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &v); err != nil {
		return nil, fmt.Errorf("parsing node-gamedig output %q: %w", stdout.String(), err)
	}
	return json.Marshal(fixture.Normalize(v, port))
}
