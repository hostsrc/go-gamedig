// Command gamedig queries a game server and prints the result as JSON.
//
// It accepts node-gamedig's command line, so it can replace that command:
//
//	gamedig --type=counterstrike16 --port=27015 --givenPortOnly 127.0.0.1
//
// Like node-gamedig, a failed query prints {"error": "..."} and exits 0;
// pass --strict to exit 1 instead. Unknown flags are ignored, as minimist
// does.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	"github.com/hostsrc/go-gamedig"
)

// boolFlags are parsed as booleans, as in node-gamedig's bin/gamedig.js,
// plus this command's own --strict, --list and --version.
var boolFlags = map[string]bool{
	"pretty": true, "debug": true, "givenPortOnly": true, "requestRules": true,
	"requestPlayers": true, "requestRulesRequired": true, "requestPlayersRequired": true,
	"stripColors": true, "portCache": true, "noBreadthOrder": true, "checkOldIDs": true,
	"rejectUnauthorized": true, "strict": true, "list": true, "version": true,
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	flags, positional := parseArgs(args)
	pretty := flags["pretty"] == "true" || flags["debug"] == "true"

	switch {
	case flags["version"] == "true":
		fmt.Fprintln(stdout, version())
		return 0
	case flags["list"] == "true":
		for _, g := range gamedig.Games() {
			state := "unsupported"
			if g.Supported() {
				state = "supported"
			}
			fmt.Fprintf(stdout, "%s\t%s\t%s\t%s\n", g.ID, g.Protocol, state, g.Name)
		}
		return 0
	}

	opts, err := buildOptions(flags, positional)
	if err == nil {
		if flags["debug"] == "true" {
			opts.Debugf = func(format string, a ...any) { fmt.Fprintf(stderr, format+"\n", a...) }
		}
		var res *gamedig.Result
		res, err = gamedig.Query(context.Background(), opts)
		if err == nil {
			if err = printJSON(stdout, res, pretty); err == nil {
				return 0
			}
		}
	}
	if flags["debug"] == "true" {
		printErrorTree(stderr, err, "")
	}
	msg := err.Error()
	var attempts *gamedig.AttemptsError
	if errors.As(err, &attempts) {
		msg = attempts.Summary()
	}
	_ = printJSON(stdout, map[string]string{"error": msg}, pretty)
	if flags["strict"] == "true" {
		return 1
	}
	return 0
}

func buildOptions(flags map[string]string, positional []string) (gamedig.Options, error) {
	opts := gamedig.Options{
		Type:           flags["type"],
		Host:           flags["host"],
		GivenPortOnly:  flags["givenPortOnly"] == "true",
		CheckOldIDs:    flags["checkOldIDs"] == "true",
		NoBreadthOrder: flags["noBreadthOrder"] == "true",
		SkipPlayers:    flags["requestPlayers"] == "false",
		RequestRules:   flags["requestRules"] == "true",
		// node-gamedig defaults stripColors to true.
		KeepColors: flags["stripColors"] == "false",
	}
	if len(positional) > 0 {
		// node-gamedig splits the target on ':' into host and port.
		parts := strings.Split(positional[0], ":")
		opts.Host = parts[0]
		if len(parts) > 1 {
			flags["port"] = parts[1]
		}
	}
	// node-gamedig parses numbers with parseInt; an unparsable value is NaN,
	// which it treats as unset.
	opts.Port = intFlag(flags, "port")
	opts.MaxRetries = intFlag(flags, "maxRetries")
	opts.SocketTimeout = time.Duration(intFlag(flags, "socketTimeout")) * time.Millisecond
	opts.AttemptTimeout = time.Duration(intFlag(flags, "attemptTimeout")) * time.Millisecond
	return opts, nil
}

// intFlag parses a decimal flag the way JavaScript's parseInt does: leading
// digits count, anything unparsable is 0 (unset).
func intFlag(flags map[string]string, name string) int {
	v := strings.TrimSpace(flags[name])
	end := 0
	if end < len(v) && (v[0] == '-' || v[0] == '+') {
		end++
	}
	for end < len(v) && v[end] >= '0' && v[end] <= '9' {
		end++
	}
	n, err := strconv.Atoi(v[:end])
	if err != nil {
		return 0
	}
	return n
}

// parseArgs follows minimist: --k=v, --k v (unless k is boolean or the next
// argument is a flag), --k and --no-k for booleans. A repeated flag keeps
// its last value.
func parseArgs(args []string) (map[string]string, []string) {
	flags := map[string]string{"requestPlayers": "true"}
	var positional []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			positional = append(positional, args[i+1:]...)
			break
		}
		if !strings.HasPrefix(a, "-") || a == "-" {
			positional = append(positional, a)
			continue
		}
		key := strings.TrimLeft(a, "-")
		if k, v, ok := strings.Cut(key, "="); ok {
			if boolFlags[k] {
				// minimist: any value but "false" sets a boolean flag.
				v = strconv.FormatBool(v != "false")
			}
			flags[k] = v
			continue
		}
		if k, ok := strings.CutPrefix(key, "no-"); ok {
			flags[k] = "false"
			continue
		}
		if boolFlags[key] {
			flags[key] = "true"
			if i+1 < len(args) && (args[i+1] == "true" || args[i+1] == "false") {
				flags[key] = args[i+1]
				i++
			}
			continue
		}
		if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
			flags[key] = args[i+1]
			i++
			continue
		}
		flags[key] = "true"
	}
	return flags, positional
}

// printJSON writes v as one JSON document. It encodes to memory first so a
// failure never leaves partial output.
func printJSON(w io.Writer, v any, pretty bool) error {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if pretty {
		enc.SetIndent("", "  ")
	}
	if err := enc.Encode(v); err != nil {
		return err
	}
	_, err := w.Write(buf.Bytes())
	return err
}

func printErrorTree(w io.Writer, err error, indent string) {
	fmt.Fprintf(w, "%s%v\n", indent, err)
	if multi, ok := err.(interface{ Unwrap() []error }); ok {
		for _, e := range multi.Unwrap() {
			printErrorTree(w, e, indent+"  ")
		}
	}
}

// buildVersion is set by release builds with -ldflags "-X main.buildVersion=v1.2.3".
var buildVersion string

func version() string {
	v := buildVersion
	if info, ok := debug.ReadBuildInfo(); ok && v == "" && info.Main.Version != "(devel)" {
		v = info.Main.Version
	}
	if v == "" {
		v = "devel"
	}
	return "gamedig (go-gamedig) " + v
}
